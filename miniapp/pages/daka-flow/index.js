// 打卡提交：为谁打卡 + 打卡方式 + 完成态，对齐 prototype scr-daka-flow / scr-daka-done。
const app = getApp()

const METHODS = [
  { key: 'photo', label: '📷 拍照' },
  { key: 'timer', label: '⏱ 计时' },
  { key: 'count', label: '🔢 计数' }
]

function genKey() {
  return Date.now() + '-' + Math.random().toString(36).slice(2)
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
    taskId: '',
    taskName: '',
    title: '',
    children: [],
    childIndex: 0,
    childName: '',
    childMemberId: 0,
    goalType: 'timer',
    goalValue: 0,
    goalUnit: '分钟',
    methods: METHODS,
    methodIndex: 1,
    amount: '',
    submitting: false,
    done: false,
    streakDays: 0
  },

  onLoad(options) {
    const goalType = options.goal_type || 'timer'
    const goalValue = parseInt(options.goal_value, 10) || 0
    const key = goalType === 'photo' ? 'photo' : (goalType === 'count' ? 'count' : 'timer')
    this.setData({
      taskId: options.task_id || '',
      taskName: decodeQueryText(options.name, ''),
      title: decodeQueryText(options.title, '打卡'),
      childName: decodeQueryText(options.child, '孩子'),
      childMemberId: parseInt(options.child_member_id, 10) || 0,
      goalType,
      goalValue,
      goalUnit: goalType === 'photo' ? '张' : (goalType === 'count' ? '个' : '分钟'),
      methodIndex: METHODS.findIndex((m) => m.key === key)
    })
    this.loadChildrenAndTasks()
  },

  loadChildrenAndTasks() {
    const fid = app.globalData.familyId
    if (!fid) return Promise.resolve()
    return Promise.all([
      app.request(`/v1/families/${fid}/members`),
      app.request('/v1/tasks')
    ]).then(([members, tasks]) => {
      const children = (members || [])
        .filter((m) => m.type === 'virtual')
        .map((m) => ({ member_id: m.member_id, name: m.name || m.relation_label || ('孩子' + m.member_id) }))
      const childIndex = Math.max(0, children.findIndex((c) => String(c.member_id) === String(this.data.childMemberId)))
      this._tasks = tasks || []
      this.setData({ children, childIndex })
    }).catch((e) => {
      if (e.code !== 4001) wx.showToast({ title: e.message || '加载孩子失败', icon: 'none' })
    })
  },

  selectMethod(e) {
    if (this.data.submitting) return
    const i = Number(e.currentTarget.dataset.i)
    const key = this.data.methods[i].key
    this.setData({ methodIndex: i, goalUnit: key === 'photo' ? '张' : (key === 'count' ? '个' : '分钟') })
  },

  selectChild(e) {
    if (this.data.submitting) return
    const idx = Number(e.currentTarget.dataset.i)
    const child = this.data.children[idx]
    if (!child) return
    const tasks = this._tasks || []
    const sameName = tasks.find((t) => String(t.child_member_id) === String(child.member_id) && t.name === this.data.taskName)
    const fallback = tasks.find((t) => String(t.child_member_id) === String(child.member_id))
    const next = sameName || fallback
    if (!next) {
      wx.showToast({ title: child.name + ' 暂无可打卡任务', icon: 'none' })
      return
    }
    const key = next.goal_type === 'photo' ? 'photo' : (next.goal_type === 'count' ? 'count' : 'timer')
    this.setData({
      childIndex: idx,
      childMemberId: child.member_id,
      childName: child.name,
      taskId: next.task_id,
      taskName: next.name,
      title: next.name + (next.goal_type === 'photo' ? ' 拍照' : next.goal_type === 'count' ? ' ' + next.goal_value + ' 个' : ' ' + next.goal_value + ' 分钟'),
      goalType: next.goal_type,
      goalValue: next.goal_value || 0,
      goalUnit: next.goal_type === 'photo' ? '张' : (next.goal_type === 'count' ? '个' : '分钟'),
      methodIndex: Math.max(0, METHODS.findIndex((m) => m.key === key)),
      amount: ''
    })
  },


  submit() {
    if (this.data.submitting) return
    const m = this.data.methods[this.data.methodIndex]
    if (m.key === 'photo') {
      this.submitPhoto()
      return
    }
    const n = parseInt(this.data.amount, 10)
    if (!n || n <= 0) return wx.showToast({ title: '请输入本次' + this.data.goalUnit + '数', icon: 'none' })

    const payload = m.key === 'count' ? { count: n } : { seconds: n * 60 }
    this.submitPayload(payload)
  },

  submitPhoto() {
    wx.chooseMedia({
      count: 1,
      mediaType: ['image'],
      sourceType: ['album', 'camera'],
      success: (res) => {
        const file = (res.tempFiles || [])[0]
        const path = file && file.tempFilePath
        if (!path) return wx.showToast({ title: '未选择照片', icon: 'none' })
        this.uploadPhoto(path)
      },
      fail: () => wx.showToast({ title: '已取消拍照', icon: 'none' })
    })
  },

  uploadPhoto(filePath) {
    this.setData({ submitting: true })
    wx.showLoading({ title: '保存中…', mask: true })
    wx.uploadFile({
      url: app.globalData.apiBase + `/v1/tasks/${this.data.taskId}/checkins/photo`,
      filePath,
      name: 'file',
      formData: { submit_member_id: '0' },
      header: {
        Authorization: 'Bearer ' + app.globalData.token,
        'X-Family-Id': String(app.globalData.familyId),
        'Idempotency-Key': genKey()
      },
      success: (res) => {
        let body = {}
        try { body = JSON.parse(res.data) } catch (e) { /* 非 JSON 响应按失败处理 */ }
        if (res.statusCode === 200 && body.code === 0) {
          const r = body.data || {}
          wx.hideLoading()
          this.syncPreviousPage()
          this.setData({ submitting: false, done: true, streakDays: r.streak_days || 0 })
          wx.showToast({ title: '已保存', icon: 'success' })
          return
        }
        wx.hideLoading()
        this.setData({ submitting: false })
        if (body.code === 4001) { app.logout(); wx.navigateBack() }
        else wx.showToast({ title: body.message || '提交失败', icon: 'none' })
      },
      fail: () => {
        wx.hideLoading()
        this.setData({ submitting: false })
        wx.showToast({ title: '网络异常，上传失败', icon: 'none' })
      }
    })
  },

  submitPayload(payload, mediaTosKey = '') {
    this.setData({ submitting: true })
    wx.showLoading({ title: '保存中…', mask: true })
    app.request(`/v1/tasks/${this.data.taskId}/checkins`, {
      method: 'POST',
      data: { payload, media_tos_key: mediaTosKey, submit_member_id: 0 },
      header: { 'Idempotency-Key': genKey() }
    })
      .then((r) => {
        wx.hideLoading()
        this.syncPreviousPage()
        this.setData({ submitting: false, done: true, streakDays: r.streak_days || 0 })
        wx.showToast({ title: '已保存', icon: 'success' })
      })
      .catch((e) => {
        wx.hideLoading()
        this.setData({ submitting: false })
        if (e.code === 4001) { app.logout(); wx.navigateBack() }
        else wx.showToast({ title: e.message || '提交失败', icon: 'none' })
      })
  },

  // 提交成功后先把上一页任务置为“已完成”，返回时不再等待一次完整刷新，降低体感耗时。
  syncPreviousPage() {
    const pages = getCurrentPages()
    const prev = pages[pages.length - 2]
    if (prev && typeof prev.markTaskChecked === 'function') {
      prev.markTaskChecked(this.data.taskId)
    }
  },

  // 再打一个：回到填写态（同任务再记一次）
  more() {
    this.setData({ done: false, amount: '' })
  },

  finish() { wx.navigateBack() },
  goBack() { wx.navigateBack() }
})
