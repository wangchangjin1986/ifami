// 家庭页：登录入口 + 建家庭 + 成员列表（avatar + 角色 chip），对齐 prototype scr-family。
// 管理员可点成员卡片进入管理面板：名称/身份配置 + 角色选择 + 管理员设置 + 移除（对齐 prototype scr-member）。
const app = getApp()
const { drawCanvas } = require('../../utils/qrcode')

const ROLE_LABEL = { admin: '管理员', parent: '家长', grandparent: '照护人', child: '孩子' }
const ROLE_OPTIONS_REAL = [
  { value: 'parent', label: '家长' },
  { value: 'grandparent', label: '照护人' }
]
// 真实成员的名称预设；孩子身份单独用儿子/女儿。
const RELATIONS_REAL = ['爸爸', '妈妈', '爷爷', '奶奶', '姥姥', '姥爷', '叔叔', '阿姨']
const RELATIONS_CHILD = ['儿子', '女儿']

const JOIN_CODE_PENDING_MS = 30 * 60 * 1000

function familyNameKey(fid) { return 'familyName:' + fid }
function familyMembersKey(fid) { return 'familyMembers:' + fid }
function joinCodePendingKey(fid) { return 'joinCodePending:' + fid }
function operationKey() { return 'join-code-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 12) }

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
    }
    meta = ROLE_LABEL[role] || role
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
    isSelf: !isVirtual && selfUserID != null && String(m.user_id) === String(selfUserID)
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
    inviteSheet: false,
    joinCodeStatus: 'idle',
    joinCode: '',
    joinCodeDisplay: '',
    joinCodeGeneration: '',
    joinCodeQR: '',
    joinCodeExpiresText: '',
    joinCodeError: '',
    sheetOpen: false,
    memberSheet: { open: false },
    customName: '',
    customRelation: '' // 自定义名称输入（顶层字段，与其他输入框一致；嵌套路径 model:value 真机回写不可靠）
  },

  onLoad() {
    if (wx.hideShareMenu) wx.hideShareMenu({ menus: ['shareAppMessage', 'shareTimeline'] })
    this._joinCodeSerial = 0
  },

  onShow() {
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 3 })
    }
    if (app.routeGrandparentHome('pages/family/index')) return
    this.refresh()
    if (this.data.inviteSheet && this._joinCodeNeedsRead) {
      this._joinCodeNeedsRead = false
      this.loadJoinCode({ generateIfMissing: true })
    }
  },

  onHide() {
    if (this.data.inviteSheet) this._joinCodeNeedsRead = true
  },

  refresh() {
    // 家庭页是登录入口：无本地会话时必须立即落到登录态，不能在 onShow 里自动 wx.login + 请求后端，
    // 否则网络/微信登录链路异常时首屏会一直停在「加载中…」。
    if (!app.globalData.token || !app.globalData.user) {
      this.setData({ loading: false, logged: false, hasFamily: false, members: [], familyName: '' })
      return Promise.resolve()
    }

    // 首屏性能：已有本地会话时，先用本地 familyId / memberships / 最近一次成员缓存同步渲染，
    // 不把首屏阻塞在 /v1/me、/v1/families、/members 串行网络请求上；远程数据随后静默刷新覆盖。
    this.renderLocalFirst()

    return app.ensureLogin()
      .then(() => {
        if (app.routeGrandparentHome('pages/family/index')) return Promise.reject({ routed: true })
      })
      .then(() => app.request('/v1/families'))
      .then((families) => {
        const list = families || []
        if (list.length === 0) {
          this.setData({ loading: false, logged: true, hasFamily: false, members: [], familyName: '' })
          return
        }
        const cur = list.find((f) => f.family_id === app.globalData.familyId) || list[0]
        app.setFamily(cur.family_id)
        wx.setStorageSync(familyNameKey(cur.family_id), cur.name || '')
        this.setData({
          loading: false,
          logged: true,
          hasFamily: true,
          isAdmin: !!app.globalData.isAdmin,
          familyName: cur.name
        })
        return app.request(`/v1/families/${cur.family_id}/members`).then((members) => {
          const selfID = app.globalData.user ? app.globalData.user.user_id : null
          wx.setStorageSync(familyMembersKey(cur.family_id), members || [])
          this.setData({ members: (members || []).map((m) => memberView(m, selfID)), loading: false })
        })
      })
      .catch((e) => {
        if (e && e.routed) return
        this.setData({ loading: false })
        if (e.code === 4001) {
          app.logout()
          this.setData({ logged: false, hasFamily: false, members: [] })
        }
        wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  renderLocalFirst() {
    const memberships = app.globalData.memberships || []
    const fid = app.globalData.familyId || (memberships[0] && memberships[0].family_id)
    if (!fid || memberships.length === 0) {
      this.setData({ loading: false, logged: true, hasFamily: false, members: [], familyName: '' })
      return
    }
    if (!app.globalData.familyId) app.setFamily(fid)
    const cachedMembers = wx.getStorageSync(familyMembersKey(fid)) || []
    const selfID = app.globalData.user ? app.globalData.user.user_id : null
    this.setData({
      loading: false,
      logged: true,
      hasFamily: true,
      isAdmin: !!app.globalData.isAdmin,
      familyName: wx.getStorageSync(familyNameKey(fid)) || this.data.familyName || '家庭',
      members: cachedMembers.map((m) => memberView(m, selfID))
    })
  },

  doLogin() {
    app.login()
      .then(() => this.refresh())
      .catch((e) => wx.showToast({ title: e.message || '登录失败', icon: 'none' }))
  },

  startCreate() {
    app.ensureLogin()
      .then(() => this.refresh())
      .catch((e) => wx.showToast({ title: e.message || '登录失败', icon: 'none' }))
  },

  startJoin() {
    app.ensureLogin()
      .then(() => {
        if ((app.globalData.memberships || []).length > 0) {
          wx.showToast({ title: '你已加入家庭，当前版本不支持加入另一个家庭', icon: 'none' })
          return
        }
        wx.navigateTo({ url: '/pages/invite/index' })
      })
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
    if (!this.data.isAdmin) return wx.showToast({ title: '只有管理员才能添加孩子', icon: 'none' })
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
    if (!this.data.isAdmin) return wx.showToast({ title: '只有管理员可以管理家庭加入码', icon: 'none' })
    this.setData({ inviteSheet: true })
    this.loadJoinCode({ generateIfMissing: true })
  },

  closeInvite() {
    this.stopJoinCodeTimer()
    this.setData({ inviteSheet: false })
  },

  clearJoinCode(status, error = '') {
    this.stopJoinCodeTimer()
    this.setData({
      joinCodeStatus: status,
      joinCode: '',
      joinCodeDisplay: '',
      joinCodeGeneration: '',
      joinCodeQR: '',
      joinCodeExpiresText: '',
      joinCodeError: error
    })
  },

  loadJoinCode({ generateIfMissing = false } = {}) {
    const fid = app.globalData.familyId
    if (!fid || !this.data.isAdmin) return Promise.resolve(false)
    const serial = ++this._joinCodeSerial
    this.clearJoinCode('loading')
    return app.request(`/v1/families/${fid}/join-code`, { familyId: fid })
      .then((view) => {
        if (serial !== this._joinCodeSerial || !this.data.inviteSheet) return false
        if (this.showJoinCode(view)) {
          wx.removeStorageSync(joinCodePendingKey(fid))
          return true
        }
        wx.removeStorageSync(joinCodePendingKey(fid))
        this.clearJoinCode('empty')
        return generateIfMissing ? this.generateJoinCode({ allowPendingReuse: false }) : false
      })
      .catch((e) => {
        if (serial === this._joinCodeSerial) this.clearJoinCode('error', e.message || '暂时无法读取家庭加入码')
        return false
      })
  },

  showJoinCode(view) {
    if (!view || !view.has_code || !view.join_code || !view.generation_id || !view.qr_content) return false
    const expires = Date.parse(view.expires_at)
    if (!Number.isFinite(expires) || expires <= Date.now()) {
      this.clearJoinCode('expired', '家庭加入码已过期，请重新生成')
      return false
    }
    const code = String(view.join_code)
    this.setData({
      joinCodeStatus: 'active',
      joinCode: code,
      joinCodeDisplay: code.slice(0, 3) + ' ' + code.slice(3),
      joinCodeGeneration: view.generation_id,
      joinCodeQR: view.qr_content,
      joinCodeExpiresText: new Date(expires).toLocaleString(),
      joinCodeError: ''
    }, () => {
      const render = () => {
        if (this.data.joinCodeStatus === 'active' && this.data.joinCodeQR === view.qr_content) drawCanvas('join-code-qr', view.qr_content, 420, 4)
      }
      if (wx.nextTick) wx.nextTick(render); else setTimeout(render, 0)
    })
    this.stopJoinCodeTimer()
    this._joinCodeTimer = setTimeout(() => {
      if (this.data.joinCodeGeneration === view.generation_id) this.clearJoinCode('expired', '家庭加入码已过期，请重新生成')
    }, Math.max(0, expires - Date.now()))
    return true
  },

  stopJoinCodeTimer() {
    if (this._joinCodeTimer) clearTimeout(this._joinCodeTimer)
    this._joinCodeTimer = null
  },

  generateJoinCode({ allowPendingReuse = true } = {}) {
    const fid = app.globalData.familyId
    if (!fid || !this.data.isAdmin) return Promise.resolve(false)
    const saved = wx.getStorageSync(joinCodePendingKey(fid)) || null
    if (allowPendingReuse && saved && Date.now() - Number(saved.at || 0) >= JOIN_CODE_PENDING_MS) {
      // 服务端回执只保留30分钟；过窗或经历未知后台间隔必须先读当前代，不能重放旧操作。
      wx.removeStorageSync(joinCodePendingKey(fid))
      return this.loadJoinCode({ generateIfMissing: true })
    }
    const pending = allowPendingReuse && saved && saved.key ? saved : { key: operationKey(), at: Date.now() }
    wx.setStorageSync(joinCodePendingKey(fid), pending)
    const serial = ++this._joinCodeSerial
    this.clearJoinCode('loading')
    return app.request(`/v1/families/${fid}/join-code`, {
      method: 'POST',
      familyId: fid,
      header: { 'Idempotency-Key': pending.key }
    }).then((view) => {
      if (serial !== this._joinCodeSerial || !this.data.inviteSheet) return false
      wx.removeStorageSync(joinCodePendingKey(fid))
      if (!this.showJoinCode(view)) throw new Error('生成结果不完整，请重新读取')
      return true
    }).catch((e) => {
      if (serial === this._joinCodeSerial) this.clearJoinCode('error', (e.message || '生成失败') + '；重试会沿用同一次操作')
      return false
    })
  },

  regenerateJoinCode() {
    const fid = app.globalData.familyId
    wx.removeStorageSync(joinCodePendingKey(fid))
    return this.generateJoinCode({ allowPendingReuse: false })
  },

  retryJoinCode() {
    const fid = app.globalData.familyId
    const saved = wx.getStorageSync(joinCodePendingKey(fid)) || null
    if (!saved || Date.now() - Number(saved.at || 0) >= JOIN_CODE_PENDING_MS || this._joinCodeNeedsRead) {
      this._joinCodeNeedsRead = false
      return this.loadJoinCode({ generateIfMissing: true })
    }
    return this.generateJoinCode({ allowPendingReuse: true })
  },

  revokeJoinCode() {
    const fid = app.globalData.familyId
    const generation = this.data.joinCodeGeneration
    if (!fid || !generation || !this.data.isAdmin) return Promise.resolve(false)
    this.clearJoinCode('loading')
    return app.request(`/v1/families/${fid}/join-code`, {
      method: 'DELETE', familyId: fid, data: { generation_id: generation }
    }).then((view) => {
      if (!this.showJoinCode(view)) this.clearJoinCode('empty')
      return true
    }).catch(() => this.loadJoinCode({ generateIfMissing: false }))
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
        // 管理员是单独权限设置；role 只表示家长/照护人/孩子。
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
    if (!this.data.isAdmin) return wx.showToast({ title: '只有管理员可以管理成员', icon: 'none' })
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
    if (!this.data.isAdmin) return wx.showToast({ title: '只有管理员可以管理成员', icon: 'none' })
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

  goManage() {
    if (!this.data.isAdmin) return wx.showToast({ title: '只有管理员可以管理全部安排', icon: 'none' })
    wx.navigateTo({ url: '/pages/manage/index' })
  },
  openFab() {
    if (!this.data.isAdmin) return wx.showToast({ title: '只有管理员才能录入', icon: 'none' })
    this.setData({ sheetOpen: true })
  },
  closeFab() { this.setData({ sheetOpen: false }) },
  noop() {},
  goEvent() {
    if (!this.data.isAdmin) return
    this.closeFab()
    wx.navigateTo({ url: '/pages/event/index' })
  },
  goTask() {
    if (!this.data.isAdmin) return
    this.closeFab()
    wx.navigateTo({ url: '/pages/task/index' })
  },
  goImport() {
    if (!this.data.isAdmin) return
    this.closeFab()
    wx.navigateTo({ url: '/pages/import/index' })
  },
  goReport() { wx.navigateTo({ url: '/pages/report/index' }) },
  goTimeline() { wx.navigateTo({ url: '/pages/timeline/index' }) },
  goSettings() { wx.navigateTo({ url: '/pages/settings/index' }) },

  onPullDownRefresh() {
    this.refresh().then(() => wx.stopPullDownRefresh())
  }
})
