// 安排一件事 / 编辑日程：手动填写 / 拍照识别课表 / 从微信导入，对齐 prototype scr-create。
// 冲突时支持强制保存（对齐后端 2005 冲突返回）；编辑模式走 PATCH /v1/schedules/:sid。
const app = getApp()
const { todayStr, weekdayCode } = require('../../utils/date')

const TYPES = [
  { value: 'course', label: '课程' },
  { value: 'pickup', label: '接送' },
  { value: 'activity', label: '活动' },
  { value: 'reminder', label: '提醒' }
]
const REPEATS = [
  { value: '', label: '单次' },
  { value: 'daily', label: '每天' },
  { value: 'weekly', label: '每周' }
]

Page({
  data: {
    heading: '安排一件事',
    mode: 'manual',
    types: TYPES,
    repeats: REPEATS,
    children: [],
    owners: [],
    typeIndex: 0,
    repeatIndex: 0,
    childIndex: 0,
    ownerIndex: 0,
    title: '',
    startDate: '',
    startTime: '18:00',
    location: '',
    note: '',
    submitting: false
  },

  onLoad(options) {
    this._edit = options.edit === '1' ? options.schedule_id : ''
    this._endTime = ''
    this.setData({ startDate: todayStr() })
    if (this._edit) {
      this.setData({ heading: '编辑日程' })
      wx.setNavigationBarTitle({ title: '编辑日程' })
    }
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
        const children = []
        const owners = []
        const myId = app.globalData.user && app.globalData.user.user_id
        let ownerIndex = 0
        ;(members || []).forEach((m) => {
          if (m.type === 'virtual') {
            children.push({ member_id: m.member_id, name: m.name || '孩子' + m.member_id })
          } else {
            const idx = owners.length
            if (m.user_id === myId) ownerIndex = idx
            owners.push({ member_id: m.member_id, user_id: m.user_id, name: m.name || m.relation_label || '成员' + m.member_id })
          }
        })
        this.setData({ children, owners, ownerIndex })
        if (this._edit) this.prefill(this._edit)
      })
      .catch((e) => {
        if (e.code !== 4001) wx.showToast({ title: e.message || '加载成员失败', icon: 'none' })
      })
  },

  // 编辑模式：拉取日程回填表单（repeatIndex 由 rrule 反推）。
  prefill(scheduleId) {
    app.request('/v1/schedules')
      .then((schedules) => {
        const s = (schedules || []).find((x) => String(x.schedule_id) === String(scheduleId))
        if (!s) { wx.showToast({ title: '未找到该日程', icon: 'none' }); return }
        let repeatIndex = 0
        if (s.rrule && s.rrule.indexOf('FREQ=WEEKLY') >= 0) repeatIndex = 2
        else if (s.rrule && s.rrule.indexOf('FREQ=DAILY') >= 0) repeatIndex = 1
        this._endTime = s.end_time || ''
        this.setData({
          title: s.title || '',
          typeIndex: Math.max(0, TYPES.findIndex((t) => t.value === s.type)),
          startDate: s.start_date || todayStr(),
          startTime: s.start_time ? s.start_time.slice(0, 5) : '18:00',
          repeatIndex,
          childIndex: Math.max(0, this.data.children.findIndex((c) => c.member_id === s.child_member_id)),
          ownerIndex: Math.max(0, this.data.owners.findIndex((o) => o.member_id === s.owner_member_id)),
          location: s.location || '',
          note: s.note || ''
        })
      })
      .catch((e) => {
        if (e.code !== 4001) wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  setMode(e) {
    this.setData({ mode: e.currentTarget.dataset.mode })
  },

  pickChatFile() {
    wx.showToast({ title: '从微信导入开发中', icon: 'none' })
  },

  onTitle(e) { this.setData({ title: e.detail.value }) },
  onLocation(e) { this.setData({ location: e.detail.value }) },
  onNote(e) { this.setData({ note: e.detail.value }) },
  onType(e) { this.setData({ typeIndex: Number(e.detail.value) }) },
  onRepeat(e) { this.setData({ repeatIndex: Number(e.detail.value) }) },
  onChild(e) { this.setData({ childIndex: Number(e.detail.value) }) },
  onOwner(e) { this.setData({ ownerIndex: Number(e.detail.value) }) },
  onDate(e) { this.setData({ startDate: e.detail.value }) },
  onStartTime(e) { this.setData({ startTime: e.detail.value }) },

  submit() {
    if (this.data.submitting) return
    const child = this.data.children[this.data.childIndex]
    const owner = this.data.owners[this.data.ownerIndex]
    if (!this.data.title.trim()) return wx.showToast({ title: '请输入事项', icon: 'none' })
    if (!child) return wx.showToast({ title: '请先到「家庭」页添加孩子', icon: 'none' })
    if (!owner) return wx.showToast({ title: '请选择责任人', icon: 'none' })

    const repeat = this.data.repeats[this.data.repeatIndex]
    let rrule = ''
    if (repeat.value === 'weekly') rrule = 'FREQ=WEEKLY;BYDAY=' + weekdayCode(this.data.startDate)
    else if (repeat.value === 'daily') rrule = 'FREQ=DAILY'

    const payload = {
      title: this.data.title.trim(),
      type: this.data.types[this.data.typeIndex].value,
      child_member_id: child.member_id,
      start_date: this.data.startDate,
      rrule,
      start_time: this.data.startTime,
      end_time: this._endTime || '',
      location: this.data.location,
      note: this.data.note,
      owner_member_id: owner.member_id
    }
    this.save(this._edit, payload, false)
  },

  // 创建走 POST，编辑走 PATCH；冲突(2005)可强制保存。
  save(scheduleId, payload, force) {
    this.setData({ submitting: true })
    const url = scheduleId ? `/v1/schedules/${scheduleId}` : '/v1/schedules'
    const method = scheduleId ? 'PATCH' : 'POST'
    app.request(url, { method, data: Object.assign({}, payload, { force }) })
      .then(() => {
        wx.showToast({ title: '已保存', icon: 'success' })
        // 编辑来自详情页（日历→详情→编辑），保存后直接回日历；新建回上一页。
        const delta = this._edit ? 2 : 1
        setTimeout(() => wx.navigateBack({ delta }), 600)
      })
      .catch((e) => {
        this.setData({ submitting: false })
        if (e.code === 2005) {
          const conflicts = (e.data && e.data.conflicts) || []
          const tips = conflicts.slice(0, 3).map((c) => c.title + ' ' + c.occur_date).join('\n')
          wx.showModal({
            title: '存在时间冲突',
            content: tips + (conflicts.length > 3 ? '\n…' : '') + '\n\n是否强制保存？',
            confirmText: '强制保存',
            success: (r) => { if (r.confirm) this.save(scheduleId, payload, true) }
          })
        } else if (e.code === 4001) {
          app.logout(); wx.navigateBack()
        } else {
          wx.showToast({ title: e.message || '保存失败', icon: 'none' })
        }
      })
  },

  goBack() { wx.navigateBack() }
})
