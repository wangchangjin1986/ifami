// ifami 小程序入口
// 账号体系：wx.login → code → 后端换取 openid + token（见 docs/技术方案设计.md §5.1）
// 登录后 token 持久化到 storage，请求统一注入 Authorization / X-Family-Id。
function normalizeRole(role) {
  return role === 'admin' ? 'parent' : role
}

function isMembershipAdmin(m) {
  return !!(m && (m.is_admin || m.role === 'admin'))
}

function normalizeMemberships(list) {
  return (list || []).map((m) => Object.assign({}, m, {
    role: normalizeRole(m.role),
    is_admin: isMembershipAdmin(m)
  }))
}

const ME_REFRESH_TTL_MS = 5 * 60 * 1000
const REQUEST_RETRY_DELAY_MS = 600

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function normalizeMethod(method) {
  return String(method || 'GET').toUpperCase()
}

function networkCategory(errMsg) {
  const msg = String(errMsg || '').toLowerCase()
  if (msg.includes('url not in domain list') || msg.includes('domain list')) return 'domain'
  if (msg.includes('ssl') || msg.includes('tls') || msg.includes('certificate') || msg.includes('handshake')) return 'tls'
  if (msg.includes('timeout') || msg.includes('timed out')) return 'timeout'
  if (msg.includes('interrupted') || msg.includes('abort')) return 'interrupted'
  if (msg.includes('fail') || msg.includes('connect') || msg.includes('refused') || msg.includes('reset')) return 'connect'
  return 'unknown'
}

function networkMessage(category) {
  if (category === 'domain') return '当前版本未配置后端合法域名，请更新体验版或检查小程序后台配置'
  if (category === 'tls') return '后端 HTTPS 证书异常，请稍后再试'
  if (category === 'timeout') return '网络请求超时，请稍后重试'
  if (category === 'connect') return '后端服务暂时不可用，请稍后重试'
  if (category === 'interrupted') return '网络请求已中断，请重试'
  return '网络异常，请稍后重试'
}

function retryableStatus(statusCode) {
  return statusCode === 502 || statusCode === 503 || statusCode === 504
}

function newClientRequestId() {
  return 'mp-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8)
}

function makeNetworkError({ path, url, method, requestId, err, startedAt, attempt, maxAttempts }) {
  const errMsg = err && err.errMsg ? err.errMsg : ''
  const category = networkCategory(errMsg)
  const e = new Error(networkMessage(category))
  e.code = 'NETWORK_FAIL'
  e.networkCategory = category
  e.errMsg = errMsg
  e.cause = err
  e.url = url
  e.path = path
  e.method = method
  e.requestId = requestId
  e.duration = Date.now() - startedAt
  e.attempt = attempt
  e.maxAttempts = maxAttempts
  e.retryable = method === 'GET' && attempt < maxAttempts && category !== 'domain' && category !== 'tls'
  return e
}

function logRequestFailure(err) {
  const payload = {
    requestId: err.requestId,
    path: err.path,
    method: err.method,
    statusCode: err.statusCode,
    code: err.code,
    category: err.networkCategory,
    errMsg: err.errMsg,
    duration: err.duration,
    attempt: err.attempt,
    maxAttempts: err.maxAttempts
  }
  if (typeof wx !== 'undefined' && wx.getNetworkType) {
    wx.getNetworkType({
      success: (res) => console.warn('[ifami] request failed', Object.assign(payload, { networkType: res.networkType })),
      fail: () => console.warn('[ifami] request failed', payload)
    })
    return
  }
  console.warn('[ifami] request failed', payload)
}

