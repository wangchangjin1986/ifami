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

Page({
  data: {
    taskId: '',
    title: '',
    childName: '',
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
      title: options.title ? decodeURIComponent(options.title) : '打卡',
      childName: options.child ? decodeURIComponent(options.child) : '孩子',
      goalType,
      goalValue,
      goalUnit: goalType === 'count' ? '个' : '分钟',
      methodIndex: METHODS.findIndex((m) => m.key === key)
    })
  },

  selectMethod(e) {
    if (this.data.submitting) return
    this.setData({ methodIndex: Number(e.currentTarget.dataset.i) })
  },


  submit() {
    if (this.data.submitting) return
    const m = this.data.methods[this.data.methodIndex]
    if (m.key === 'photo') {
      wx.showToast({ title: '拍照打卡待对象存储接入', icon: 'none' })
      return
    }
    const n = parseInt(this.data.amount, 10)
    if (!n || n <= 0) return wx.showToast({ title: '请输入本次' + this.data.goalUnit + '数', icon: 'none' })

    const payload = m.key === 'count' ? { count: n } : { seconds: n * 60 }
    this.setData({ submitting: true })
    wx.showLoading({ title: '保存中…', mask: true })
    app.request(`/v1/tasks/${this.data.taskId}/checkins`, {
      method: 'POST',
      data: { payload, submit_member_id: 0 },
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
