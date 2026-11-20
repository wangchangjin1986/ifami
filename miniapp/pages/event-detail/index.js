// 日程详情只以服务端授权后的实例数据为准；URL 仅携带稳定 instance_id/family_id。
const app = getApp()
const { hhmm } = require('../../utils/date')

const WEEK_CN = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

function dateLabel(dateStr) {
  if (!dateStr) return ''
  const d = new Date(dateStr + 'T00:00:00')
  return (d.getMonth() + 1) + '月' + d.getDate() + '日 ' + WEEK_CN[d.getDay()]
}

Page({
  data: {
    loading: true,
    loaded: false,
    errorText: '',
    canManage: false,
    isGrandparent: false,
    canAct: false,
    title: '',
    time: '',
    childName: '',
    ownerName: '',
    location: '',
    note: '',
    repeatLabel: '单次',
    deleteButtonText: '删除此日程',
    deleteConfirmTitle: '删除此日程',
    deleteConfirmContent: '会删除这条单次安排，确认删除？历史记录不受影响。',
    deleteNote: '删除此日程会移除这条单次安排；历史记录不受影响。',
    owners: [],
    sheetOpen: false,
    feedbackOpen: false,
    feedbackReasons: [
      { value: '今天去不了', label: '今天去不了', desc: '我这次没法接送，请家长重新安排' },
      { value: '时间不对', label: '时间不对', desc: '看到的时间和实际不一致' },
      { value: '地点/携带物不清楚', label: '地点/携带物不清楚', desc: '需要家长补充说明' },
      { value: '其他，电话说', label: '其他，电话说', desc: '保留人工兜底' }
    ]
  },

  onLoad(options = {}) {
    this._instanceID = options.instance_id || ''
    this._scheduleID = options.schedule_id || ''
    this._familyId = options.family_id || app.globalData.familyId
    if (this._familyId) app.setFamily(this._familyId)
    app.reportNotifyReceipt(options.job_id)
    this.loadAuthorizedDetail()
  },

  loadAuthorizedDetail() {
    this.setData({ loading: true, loaded: false, errorText: '', title: '', location: '', note: '', owners: [] })
    return app.ensureLogin()
      .then(() => {
        this._familyId = this._familyId || app.globalData.familyId
        if (this._instanceID) {
          return app.request(`/v1/schedules/instances/${this._instanceID}`, { familyId: this._familyId })
            .then((instance) => this.renderInstance(instance))
        }
        return this.resolveAuthorizedTemplateLink()
      })
      .catch((error) => {
        const moved = error && (error.code === 2004 || error.statusCode === 403)
        this.setData({
          loading: false,
          loaded: false,
          title: '', time: '', childName: '', ownerName: '', location: '', note: '', owners: [],
          errorText: moved ? '分工已调整，你已无权查看这条安排' : (error.message || '无法加载这条安排')
        })
      })
  },

  resolveAuthorizedTemplateLink() {
    if (!this._scheduleID) throw new Error('缺少日程实例，请从日历选择具体日期')
    return app.request('/v1/schedules', { familyId: this._familyId }).then((rows) => {
      const found = (rows || []).some((row) => String(row.schedule_id) === String(this._scheduleID))
      if (!found) throw new Error('这条日程不存在或无权查看')
      if (app.globalData.isAdmin) {
        wx.redirectTo({ url: '/pages/event/index?edit=1&schedule_id=' + this._scheduleID })
        return
      }
      this.setData({ loading: false, loaded: false, errorText: '此入口没有具体日期，请从日历选择一次安排查看' })
    })
  },

  renderInstance(instance) {
    if (!instance || !instance.instance_id || (this._familyId && String(instance.family_id) !== String(this._familyId))) {
      throw new Error('日程详情校验失败')
    }
    this._inst = instance
    this._familyId = instance.family_id
    const isRecurring = !!instance.rrule
    const canManage = !!app.globalData.isAdmin
    const isGrandparent = app.globalData.role === 'grandparent' && !canManage
    const active = instance.status === 'active'
    this.setData({
      loading: false,
      loaded: true,
      errorText: '',
      canManage,
      isGrandparent,
      canAct: active,
      title: instance.title || '日程详情',
      time: dateLabel(instance.occur_date) + ' ' + hhmm(instance.start_at) + (instance.end_at ? '–' + hhmm(instance.end_at) : ''),
      repeatLabel: isRecurring ? '重复' : '单次',
      location: instance.location || '',
      note: instance.note || '',
      deleteButtonText: isRecurring ? '删除此后周期日程' : '删除此日程',
      deleteConfirmTitle: isRecurring ? '删除此后周期日程' : '删除此日程',
      deleteConfirmContent: isRecurring
        ? '会删除这条周期日程从本次开始的当前及未来日历记录，历史记录会保留。确认删除此后周期日程？'
        : '会删除这条单次安排，确认删除？历史记录不受影响。',
      deleteNote: isRecurring
        ? '「取消本次」只隐藏当前这一次；「删除此后周期日程」会保留历史记录。'
        : '单次日程可直接删除；历史记录不受影响。'
    })
    return this.loadMembers(instance)
  },

  loadMembers(instance = this._inst) {
    if (!instance) return Promise.resolve()
    return app.request(`/v1/families/${this._familyId}/members`, { familyId: this._familyId })
      .then((members) => {
        const owners = []
        let childName = ''
        let ownerName = ''
        ;(members || []).forEach((m) => {
          const label = m.name || m.relation_label || '成员' + m.member_id
          if (m.type === 'virtual') {
            if (String(m.member_id) === String(instance.child_member_id)) childName = label
          } else {
            owners.push({ member_id: m.member_id, user_id: m.user_id, name: label, picked: String(m.member_id) === String(instance.owner_member_id) })
            if (String(m.member_id) === String(instance.owner_member_id)) ownerName = label
          }
        })
        this.setData({ owners, childName: childName || '—', ownerName: ownerName || '—' })
      })
      .catch(() => this.setData({ childName: '—', ownerName: '—', owners: [] }))
  },

  goBack() { wx.navigateBack() },
  openReassign() { if (this.data.canManage && this.data.canAct) this.setData({ sheetOpen: true }) },
  closeSheet() { this.setData({ sheetOpen: false }) },
  openFeedback() { if (this.data.isGrandparent && this.data.canAct) this.setData({ feedbackOpen: true }) },
  closeFeedback() { this.setData({ feedbackOpen: false }) },
  noop() {},

  completeInstance() {
    if (!this.data.isGrandparent || !this.data.canAct) return
    return app.request(`/v1/schedules/instances/${this._inst.instance_id}/complete`, { method: 'POST', familyId: this._familyId })
      .then(() => { this.setData({ canAct: false }); wx.showToast({ title: '已完成', icon: 'success' }) })
      .catch((error) => wx.showToast({ title: error.message || '提交失败', icon: 'none' }))
  },

  submitFeedback(e) {
    if (!this.data.isGrandparent || !this.data.canAct) return
    const reason = e.currentTarget.dataset.reason
    if (!reason) return
    return app.request(`/v1/schedules/instances/${this._inst.instance_id}/feedback`, { method: 'POST', familyId: this._familyId, data: { reason } })
      .then(() => { this.setData({ feedbackOpen: false }); wx.showToast({ title: '已通知家长处理', icon: 'none' }) })
      .catch((error) => wx.showToast({ title: error.message || '提交失败', icon: 'none' }))
  },

  reassign(e) {
    if (!this.data.canManage || !this.data.canAct) return
    const ownerID = e.currentTarget.dataset.id
    if (!ownerID || String(ownerID) === String(this._inst.owner_member_id)) return this.closeSheet()
    const owner = this.data.owners.find((item) => String(item.member_id) === String(ownerID))
    const currentUserID = app.globalData.user && app.globalData.user.user_id
    const subscribe = owner && String(owner.user_id) === String(currentUserID) ? app.requestNotifySubscribe() : Promise.resolve(false)
    return subscribe
      .then(() => app.request(`/v1/schedules/instances/${this._inst.instance_id}`, { method: 'PATCH', familyId: this._familyId, data: { owner_member_id: ownerID } }))
      .then(() => { this.closeSheet(); return this.loadAuthorizedDetail() })
      .catch((error) => wx.showToast({ title: error.message || '更新失败', icon: 'none' }))
  },

  edit() {
    if (!this.data.canManage || !this._inst) return
    wx.navigateTo({ url: '/pages/event/index?edit=1&schedule_id=' + this._inst.schedule_id })
  },

  cancelInstance() {
    if (!this.data.canManage || !this.data.canAct || !this._inst) return
    wx.showModal({
      title: '取消本次', content: '仅取消这一次，不影响之后重复的日程。', confirmText: '取消本次', confirmColor: '#BF4A3C',
      success: (result) => {
        if (!result.confirm) return
        app.request(`/v1/schedules/instances/${this._inst.instance_id}`, { method: 'PATCH', familyId: this._familyId, data: { status: 'cancelled' } })
          .then(() => { wx.showToast({ title: '已取消', icon: 'success' }); setTimeout(() => wx.navigateBack(), 500) })
          .catch((error) => wx.showToast({ title: error.message || '取消失败', icon: 'none' }))
      }
    })
  },

  deleteSchedule() {
    if (!this.data.canManage || !this._inst) return
    wx.showModal({
      title: this.data.deleteConfirmTitle, content: this.data.deleteConfirmContent, confirmText: '删除', confirmColor: '#BF4A3C',
      success: (result) => {
        if (!result.confirm) return
        const from = this._inst.occur_date ? '?from=' + this._inst.occur_date : ''
        app.request(`/v1/schedules/${this._inst.schedule_id}${from}`, { method: 'DELETE', familyId: this._familyId })
          .then(() => { wx.showToast({ title: '已删除', icon: 'success' }); setTimeout(() => wx.navigateBack(), 500) })
          .catch((error) => wx.showToast({ title: error.message || '删除失败', icon: 'none' }))
      }
    })
  }
})
