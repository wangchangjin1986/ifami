// 加入家庭落地：微信授权 → 提醒通道说明 → 选称谓 → 进入家庭，对齐 prototype scr-invite。
const app = getApp()

const INVITE_COPY = {
  parent: {
    desc: '你被邀请成为家长成员，一起管理孩子的日程和打卡。',
    relationTitle: '你的身份是？',
    relations: ['妈妈', '爸爸', '其他家长'],
    successUrl: '/pages/today/index'
  },
  grandparent: {
    desc: '你被邀请查看孩子今天的接送和活动安排。',
    relationTitle: '你是孩子的？',
    relations: ['奶奶', '爷爷', '姥姥', '姥爷'],
    successUrl: '/pages/gp-today/index'
  },
  caregiver: {
    desc: '你被邀请协助照看孩子，只会看到今天需要你处理的安排。',
    relationTitle: '你的身份是？',
    relations: ['阿姨', '保姆阿姨', '接送阿姨'],
    successUrl: '/pages/gp-today/index'
  }
}

Page({
  data: {
    inviteToken: '',
    familyName: '家庭',
    inviteType: 'grandparent',
    isAdminInvite: false,
    desc: INVITE_COPY.grandparent.desc,
    relationTitle: INVITE_COPY.grandparent.relationTitle,
    relations: INVITE_COPY.grandparent.relations,
    relationIndex: -1,
    joining: false
  },

  onLoad(options) {
    const inviteType = INVITE_COPY[options.invite_type] ? options.invite_type : 'grandparent'
    const copy = INVITE_COPY[inviteType]
    this.setData({
      inviteToken: options.invite_token || '',
      familyName: options.family ? decodeURIComponent(options.family) : '家庭',
      inviteType,
      isAdminInvite: options.is_admin === '1',
      desc: copy.desc,
      relationTitle: copy.relationTitle,
      relations: copy.relations
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
            return app.refreshMe().then(() => data)
          })
          .then((data) => {
            wx.showToast({ title: '已加入「' + this.data.familyName + '」', icon: 'success' })
            const inviteType = data.invite_type || this.data.inviteType
            const target = inviteType === 'parent' ? '/pages/today/index' : '/pages/gp-today/index'
            setTimeout(() => wx.reLaunch({ url: target }), 600)
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
