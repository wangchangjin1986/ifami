// 成长时间线：从现有日程实例 + 打卡记录自动沉淀，家庭内只读。
const app = getApp()
const { fmt, addDays, hhmm } = require('../../utils/date')

function goalSuffix(t) {
  if (t.goal_type === 'timer') return t.goal_value + ' 分钟'
  if (t.goal_type === 'count') return t.goal_value + ' 个'
  if (t.goal_type === 'photo') return '拍照'
  return ''
}

function dayLabel(dateStr) {
  const today = fmt(new Date())
  const yesterday = fmt(addDays(new Date(), -1))
  if (dateStr === today) return '今天'
  if (dateStr === yesterday) return '昨天'
  return dateStr.slice(5).replace('-', '/')
}

function timelineDate(ts) {
  return (ts || '').slice(0, 10)
}

function scheduleTitle(i) {
  if (i.type === 'pickup') return '🚗 ' + i.title
  if (i.type === 'course') return '📚 ' + i.title
  if (i.type === 'activity') return '🌿 ' + i.title
  return '⏰ ' + i.title
}

Page({
  data: {
    logged: false,
    hasFamily: false,
    loading: true,
    children: [],
    childFilter: 0,
    childLabel: '全部',
    isAdmin: false,
    entries: [],
    filteredEntries: []
  },

  onLoad() {
    this.refresh()
  },

  refresh() {
    if (!app.globalData.token) {
      this.setData({ logged: false, hasFamily: false, loading: false, entries: [], filteredEntries: [] })
      return Promise.resolve()
    }
    this.setData({ loading: true })
    return app.refreshMe()
      .then(() => this.loadTimeline())
      .catch((e) => {
        this.setData({ loading: false })
        if (e.code === 4001) { app.logout(); this.setData({ logged: false, hasFamily: false }) }
        else wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  loadTimeline() {
    const fid = app.globalData.familyId
    this.setData({ logged: true })
    if (!fid) {
      this.setData({ hasFamily: false, loading: false, entries: [], filteredEntries: [] })
      return Promise.resolve()
    }
    const end = fmt(new Date())
    const start = fmt(addDays(new Date(), -60))
    this._rangeStart = start
    this._rangeEnd = end
    this.setData({ hasFamily: true, isAdmin: !!app.globalData.isAdmin })
    return Promise.all([
      app.request(`/v1/families/${fid}/members`),
      app.request('/v1/tasks'),
      app.request(`/v1/schedules/instances?start=${start}&end=${end}`),
      app.request(`/v1/checkins?start=${start}&end=${end}`)
    ])
      .then(([members, tasks, schedules, checkins]) => {
        const nameMap = {}
        const children = []
        ;(members || []).forEach((m) => {
          const name = m.name || m.relation_label || ('成员' + m.member_id)
          nameMap[m.member_id] = name
          if (m.type === 'virtual') children.push({ member_id: m.member_id, name })
        })
        this._nameMap = nameMap
        this._tasks = tasks || []
        this._schedules = schedules || []
        this.setData({ children })
        const entries = this.buildEntries(this._tasks || [], checkins || [], this._schedules || [], nameMap)
        this.setData({ entries, loading: false })
        this.applyFilter(this.data.childFilter)
      })
  },

  buildEntries(tasks, checkins, schedules, nameMap) {
    const taskMap = {}
    tasks.forEach((t) => { taskMap[t.task_id] = t })
    const out = []
    ;(checkins || []).forEach((c) => {
      const task = taskMap[c.task_id] || { name: '已删除任务', goal_type: '', goal_value: 0 }
      const date = timelineDate(c.created_at)
      out.push({
        id: 'checkin-' + c.checkin_id,
        kind: 'checkin',
        checkinId: c.checkin_id,
        childId: c.child_member_id,
        childName: nameMap[c.child_member_id] || '孩子',
        date,
        sortKey: c.created_at,
        day: dayLabel(date),
        title: '✅ ' + task.name + '打卡 ' + goalSuffix(task),
        meta: (nameMap[c.child_member_id] || '孩子') + ' · ' + (nameMap[c.submit_member_id] || '家人') + '提交'
      })
    })
    ;(schedules || [])
      .filter((i) => i.status !== 'cancelled' && i.occur_date <= fmt(new Date()))
      .forEach((i) => {
        out.push({
          id: 'schedule-' + i.instance_id,
          kind: 'schedule',
          childId: i.child_member_id,
          childName: nameMap[i.child_member_id] || '孩子',
          date: i.occur_date,
          sortKey: i.start_at,
          day: dayLabel(i.occur_date),
          title: scheduleTitle(i),
          meta: (nameMap[i.child_member_id] || '孩子') + ' · ' + hhmm(i.start_at) + ' · 责任：' + (nameMap[i.owner_member_id] || '—')
        })
      })
    return out.sort((a, b) => (a.sortKey < b.sortKey ? 1 : -1)).slice(0, 80)
  },

  applyFilter(childFilter) {
    const label = childFilter === 0 ? '全部' : (this._nameMap && this._nameMap[childFilter]) || '孩子'
    const filteredEntries = childFilter === 0 ? this.data.entries : this.data.entries.filter((e) => e.childId === childFilter)
    this.setData({ childFilter, childLabel: label, filteredEntries })
  },

  selectChild(e) {
    this.applyFilter(Number(e.currentTarget.dataset.id))
  },

  deleteCheckin(e) {
    const id = e.currentTarget.dataset.id
    if (!id) return
    wx.showModal({
      title: '删除打卡记录',
      content: '删除后会影响连续天数、周报和时间线统计，确认删除？',
      confirmText: '删除',
      confirmColor: '#BF4A3C',
      success: (r) => {
        if (!r.confirm) return
        app.request(`/v1/checkins/${id}`, { method: 'DELETE' })
          .then(() => {
            wx.showToast({ title: '已删除', icon: 'success' })
            return this.refresh()
          })
          .catch((err) => wx.showToast({ title: err.message || '删除失败', icon: 'none' }))
      }
    })
  },

  goBack() { wx.navigateBack() },

  onPullDownRefresh() {
    this.refresh().then(() => wx.stopPullDownRefresh())
  }
})
