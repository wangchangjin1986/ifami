const app = getApp()

const FREQ_LABEL = { daily: '每天', weekdays: '工作日' }

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

function goalLabel(goalType, goalValue) {
  if (goalType === 'count') return (goalValue || 0) + ' 个'
  if (goalType === 'photo') return '拍照'
  return (goalValue || 0) + ' 分钟'
}

function freqLabel(freq, weeklyTarget) {
  if (freq === 'weekly') return '每周 ' + (weeklyTarget || 'N') + ' 次'
  return FREQ_LABEL[freq] || freq || '每天'
}

function amountLabel(goalType, payload) {
  payload = payload || {}
  if (goalType === 'count') return (payload.count || 0) + ' 个'
  if (goalType === 'timer') return Math.round((payload.seconds || 0) / 60) + ' 分钟'
  return '拍照'
}

function timeLabel(s) {
  if (!s) return '未知时间'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s.slice(0, 16)
  const now = new Date()
  const today = now.toISOString().slice(0, 10)
  const date = d.toISOString().slice(0, 10)
  const hm = d.toTimeString().slice(0, 5)
  if (date === today) return '今天 ' + hm
  return date.slice(5).replace('-', '/') + ' ' + hm
}

Page({
  data: {
    loading: true,
    task: null,
    taskId: '',
    title: '打卡任务详情',
    childName: '',
    ownerName: '',
    goalLabel: '',
    freqLabel: '',
    remindLabel: '未设置',
    checkins: []
  },

  onLoad(options = {}) {
    const task = {
      task_id: options.task_id || '',
      name: decodeQueryText(options.name, ''),
      title: decodeQueryText(options.title, ''),
      goal_type: options.goal_type || 'timer',
      goal_value: parseInt(options.goal_value, 10) || 0,
      freq: options.freq || 'daily',
      weekly_target: parseInt(options.weekly_target, 10) || 0,
      child_member_id: parseInt(options.child_member_id, 10) || 0,
      owner_member_id: parseInt(options.owner_member_id, 10) || 0,
      remind_time: decodeQueryText(options.remind_time, '')
    }
    this.setData({
      task,
      taskId: task.task_id,
      title: task.title || (task.name ? task.name + ' ' + goalLabel(task.goal_type, task.goal_value) : '打卡任务详情'),
      goalLabel: goalLabel(task.goal_type, task.goal_value),
      freqLabel: freqLabel(task.freq, task.weekly_target),
      remindLabel: task.remind_time ? task.remind_time + ' 未完成提醒' : '未设置'
    })
    this.refresh()
  },

  refresh() {
    const fid = app.globalData.familyId
    const task = this.data.task
    if (!fid || !task || !task.task_id) {
      this.setData({ loading: false })
      return Promise.resolve()
    }
    this.setData({ loading: true })
    return Promise.all([
      app.request(`/v1/families/${fid}/members`),
      app.request(`/v1/tasks/${task.task_id}/checkins`)
    ])
      .then(([members, checkins]) => {
        const nameMap = {}
        ;(members || []).forEach((m) => { nameMap[m.member_id] = m.name || m.relation_label || ('成员' + m.member_id) })
        const rows = (checkins || []).slice().reverse().slice(0, 20).map((c) => Object.assign({}, c, {
          timeLabel: timeLabel(c.created_at),
          submitName: nameMap[c.submit_member_id] || '家人',
          amountLabel: amountLabel(task.goal_type, c.payload)
        }))
        this.setData({
          childName: nameMap[task.child_member_id] || '孩子',
          ownerName: nameMap[task.owner_member_id] || '—',
          checkins: rows,
          loading: false
        })
      })
      .catch((e) => {
        this.setData({ loading: false })
        wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  editTask() {
    const t = this.data.task
    wx.navigateTo({
      url: '/pages/task/index?edit=1' +
        '&task_id=' + t.task_id +
        '&name=' + encodeURIComponent(t.name || '') +
        '&goal_type=' + (t.goal_type || '') +
        '&goal_value=' + (t.goal_value || 0) +
        '&freq=' + (t.freq || 'daily') +
        '&weekly_target=' + (t.weekly_target || 0) +
        '&child_member_id=' + (t.child_member_id || 0) +
        '&owner_member_id=' + (t.owner_member_id || 0) +
        '&remind_time=' + encodeURIComponent(t.remind_time || '')
    })
  },

  pauseTask() {
    const t = this.data.task
    wx.showModal({
      title: '暂停提醒',
      content: '暂停后任务仍可手动打卡，但不会再按时间提醒责任人。',
      confirmText: '暂停',
      success: (r) => {
        if (!r.confirm) return
        app.request(`/v1/tasks/${t.task_id}`, {
          method: 'PATCH',
          data: {
            name: t.name,
            goal_type: t.goal_type,
            goal_value: t.goal_value,
            freq: t.freq,
            weekly_target: t.weekly_target,
            child_member_id: t.child_member_id,
            owner_member_id: t.owner_member_id,
            remind_time: ''
          }
        })
          .then(() => {
            const next = Object.assign({}, t, { remind_time: '' })
            this.setData({ task: next, remindLabel: '未设置' })
            wx.showToast({ title: '已暂停提醒', icon: 'success' })
          })
          .catch((e) => wx.showToast({ title: e.message || '暂停失败', icon: 'none' }))
      }
    })
  },

  archiveTask() {
    const t = this.data.task
    wx.showModal({
      title: '归档任务',
      content: `归档「${t.name}」后，任务不再展示和提醒，历史打卡记录保留。`,
      confirmText: '归档',
      confirmColor: '#BF4A3C',
      success: (r) => {
        if (!r.confirm) return
        app.request(`/v1/tasks/${t.task_id}`, { method: 'DELETE' })
          .then(() => {
            wx.showToast({ title: '已归档', icon: 'success' })
            setTimeout(() => wx.navigateBack(), 600)
          })
          .catch((e) => wx.showToast({ title: e.message || '归档失败', icon: 'none' }))
      }
    })
  },

  deleteCheckin(e) {
    const id = e.currentTarget.dataset.id
    if (!id) return
    wx.showModal({
      title: '删除打卡记录',
      content: '删除后会影响连续天数、周报和成长时间线统计，确认删除？',
      confirmText: '删除',
      confirmColor: '#BF4A3C',
      success: (r) => {
        if (!r.confirm) return
        app.request(`/v1/checkins/${id}`, { method: 'DELETE' })
          .then(() => {
            wx.showToast({ title: '已删除', icon: 'success' })
            return this.refresh()
          })
          .catch((e) => wx.showToast({ title: e.message || '删除失败', icon: 'none' }))
      }
    })
  },

  goBack() { wx.navigateBack() }
})
