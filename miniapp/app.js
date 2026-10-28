// ifami 小程序入口
// 账号体系：wx.login → code → 后端换取 openid + token（见 docs/技术方案设计.md §5.1）
// 登录后 token 持久化到 storage，请求统一注入 Authorization / X-Family-Id。
App({
  globalData: {
    // 默认指向 Phase 0 服务器（备案完成前 IP 直连 HTTP，开发者工具需勾选「不校验合法域名」）；
    // 本地联调改回 http://127.0.0.1:8080；备案完成后改为 https://api.ifami.site。
    apiBase: 'http://118.196.79.193',
    token: '',
    user: null,        // {user_id, nick, avatar}
    memberships: [],   // [{family_id, role}]
    familyId: null,    // 当前家庭（默认第一个）
    role: null         // admin|parent|grandparent
  },

  onLaunch() {
    this.globalData.token = wx.getStorageSync('token') || ''
    this.globalData.user = wx.getStorageSync('user') || null
    this.globalData.memberships = wx.getStorageSync('memberships') || []
    this.globalData.familyId = wx.getStorageSync('familyId') || null
    this.globalData.role = wx.getStorageSync('role') || null
  },

  // 统一请求：Promise 化 wx.request，注入鉴权头，解析 {code, data, message}。
  // extraHeader 用于业务自定义头（如打卡提交的 Idempotency-Key）。
  request(path, { method = 'GET', data = {}, familyId = null, header: extraHeader = {} } = {}) {
    const header = Object.assign({ 'Content-Type': 'application/json' }, extraHeader)
    if (this.globalData.token) header.Authorization = 'Bearer ' + this.globalData.token
    const fid = familyId !== null ? familyId : this.globalData.familyId
    if (fid) header['X-Family-Id'] = String(fid)

    return new Promise((resolve, reject) => {
      wx.request({
        url: this.globalData.apiBase + path,
        method,
        data,
        header,
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
          err.message = '网络请求失败（本地联调请确认后端已启动）'
          reject(err)
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
    this.globalData.memberships = data.memberships || []
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
      const m = this.globalData.memberships.find((x) => x.family_id === familyId)
      this.globalData.role = m ? m.role : null
    } else {
      this.globalData.role = null
    }
    wx.setStorageSync('role', this.globalData.role)
  },

  // 刷新当前用户与家庭列表（创建/加入家庭后调用，重新拉 memberships）。
  refreshMe() {
    return this.request('/v1/me').then((data) => {
      this.globalData.user = data.user || null
      this.globalData.memberships = data.memberships || []
      wx.setStorageSync('user', this.globalData.user)
      wx.setStorageSync('memberships', this.globalData.memberships)
      const list = this.globalData.memberships
      if (list.length === 0) {
        this.setFamily(null)
      } else if (!list.some((x) => x.family_id === this.globalData.familyId)) {
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
      return Promise.resolve()
    }
    return this.login()
  },

  logout() {
    this.globalData.token = ''
    this.globalData.user = null
    this.globalData.memberships = []
    wx.removeStorageSync('token')
    wx.removeStorageSync('user')
    wx.removeStorageSync('memberships')
    this.setFamily(null)
  }
})