App({
  globalData: {
    // 默认指向备案后的 HTTPS 域名；本地联调改回 http://127.0.0.1:8080。
    apiBase: 'https://api.ifami.site',
    requestTimeout: 15000,
    token: '',
    user: null,        // {user_id, nick, avatar}
    memberships: [],   // [{family_id, role, is_admin}]
    familyId: null,    // 当前家庭（默认第一个）
    role: null,        // parent|grandparent|child
    isAdmin: false,
    meRefreshAt: 0     // 最近一次 /v1/me 成功时间，用于避免切 Tab 时反复校验
  },

  onLaunch() {
    this.globalData.token = wx.getStorageSync('token') || ''
    this.globalData.user = wx.getStorageSync('user') || null
    this.globalData.memberships = normalizeMemberships(wx.getStorageSync('memberships'))
    this.globalData.familyId = wx.getStorageSync('familyId') || null
    this.globalData.role = wx.getStorageSync('role') || null
    this.globalData.isAdmin = !!wx.getStorageSync('isAdmin')
    this.globalData.meRefreshAt = Number(wx.getStorageSync('meRefreshAt') || 0)
  },

  onShow(options) {
    this.routeUnauthedEntry(options)
  },

  // 冷启动无会话时，默认落到「家庭」Tab，避免首屏停在今日/日历/打卡的“请先登录”空态。
  routeUnauthedEntry(options = {}) {
    if (this._routedUnauthedEntry) return
    if (this.globalData.token && this.globalData.user) return
    const path = options.path || 'pages/today/index'
    if (path === 'pages/family/index' || path === 'pages/invite/index') return
    const tabPages = ['pages/today/index', 'pages/week/index', 'pages/daka/index']
    if (!tabPages.includes(path)) return
    this._routedUnauthedEntry = true
    setTimeout(() => wx.switchTab({ url: '/pages/family/index' }), 0)
  },

  // 统一请求：Promise 化 wx.request，注入鉴权头，解析 {code, data, message}。
  // extraHeader 用于业务自定义头（如打卡提交的 Idempotency-Key）。
  request(path, { method = 'GET', data = {}, familyId = null, header: extraHeader = {}, retry = null } = {}) {
    method = normalizeMethod(method)
    const header = Object.assign({ 'Content-Type': 'application/json' }, extraHeader)
    if (!header['X-Request-Id']) header['X-Request-Id'] = newClientRequestId()
    if (this.globalData.token) header.Authorization = 'Bearer ' + this.globalData.token
    const fid = familyId !== null ? familyId : this.globalData.familyId
    if (fid) header['X-Family-Id'] = String(fid)
    const url = this.globalData.apiBase + path
    const maxAttempts = retry === null ? (method === 'GET' ? 2 : 1) : (retry ? 2 : 1)

    const once = (attempt) => new Promise((resolve, reject) => {
      const startedAt = Date.now()
      wx.request({
        url,
        method,
        data,
        header,
        timeout: this.globalData.requestTimeout,
        success: (res) => {
          const body = res.data || {}
          if (res.statusCode === 200 && body.code === 0) {
            resolve(body.data)
            return
          }
          const err = new Error(body.message || '请求失败')
          err.code = body.code
          err.statusCode = res.statusCode
          err.data = body.data
          err.url = url
          err.path = path
          err.method = method
          err.requestId = header['X-Request-Id']
          err.duration = Date.now() - startedAt
          err.attempt = attempt
          err.maxAttempts = maxAttempts
          if (method === 'GET' && retryableStatus(res.statusCode) && attempt < maxAttempts) {
            sleep(REQUEST_RETRY_DELAY_MS * attempt).then(() => once(attempt + 1).then(resolve).catch(reject))
            return
          }
          if (res.statusCode >= 500) logRequestFailure(err)
          reject(err)
        },
        fail: (err) => {
          const e = makeNetworkError({ path, url, method, requestId: header['X-Request-Id'], err, startedAt, attempt, maxAttempts })
          if (e.retryable) {
            logRequestFailure(e)
            sleep(REQUEST_RETRY_DELAY_MS * attempt).then(() => once(attempt + 1).then(resolve).catch(reject))
            return
          }
          logRequestFailure(e)
          reject(e)
        }
      })
    })

    return once(1)
  },

  // 登录：wx.login → code → POST /v1/auth/login，返回 token/user/memberships。
  login() {
    return new Promise((resolve, reject) => {
      wx.login({
        success: (r) => {
          this.request('/v1/auth/login', { method: 'POST', data: { code: r.code } })
            .then((data) => {
              this.setSession(data)
              resolve(data)
            })
            .catch(reject)
        },
        fail: (e) => reject(new Error('wx.login 失败'))
      })
    })
  },

  // 落会话：token/user/memberships 持久化，并选中默认家庭。
  setSession(data) {
    this.globalData.token = data.token || ''
    this.globalData.user = data.user || null
    this.globalData.memberships = normalizeMemberships(data.memberships)
    this.markMeFresh()
    wx.setStorageSync('token', this.globalData.token)
    wx.setStorageSync('user', this.globalData.user)
    wx.setStorageSync('memberships', this.globalData.memberships)
    const list = this.globalData.memberships
    if (list.length > 0) {
      this.setFamily(this.globalData.familyId || list[0].family_id)
    } else {
      this.setFamily(null)
    }
  },

  // 切当前家庭并同步 role。
  setFamily(familyId) {
    this.globalData.familyId = familyId || null
    wx.setStorageSync('familyId', this.globalData.familyId)
    if (familyId) {
      const m = this.globalData.memberships.find((x) => String(x.family_id) === String(familyId))
      this.globalData.role = m ? normalizeRole(m.role) : null
      this.globalData.isAdmin = isMembershipAdmin(m)
    } else {
      this.globalData.role = null
      this.globalData.isAdmin = false
    }
    wx.setStorageSync('role', this.globalData.role)
    wx.setStorageSync('isAdmin', this.globalData.isAdmin)
  },

  markMeFresh() {
    this.globalData.meRefreshAt = Date.now()
    wx.setStorageSync('meRefreshAt', this.globalData.meRefreshAt)
  },

  // 刷新当前用户与家庭列表（创建/加入家庭后调用，重新拉 memberships）。
  refreshMe() {
    return this.request('/v1/me').then((data) => {
      this.globalData.user = data.user || null
      this.globalData.memberships = normalizeMemberships(data.memberships)
      this.markMeFresh()
      wx.setStorageSync('user', this.globalData.user)
      wx.setStorageSync('memberships', this.globalData.memberships)
      const list = this.globalData.memberships
      if (list.length === 0) {
        this.setFamily(null)
      } else if (!list.some((x) => String(x.family_id) === String(this.globalData.familyId))) {
        this.setFamily(list[0].family_id)
      } else {
        this.setFamily(this.globalData.familyId)
      }
      return data
    })
  },

  // 带 TTL 的会话校验：短时间内切 Tab/回前台不重复阻塞在 /v1/me。
  maybeRefreshMe({ force = false } = {}) {
    if (!this.globalData.token || !this.globalData.user) return this.login()
    const fresh = Date.now() - (this.globalData.meRefreshAt || 0) < ME_REFRESH_TTL_MS
    if (!force && fresh) {
      return Promise.resolve({ user: this.globalData.user, memberships: this.globalData.memberships })
    }
    return this.refreshMe()
  },

  // 确保已登录（页面 onShow 调用）；token 失效时会重新登录。
  ensureLogin() {
    if (this.globalData.token && this.globalData.user) {
      // /v1/me 带 TTL，避免每次切页面都串行请求；创建/加入/成员变更后仍会显式 refreshMe。
      return this.maybeRefreshMe().catch((e) => {
        if (e.code === 4001) {
          this.logout()
          return this.login()
        }
        throw e
      })
    }
    return this.login()
  },

  logout() {
    this.globalData.token = ''
    this.globalData.user = null
    this.globalData.memberships = []
    this.globalData.isAdmin = false
    wx.removeStorageSync('token')
    wx.removeStorageSync('user')
    wx.removeStorageSync('memberships')
    wx.removeStorageSync('isAdmin')
    wx.removeStorageSync('meRefreshAt')
    this.setFamily(null)
  }
})
