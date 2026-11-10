// 日程详情：展示单个实例，支持改分工（重指派责任人）/ 编辑 / 取消本次 / 删除全部，对齐 prototype scr-event。
const app = getApp()
const { hhmm } = require('../../utils/date')

const WEEK_CN = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

function dateLabel(dateStr) {
  const d = new Date(dateStr + 'T00:00:00')
  return (d.getMonth() + 1) + '月' + d.getDate() + '日 ' + WEEK_CN[d.getDay()]
}

function decodeQueryText(v, fallback = '') {
  let s = v || fallback
  for (let i = 0; i < 2; i++) {
    if (!/%[0-9a-fA-F]{2}/.test(s)) break
    try {
      const decoded = decodeURIComponent(s)
      if (decoded === s) break
      s = decoded
    } catch (e) {
      break
    }
  }
  return s
}

Page({
  data: {
    canManage: false,
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
    hasInstance: false,
    owners: [],
    sheetOpen: false
  },

  onLoad(options) {
    this._inst = options
    const isRecurring = !!options.rrule
    this.setData({
      canManage: !!app.globalData.isAdmin,
      title: decodeQueryText(options.title),
      time: dateLabel(options.occur_date) + ' ' + hhmm(options.start_at) + (options.end_at ? '–' + hhmm(options.end_at) : ''),
      repeatLabel: decodeQueryText(options.repeat_label, isRecurring ? '重复' : '单次'),
      hasInstance: !!options.instance_id,
      deleteButtonText: isRecurring ? '删除此后周期日程' : '删除此日程',
      deleteConfirmTitle: isRecurring ? '删除此后周期日程' : '删除此日程',
      deleteConfirmContent: isRecurring
        ? '会删除这条周期日程从本次开始的当前及未来日历记录，历史记录会保留。如果只是不去这一次，请选择「取消本次」。确认删除此后周期日程？'
        : '会删除这条单次安排，确认删除？历史记录不受影响。',
      deleteNote: isRecurring
        ? '「取消本次」只隐藏当前这一次；「删除此后周期日程」会保留历史记录，只删除当前及未来日程。'
        : '单次日程可直接删除；历史记录不受影响。',
      location: decodeQueryText(options.location),
      note: decodeQueryText(options.note)
    })
    this.loadMembers()
  },

  loadMembers() {
    app.ensureLogin()
      .then(() => {
        const fid = app.globalData.familyId
        if (!fid) return []
        return app.request(`/v1/families/${fid}/members`)
      })
      .then((members) => {
        const owners = []
        let childName = ''
        let ownerName = ''
        ;(members || []).forEach((m) => {
          const label = m.name || m.relation_label || '成员' + m.member_id
          if (m.type === 'virtual') {
            if (String(m.member_id) === String(this._inst.child_member_id)) childName = label
          } else {
            owners.push({ member_id: m.member_id, name: label, picked: String(m.member_id) === String(this._inst.owner_member_id) })
            if (String(m.member_id) === String(this._inst.owner_member_id)) ownerName = label
          }
        })
        this.setData({ owners, childName: childName || '—', ownerName: ownerName || '—' })
      })
      .catch((e) => {
        if (e.code !== 4001) wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  goBack() { wx.navigateBack() },

  openReassign() { this.setData({ sheetOpen: true }) },
  closeSheet() { this.setData({ sheetOpen: false }) },
  noop() {},

  reassign(e) {
    const ownerId = e.currentTarget.dataset.id
    const iid = this._inst.instance_id
    if (!iid || !ownerId) return
    // 已是当前责任人：无需请求（后端幂等，但避免无意义往返）。
    if (String(ownerId) === String(this._inst.owner_member_id)) {
      this.closeSheet()
      return
    }
    app.request(`/v1/schedules/instances/${iid}`, { method: 'PATCH', data: { owner_member_id: ownerId } })
      .then(() => {
        this.closeSheet()
        this._inst.owner_member_id = ownerId
        this.loadMembers()
        wx.showToast({ title: '已更新分工', icon: 'success' })
      })
      .catch((err) => wx.showToast({ title: err.message || '更新失败', icon: 'none' }))
  },

  edit() {
    const sid = this._inst.schedule_id
    if (!sid) return wx.showToast({ title: '缺少日程信息', icon: 'none' })
    wx.navigateTo({ url: '/pages/event/index?edit=1&schedule_id=' + sid })
  },

  cancelInstance() {
    const iid = this._inst.instance_id
    if (!iid) return wx.showToast({ title: '请从日历具体日期取消本次', icon: 'none' })
    wx.showModal({
      title: '取消本次',
      content: '仅取消这一次，不影响之后重复的日程。',
      confirmText: '取消本次',
      confirmColor: '#BF4A3C',
      success: (r) => {
        if (!r.confirm) return
        app.request(`/v1/schedules/instances/${iid}`, { method: 'PATCH', data: { status: 'cancelled' } })
          .then(() => {
            wx.showToast({ title: '已取消', icon: 'success' })
            setTimeout(() => wx.navigateBack(), 500)
          })
          .catch((err) => wx.showToast({ title: err.message || '取消失败', icon: 'none' }))
      }
    })
  },

  deleteSchedule() {
    const sid = this._inst.schedule_id
    if (!sid) return wx.showToast({ title: '缺少日程信息', icon: 'none' })
    wx.showModal({
      title: this.data.deleteConfirmTitle,
      content: this.data.deleteConfirmContent,
      confirmText: '删除',
      confirmColor: '#BF4A3C',
      success: (r) => {
        if (!r.confirm) return
        const from = this._inst.occur_date ? '?from=' + this._inst.occur_date : ''
        app.request(`/v1/schedules/${sid}${from}`, { method: 'DELETE' })
          .then(() => {
            wx.showToast({ title: '已删除', icon: 'success' })
            setTimeout(() => wx.navigateBack(), 500)
          })
          .catch((err) => wx.showToast({ title: err.message || '删除失败', icon: 'none' }))
      }
    })
  }
})
