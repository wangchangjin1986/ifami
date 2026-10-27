// 加入家庭落地：微信授权 → 手机号 → 选称谓 → 关注服务号 → 进入家庭，对齐 prototype scr-invite。
const app = getApp()

const RELATIONS = ['奶奶', '爷爷', '外婆', '外公']

Page({
  data: {
    inviteToken: '',
    familyName: '家庭',
    relations: RELATIONS,
    relationIndex: -1,
    joining: false
  },

  onLoad(options) {
    this.setData({
      inviteToken: options.invite_token || '',
      familyName: options.family ? decodeURIComponent(options.family) : '家庭'
    })
  },

  selectRelation(e) {
    this.setData({ relationIndex: Number(e.currentTarget.dataset.i) })
  },

  join() {
    if (this.data.joining) return
    if (this.data.relationIndex < 0) return wx.showToast({ title: '请选择你是孩子的？', icon: 'none' })
    if (!this.data.inviteToken) return wx.showToast({ title: '缺少邀请令牌，请通过邀请链接进入', icon: 'none' })

    const relation = this.data.relations[this.data.relationIndex]
    this.setData({ joining: true })
    wx.login({
      success: (r) => {
        app.request('/v1/families/join', {
          method: 'POST',
          data: { code: r.code, invite_token: this.data.inviteToken, relation_label: relation }
        })
          .then((data) => {
            app.globalData.token = data.token
            wx.setStorageSync('token', data.token)
            return app.refreshMe()
          })
          .then(() => {
            wx.showToast({ title: '已加入「' + this.data.familyName + '」', icon: 'success' })
            setTimeout(() => wx.switchTab({ url: '/pages/today/index' }), 600)
          })
          .catch((e) => {
            this.setData({ joining: false })
            wx.showToast({ title: e.message || '加入失败', icon: 'none' })
          })
      },
      fail: () => {
        this.setData({ joining: false })
        wx.showToast({ title: '微信登录失败', icon: 'none' })
      }
    })
  },

  goBack() { wx.navigateBack() }
})
