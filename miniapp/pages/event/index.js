// 安排一件事 / 编辑日程：手动填写，对齐 prototype scr-create（拍照导入走 ＋ 面板「拍照导入日程」）。
// 冲突时支持强制保存（对齐后端 2005 冲突返回）；编辑模式走 PATCH /v1/schedules/:sid。
const app = getApp()
const { todayStr, weekdayCode } = require('../../utils/date')

const DAILY_RRULE = 'FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR,SA,SU'

const TYPES = [
  { value: 'course', label: '课程' },
  { value: 'pickup', label: '接送' },
  { value: 'activity', label: '活动' },
  { value: 'reminder', label: '提醒' }
]
const SCHOOL_TYPE = { value: 'school', label: '在校' }
const REPEATS = [
  { value: '', label: '单次' },
  { value: 'daily', label: '每天' },
  { value: 'weekly', label: '每周' }
]

function genKey() {
  return Date.now() + '-' + Math.random().toString(36).slice(2)
}

function repeatIndexOf(rrule) {
  if (!rrule) return 0
  if (rrule.indexOf('BYDAY=MO,TU,WE,TH,FR,SA,SU') >= 0) return 1
  if (rrule.indexOf('FREQ=WEEKLY') >= 0) return 2
  return 0
}

function buildRRule(repeatValue, startDate) {
  // 后端只支持 FREQ=WEEKLY：「每天」= 每周七天都选。
  if (repeatValue === 'weekly') return 'FREQ=WEEKLY;BYDAY=' + weekdayCode(startDate)
  if (repeatValue === 'daily') return DAILY_RRULE
  return ''
}

Page({
  data: {
    heading: '安排一件事',
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
    isSchoolEdit: false,
    submitting: false
  },

  onLoad(options) {
    this._edit = options.edit === '1' ? options.schedule_id : ''
    this._endTime = ''
    this._original = null
    this._repeatTouched = false
    this._createIdempotencyKey = genKey()
    if (!app.globalData.isAdmin) {
      wx.showToast({ title: '只有管理员才能录入', icon: 'none' })
      setTimeout(() => wx.navigateBack(), 500)
      return
    }
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
        const isSchoolEdit = s.type === SCHOOL_TYPE.value
        const types = isSchoolEdit ? TYPES.concat([SCHOOL_TYPE]) : TYPES
        const typeIndex = types.findIndex((t) => t.value === s.type)
        const childIndex = this.data.children.findIndex((c) => c.member_id === s.child_member_id)
        const ownerIndex = this.data.owners.findIndex((o) => o.member_id === s.owner_member_id)
        this._original = {
          rrule: s.rrule || '',
          type: s.type || '',
          ownerMemberID: s.owner_member_id || 0
        }
        this._endTime = s.end_time || ''
        this.setData({
          types,
          title: s.title || '',
          typeIndex: typeIndex >= 0 ? typeIndex : 0,
          startDate: s.start_date || todayStr(),
          startTime: s.start_time ? s.start_time.slice(0, 5) : '18:00',
          repeatIndex: repeatIndexOf(s.rrule || ''),
          childIndex: childIndex >= 0 ? childIndex : 0,
          ownerIndex: ownerIndex >= 0 ? ownerIndex : 0,
          location: s.location || '',
          note: s.note || '',
          isSchoolEdit
        })
      })
      .catch((e) => {
        if (e.code !== 4001) wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  onType(e) { this.setData({ typeIndex: Number(e.detail.value) }) },
  onRepeat(e) { this._repeatTouched = true; this.setData({ repeatIndex: Number(e.detail.value) }) },
  onChild(e) { this.setData({ childIndex: Number(e.detail.value) }) },
  onOwner(e) { this.setData({ ownerIndex: Number(e.detail.value) }) },
  onDate(e) { this.setData({ startDate: e.detail.value }) },
  onStartTime(e) { this.setData({ startTime: e.detail.value }) },

  submit() {
    if (this.data.submitting) return
    const child = this.data.children[this.data.childIndex]
    const owner = this.data.owners[this.data.ownerIndex]
    const type = this.data.isSchoolEdit ? SCHOOL_TYPE.value : this.data.types[this.data.typeIndex].value
    const needOwner = type !== SCHOOL_TYPE.value
    if (!this.data.title.trim()) return wx.showToast({ title: '请输入事项', icon: 'none' })
    if (!child) return wx.showToast({ title: '请先到「家庭」页添加孩子', icon: 'none' })
    if (needOwner && !owner) return wx.showToast({ title: '请选择责任人', icon: 'none' })

    const repeat = this.data.repeats[this.data.repeatIndex]
    const rrule = this._edit && !this._repeatTouched && this._original ? this._original.rrule : buildRRule(repeat.value, this.data.startDate)

    const payload = {
      title: this.data.title.trim(),
      type,
      child_member_id: child.member_id,
      start_date: this.data.startDate,
      rrule,
      start_time: this.data.startTime,
      end_time: this._endTime || '',
      location: this.data.location,
      note: this.data.note,
      owner_member_id: needOwner ? owner.member_id : 0
    }
    this.save(this._edit, payload, false)
  },

  // 创建走 POST，编辑走 PATCH；冲突(2005)可强制保存。
  save(scheduleId, payload, force) {
    this.setData({ submitting: true })
    const url = scheduleId ? `/v1/schedules/${scheduleId}` : '/v1/schedules'
    const method = scheduleId ? 'PATCH' : 'POST'
    const header = scheduleId ? {} : { 'Idempotency-Key': this._createIdempotencyKey }
    app.requestNotifySubscribe()
      .then(() => app.request(url, { method, data: Object.assign({}, payload, { force }), header }))
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
