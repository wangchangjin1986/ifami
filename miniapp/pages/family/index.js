// 家庭页：登录入口 + 建家庭 + 成员列表（avatar + 角色 chip），对齐 prototype scr-family。
const app = getApp()

const ROLE_LABEL = { admin: '管理员', parent: '家长', grandparent: '祖辈', child: '孩子' }

function memberView(m) {
  const isVirtual = m.type === 'virtual'
  let chip = ''
  let meta = ''
  if (isVirtual) {
    chip = '孩子档案'
    meta = (m.relation_label ? m.relation_label + ' · ' : '') + '虚拟成员，由父母代管'
  } else {
    if (m.role === 'admin') chip = '管理员'
    meta = ROLE_LABEL[m.role] || m.role
    if (m.relation_label) meta += ' · ' + m.relation_label
  }
  return {
    member_id: m.member_id,
    avatar: (m.name || '?').slice(0, 1),
    name: m.name || '未命名',
    chip,
    meta,
    isVirtual
  }
}

Page({
  data: {
    loading: true,
    logged: false,
    hasFamily: false,
    familyName: '',
    members: [],
    newFamilyName: '',
    childName: '',
    childRelation: '',
    inviteToken: '',
    sheetOpen: false
  },

  onShow() {
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 3 })
    }
    this.refresh()
  },

  refresh() {
    this.setData({ loading: true })
    return app.ensureLogin()
      .then(() => app.request('/v1/families'))
      .then((families) => {
        const list = families || []
        if (list.length === 0) {
          this.setData({ loading: false, logged: true, hasFamily: false, members: [], familyName: '' })
          return
        }
        const cur = list.find((f) => f.family_id === app.globalData.familyId) || list[0]
        app.setFamily(cur.family_id)
        this.setData({ logged: true, hasFamily: true, familyName: cur.name })
        return app.request(`/v1/families/${cur.family_id}/members`).then((members) => {
          this.setData({ members: (members || []).map(memberView), loading: false })
        })
      })
      .catch((e) => {
        this.setData({ loading: false })
        if (e.code === 4001) {
          app.logout()
          this.setData({ logged: false, hasFamily: false, members: [] })
        }
        wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  doLogin() {
    app.login()
      .then(() => this.refresh())
      .catch((e) => wx.showToast({ title: e.message || '登录失败', icon: 'none' }))
  },

  onNewFamily(e) { this.setData({ newFamilyName: e.detail.value }) },
  createFamily() {
    const name = (this.data.newFamilyName || '').trim()
    if (!name) return wx.showToast({ title: '请输入家庭名', icon: 'none' })
    app.request('/v1/families', { method: 'POST', data: { name } })
      .then((r) => {
        app.setFamily(r.family_id)
        this.setData({ newFamilyName: '' })
        return app.refreshMe()
      })
      .then(() => this.refresh())
      .catch((e) => wx.showToast({ title: e.message || '创建失败', icon: 'none' }))
  },

  onChildName(e) { this.setData({ childName: e.detail.value }) },
  onChildRelation(e) { this.setData({ childRelation: e.detail.value }) },
  addChild() {
    const name = (this.data.childName || '').trim()
    if (!name) return wx.showToast({ title: '请输入孩子姓名', icon: 'none' })
    const fid = app.globalData.familyId
    app.request(`/v1/families/${fid}/children`, {
      method: 'POST',
      data: { name, relation_label: this.data.childRelation }
    })
      .then(() => {
        this.setData({ childName: '', childRelation: '' })
        this.refresh()
      })
      .catch((e) => wx.showToast({ title: e.message || '添加失败', icon: 'none' }))
  },

  genInvite() {
    const fid = app.globalData.familyId
    app.request(`/v1/families/${fid}/invitations`, { method: 'POST', data: {} })
      .then((r) => this.setData({ inviteToken: r.invite_token }))
      .catch((e) => wx.showToast({ title: e.message || '生成失败', icon: 'none' }))
  },

  copyInvite() {
    if (!this.data.inviteToken) return
    wx.setClipboardData({ data: this.data.inviteToken })
  },

  goManage() { wx.navigateTo({ url: '/pages/manage/index' }) },
  openFab() { this.setData({ sheetOpen: true }) },
  closeFab() { this.setData({ sheetOpen: false }) },
  noop() {},
  goEvent() {
    this.closeFab()
    wx.navigateTo({ url: '/pages/event/index' })
  },
  goTask() {
    this.closeFab()
    wx.navigateTo({ url: '/pages/task/index' })
  },
  goReport() { wx.showToast({ title: '家庭周报开发中', icon: 'none' }) },
  goTimeline() { wx.showToast({ title: '成长时间线开发中', icon: 'none' }) },
  goSettings() { wx.showToast({ title: '设置页开发中', icon: 'none' }) },

  onPullDownRefresh() {
    this.refresh().then(() => wx.stopPullDownRefresh())
  }
})
