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
    isAdmin: false
  },

  onLaunch() {
    this.globalData.token = wx.getStorageSync('token') || ''
    this.globalData.user = wx.getStorageSync('user') || null
    this.globalData.memberships = normalizeMemberships(wx.getStorageSync('memberships'))
    this.globalData.familyId = wx.getStorageSync('familyId') || null
    this.globalData.role = wx.getStorageSync('role') || null
    this.globalData.isAdmin = !!wx.getStorageSync('isAdmin')
  },

  // 统一请求：Promise 化 wx.request，注入鉴权头，解析 {code, data, message}。
  // extraHeader 用于业务自定义头（如打卡提交的 Idempotency-Key）。
  request(path, { method = 'GET', data = {}, familyId = null, header: extraHeader = {} } = {}) {
    const header = Object.assign({ 'Content-Type': 'application/json' }, extraHeader)
    if (this.globalData.token) header.Authorization = 'Bearer ' + this.globalData.token
    const fid = familyId !== null ? familyId : this.globalData.familyId
    if (fid) header['X-Family-Id'] = String(fid)
    const url = this.globalData.apiBase + path

    return new Promise((resolve, reject) => {
      wx.request({
        url,
        method,
        data,
        header,
        timeout: this.globalData.requestTimeout,
        success(res) {
          const body = res.data || {}
          if (res.statusCode === 200 && body.code === 0) {
            resolve(body.data)
            return
          }
          const err = new Error(body.message || '请求失败')
          err.code = body.code
          err.statusCode = res.statusCode
          err.data = body.data
          reject(err)
        },
        fail(err) {
          const detail = err && err.errMsg ? `：${err.errMsg}` : ''
          const e = new Error(`网络请求失败，请检查小程序合法域名或后端 HTTPS 状态${detail}`)
          e.cause = err
          e.url = url
          reject(e)
        }
      })
    })
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

  // 刷新当前用户与家庭列表（创建/加入家庭后调用，重新拉 memberships）。
  refreshMe() {
    return this.request('/v1/me').then((data) => {
      this.globalData.user = data.user || null
      this.globalData.memberships = normalizeMemberships(data.memberships)
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

  // 确保已登录（页面 onShow 调用）；token 失效时会重新登录。
  ensureLogin() {
    if (this.globalData.token && this.globalData.user) {
      // 每次进入页面都刷新 /v1/me，避免小程序本地缓存仍是旧 memberships（缺少 is_admin）时，
      // 管理员被误判为普通成员，导致家庭成员编辑入口消失。
      return this.refreshMe().catch((e) => {
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
    this.setFamily(null)
  }
})
