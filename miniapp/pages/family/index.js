// 家庭页：登录入口 + 建家庭 + 成员列表（avatar + 角色 chip），对齐 prototype scr-family。
// 管理员可点成员卡片进入管理面板：名称/身份配置 + 角色选择 + 管理员设置 + 移除（对齐 prototype scr-member）。
const app = getApp()

const ROLE_LABEL = { admin: '管理员', parent: '家长', grandparent: '祖辈', child: '孩子' }
const ROLE_OPTIONS_REAL = [
  { value: 'parent', label: '家长' },
  { value: 'grandparent', label: '祖辈' }
]
// 真实成员的名称预设；孩子身份单独用儿子/女儿。
const RELATIONS_REAL = ['爸爸', '妈妈', '爷爷', '奶奶', '姥姥', '姥爷', '叔叔', '阿姨']
const RELATIONS_CHILD = ['儿子', '女儿']

function memberView(m, selfUserID) {
  const isVirtual = m.type === 'virtual'
  const role = m.role === 'admin' ? 'parent' : m.role
  const isAdmin = !!(m.is_admin || m.role === 'admin')
  // 卡片主标题：真实成员用名称（relation_label），孩子用姓名；角色只决定权限与视角。
  const display = isVirtual ? (m.name || '未命名') : (m.relation_label || m.name || '未命名')
  let chip = ''
  let meta = ''
  if (isVirtual) {
    chip = '孩子档案'
    // 孩子也显示名称（儿子/女儿或自定义），让编辑「自定义名称」保存后能反映到卡片上。
    meta = (m.relation_label ? m.relation_label + ' · ' : '') + '虚拟成员，由父母代管'
  } else {
    if (isAdmin) {
      chip = '管理员'
      meta = ROLE_LABEL[role] || role
    } else {
      meta = ROLE_LABEL[role] || role
    }
  }
  return {
    member_id: m.member_id,
    avatar: (display || '?').slice(0, 1),
    name: display,
    rawName: m.name || '',
    chip,
    meta,
    isVirtual,
    role,
    is_admin: isAdmin,
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
    memberSheet: { open: false },
    customName: '',
    customRelation: '' // 自定义名称输入（顶层字段，与其他输入框一致；嵌套路径 model:value 真机回写不可靠）
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
          isAdmin: !!app.globalData.isAdmin,
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
    // 当前名称不在预设里（自定义名称）时，并入 chips 列表一并展示。
    const relations = m.relation && base.indexOf(m.relation) < 0 ? base.concat([m.relation]) : base
    this.setData({
      customName: m.isVirtual ? (m.rawName || m.name || '') : '',
      customRelation: '',
      memberSheet: {
        open: true,
        member_id: m.member_id,
        name: m.name,
        rawName: m.rawName,
        chip: m.chip,
        isVirtual: m.isVirtual,
        isSelf: m.isSelf,
        relations,
        relation: m.relation,
        // 管理员是单独权限设置；role 只表示家长/祖辈/孩子。
        role: m.role,
        isAdmin: !!m.is_admin,
        roleOptions: ROLE_OPTIONS_REAL,
        originRelation: m.relation,
        originRole: m.role,
        originIsAdmin: !!m.is_admin,
        originName: m.rawName || m.name || ''
      }
    })
  },

  closeMember() { this.setData({ 'memberSheet.open': false, customName: '', customRelation: '' }) },

  pickRelation(e) {
    this.setData({
      'memberSheet.relation': e.currentTarget.dataset.rel,
      customRelation: this.data.memberSheet.isVirtual ? this.data.customRelation : ''
    })
  },

  pickRole(e) {
    const role = e.currentTarget.dataset.role
    if (!role || this.data.memberSheet.isVirtual) return
    this.setData({ 'memberSheet.role': role })
  },

  toggleAdmin() {
    if (this.data.memberSheet.isVirtual) return
    this.setData({ 'memberSheet.isAdmin': !this.data.memberSheet.isAdmin })
  },

  saveMember() {
    const s = this.data.memberSheet
    const fid = app.globalData.familyId
    // 真实成员：自定义名称优先于预设；孩子：名称是独立可编辑字段，身份为儿子/女儿。
    const relation = s.isVirtual ? s.relation : ((this.data.customRelation || '').trim() || s.relation)
    const name = (this.data.customName || '').trim()
    const payload = {}
    if (relation !== s.originRelation) payload.relation_label = relation
    if (s.isVirtual) {
      if (!name) return wx.showToast({ title: '请输入孩子名称', icon: 'none' })
      if (name !== s.originName) payload.name = name
    }
    // role 是系统预设枚举；管理员是单独设置，不出现在角色选项里。
    if (!s.isVirtual) {
      if (s.role !== s.originRole) payload.role = s.role
      if (s.isAdmin !== s.originIsAdmin) payload.is_admin = s.isAdmin
    }
    if (Object.keys(payload).length === 0) {
      this.closeMember()
      return
    }
    app.request(`/v1/families/${fid}/members/${s.member_id}`, { method: 'PATCH', data: payload })
      .then(() => {
        wx.showToast({ title: '已保存', icon: 'success' })
        this.closeMember()
        // 改的是自己的角色/管理员权限时刷新会话里的 memberships，避免权限状态陈旧。
        if (s.isSelf && (payload.role || Object.prototype.hasOwnProperty.call(payload, 'is_admin'))) return app.refreshMe()
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

  openEntryPermission() {
    wx.showToast({ title: '只有管理员才能录入', icon: 'none' })
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
