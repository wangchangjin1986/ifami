// 家庭页：登录入口 + 建家庭 + 成员列表（avatar + 角色 chip），对齐 prototype scr-family。
// 管理员可点成员卡片进入管理面板：身份配置 + 管理员开关 + 移除（对齐 prototype scr-member）。
const app = getApp()

const ROLE_LABEL = { admin: '管理员', parent: '家长', grandparent: '祖辈', child: '孩子' }
// 身份预设（relation_label），对齐 prototype scr-member 的 chips；孩子用另一组。
const RELATIONS_REAL = ['爸爸', '妈妈', '爷爷', '奶奶', '姥姥', '姥爷', '叔叔', '阿姨']
const RELATIONS_CHILD = ['儿子', '女儿']
// 取消管理员时按称谓回落角色：祖辈称谓 → grandparent，其余 → parent。
const GRAND_RELATIONS = ['爷爷', '奶奶', '姥姥', '姥爷']

function memberView(m, selfUserID) {
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
    isVirtual,
    role: m.role,
    relation: m.relation_label || '',
    isSelf: !isVirtual && selfUserID != null && m.user_id === selfUserID
  }
}

Page({
  data: {
    loading: true,
    logged: false,
    hasFamily: false,
    isAdmin: false,
    familyName: '',
    members: [],
    newFamilyName: '',
    childName: '',
    childRelation: '',
    inviteToken: '',
    sheetOpen: false,
    memberSheet: { open: false }
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
        this.setData({
          logged: true,
          hasFamily: true,
          isAdmin: app.globalData.role === 'admin',
          familyName: cur.name
        })
        return app.request(`/v1/families/${cur.family_id}/members`).then((members) => {
          const selfID = app.globalData.user ? app.globalData.user.user_id : null
          this.setData({ members: (members || []).map((m) => memberView(m, selfID)), loading: false })
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

  // ---- 成员管理（仅管理员；对齐 prototype scr-member） ----

  openMember(e) {
    if (!this.data.isAdmin) return
    const id = e.currentTarget.dataset.id
    const m = this.data.members.find((x) => String(x.member_id) === String(id))
    if (!m) return
    const base = m.isVirtual ? RELATIONS_CHILD : RELATIONS_REAL
    // 当前身份不在预设里（自定义称谓）时，并入 chips 列表一并展示。
    const relations = m.relation && base.indexOf(m.relation) < 0 ? base.concat([m.relation]) : base
    this.setData({
      memberSheet: {
        open: true,
        member_id: m.member_id,
        name: m.name,
        chip: m.chip,
        isVirtual: m.isVirtual,
        isSelf: m.isSelf,
        relations,
        relation: m.relation,
        customRelation: '',
        isAdmin: m.role === 'admin',
        originRelation: m.relation,
        originIsAdmin: m.role === 'admin'
      }
    })
  },

  closeMember() { this.setData({ 'memberSheet.open': false }) },

  pickRelation(e) {
    this.setData({
      'memberSheet.relation': e.currentTarget.dataset.rel,
      'memberSheet.customRelation': ''
    })
  },

  toggleAdmin() {
    this.setData({ 'memberSheet.isAdmin': !this.data.memberSheet.isAdmin })
  },

  saveMember() {
    const s = this.data.memberSheet
    const fid = app.globalData.familyId
    const relation = (s.customRelation || '').trim() || s.relation
    const payload = {}
    if (relation !== s.originRelation) payload.relation_label = relation
    if (!s.isVirtual && s.isAdmin !== s.originIsAdmin) {
      payload.role = s.isAdmin ? 'admin' : (GRAND_RELATIONS.indexOf(relation) >= 0 ? 'grandparent' : 'parent')
    }
    if (Object.keys(payload).length === 0) {
      this.closeMember()
      return
    }
    app.request(`/v1/families/${fid}/members/${s.member_id}`, { method: 'PATCH', data: payload })
      .then(() => {
        wx.showToast({ title: '已保存', icon: 'success' })
        this.closeMember()
        // 改的是自己的角色时刷新会话里的 memberships，避免权限状态陈旧。
        if (s.isSelf && payload.role) return app.refreshMe()
      })
      .then(() => this.refresh())
      .catch((e) => wx.showToast({ title: e.message || '保存失败', icon: 'none' }))
  },

  removeMember() {
    const s = this.data.memberSheet
    const fid = app.globalData.familyId
    wx.showModal({
      title: s.isSelf ? '退出家庭' : '移除成员',
      content: s.isSelf
        ? '退出后需重新被邀请才能加入。虚拟孩子档案不受影响。'
        : `将「${s.name}」移出家庭？孩子档案不受影响。`,
      confirmText: s.isSelf ? '退出' : '移除',
      confirmColor: '#BF4A3C',
      success: (r) => {
        if (!r.confirm) return
        app.request(`/v1/families/${fid}/members/${s.member_id}`, { method: 'DELETE' })
          .then(() => {
            wx.showToast({ title: s.isSelf ? '已退出' : '已移除', icon: 'success' })
            this.closeMember()
            if (s.isSelf) return app.refreshMe()
          })
          .then(() => this.refresh())
          .catch((e) => wx.showToast({ title: e.message || '操作失败', icon: 'none' }))
      }
    })
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
