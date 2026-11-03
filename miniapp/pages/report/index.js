// 家庭周报：基于现有日程实例、任务和打卡记录自动生成家庭内周小结。
const app = getApp()
const { fmt, mondayOf, addDays, hhmm } = require('../../utils/date')

const FREQ_LABEL = { daily: '每天', weekdays: '工作日', weekly: '每周' }

function goalSuffix(t) {
  if (t.goal_type === 'timer') return t.goal_value + ' 分钟'
  if (t.goal_type === 'count') return t.goal_value + ' 个'
  if (t.goal_type === 'photo') return '拍照'
  return ''
}

function dateOnly(ts) {
  return (ts || '').slice(0, 10)
}

function inRange(dateStr, start, endExclusive) {
  return dateStr >= start && dateStr < endExclusive
}

function countWeekdays(startDate, endDate) {
  let n = 0
  for (let d = new Date(startDate); d <= endDate; d = addDays(d, 1)) {
    const day = d.getDay()
    if (day !== 0 && day !== 6) n++
  }
  return n
}

function scheduleIcon(type) {
  if (type === 'pickup') return '🚗'
  if (type === 'course') return '📚'
  if (type === 'activity') return '🌿'
  return '⏰'
}

Page({
  data: {
    logged: false,
    hasFamily: false,
    loading: true,
    children: [],
    childFilter: 0,
    weekLabel: '',
    summary: { completionRate: '—', checkinDone: '0', scheduleCount: '0', pastScheduleText: '0/0' },
    enoughData: false,
    taskRows: [],
    childHighlights: [],
    nextWeekItems: []
  },

  onLoad() {
    this.refresh()
  },

  refresh() {
    if (!app.globalData.token) {
      this.setData({ logged: false, hasFamily: false, loading: false })
      return Promise.resolve()
    }
    this.setData({ loading: true })
    return app.refreshMe()
      .then(() => this.loadReport())
      .catch((e) => {
        this.setData({ loading: false })
        if (e.code === 4001) { app.logout(); this.setData({ logged: false, hasFamily: false }) }
        else wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  loadReport() {
    const fid = app.globalData.familyId
    this.setData({ logged: true })
    if (!fid) {
      this.setData({ hasFamily: false, loading: false })
      return Promise.resolve()
    }

    const now = new Date()
    const mon = mondayOf(now)
    const weekStart = fmt(mon)
    const weekEndDate = addDays(mon, 6)
    const weekEnd = fmt(weekEndDate)
    const nextStartDate = addDays(mon, 7)
    const nextEndDate = addDays(mon, 13)
    this._weekStart = weekStart
    this._weekEndExclusive = fmt(addDays(mon, 7))
    this._weekEnd = weekEnd
    this._scopeEndDate = now < weekEndDate ? now : weekEndDate
    this._nextStart = fmt(nextStartDate)
    this._nextEnd = fmt(nextEndDate)
    this.setData({ hasFamily: true, weekLabel: weekStart.slice(5).replace('-', '/') + ' – ' + weekEnd.slice(5).replace('-', '/') })

    return Promise.all([
      app.request(`/v1/families/${fid}/members`),
      app.request('/v1/tasks'),
      app.request(`/v1/schedules/instances?start=${weekStart}&end=${weekEnd}`),
      app.request(`/v1/schedules/instances?start=${this._nextStart}&end=${this._nextEnd}`)
    ])
      .then(([members, tasks, weekSchedules, nextSchedules]) => {
        const nameMap = {}
        const children = []
        ;(members || []).forEach((m) => {
          const name = m.name || m.relation_label || ('成员' + m.member_id)
          nameMap[m.member_id] = name
          if (m.type === 'virtual') children.push({ member_id: m.member_id, name })
        })
        this._nameMap = nameMap
        this._tasks = tasks || []
        this._weekSchedules = weekSchedules || []
        this._nextSchedules = nextSchedules || []
        this.setData({ children })
        return this.loadCheckins(tasks || [])
      })
      .then((checkinGroups) => {
        this._checkinGroups = checkinGroups || []
        this.applyFilter(this.data.childFilter)
        this.setData({ loading: false })
      })
  },

  loadCheckins(tasks) {
    if (!tasks.length) return Promise.resolve([])
    return Promise.all(tasks.map((t) => app.request(`/v1/tasks/${t.task_id}/checkins`).then((rows) => ({ task: t, rows: rows || [] }))))
  },

  buildReport(childFilter) {
    const nameMap = this._nameMap || {}
    const weekStart = this._weekStart
    const weekEndExclusive = this._weekEndExclusive
    const tasks = (this._tasks || []).filter((t) => childFilter === 0 || t.child_member_id === childFilter)
    const schedules = (this._weekSchedules || []).filter((i) => i.status !== 'cancelled' && (childFilter === 0 || i.child_member_id === childFilter))
    const nextSchedules = (this._nextSchedules || []).filter((i) => i.status !== 'cancelled' && (childFilter === 0 || i.child_member_id === childFilter))
    const elapsedDays = Math.max(1, Math.floor((this._scopeEndDate - new Date(weekStart + 'T00:00:00')) / 86400000) + 1)
    const elapsedWeekdays = countWeekdays(new Date(weekStart + 'T00:00:00'), this._scopeEndDate)

    const checkinsByTask = {}
    ;(this._checkinGroups || []).forEach((g) => {
      checkinsByTask[g.task.task_id] = (g.rows || []).filter((c) => inRange(dateOnly(c.created_at), weekStart, weekEndExclusive))
    })

    let totalExpected = 0
    let totalDone = 0
    const taskRows = tasks.map((t) => {
      let expected = elapsedDays
      if (t.freq === 'weekdays') expected = elapsedWeekdays
      if (t.freq === 'weekly') expected = t.weekly_target || 1
      expected = Math.max(1, expected)
      const done = (checkinsByTask[t.task_id] || []).length
      totalExpected += expected
      totalDone += Math.min(done, expected)
      return {
        id: t.task_id,
        title: t.name + ' ' + goalSuffix(t),
        childName: nameMap[t.child_member_id] || '孩子',
        progress: done + '/' + expected,
        meta: (FREQ_LABEL[t.freq] || t.freq) + ' · 连续 ' + (t.streak_days || 0) + ' 天',
        completed: done >= expected
      }
    })

    const checkinDone = Object.keys(checkinsByTask).reduce((n, id) => n + checkinsByTask[id].length, 0)
    const rate = totalExpected > 0 ? Math.round((totalDone / totalExpected) * 100) : 0
    const pastScheduleCount = schedules.filter((i) => i.occur_date <= fmt(new Date())).length
    const childHighlights = this.buildHighlights(childFilter, checkinsByTask, schedules, nameMap)
    const nextWeekItems = nextSchedules
      .sort((a, b) => (a.start_at < b.start_at ? -1 : 1))
      .slice(0, 5)
      .map((i) => ({
        id: i.instance_id,
        title: scheduleIcon(i.type) + ' ' + i.title,
        meta: i.occur_date.slice(5).replace('-', '/') + ' ' + hhmm(i.start_at) + ' · ' + (nameMap[i.child_member_id] || '孩子') + ' · 责任：' + (nameMap[i.owner_member_id] || '—')
      }))

    return {
      summary: {
        completionRate: totalExpected > 0 ? rate + '%' : '—',
        checkinDone: String(checkinDone),
        scheduleCount: String(schedules.length),
        pastScheduleText: pastScheduleCount + '/' + schedules.length
      },
      enoughData: checkinDone > 0 || schedules.length > 0,
      taskRows,
      childHighlights,
      nextWeekItems
    }
  },

  buildHighlights(childFilter, checkinsByTask, schedules, nameMap) {
    const taskMap = {}
    ;(this._tasks || []).forEach((t) => { taskMap[t.task_id] = t })
    const out = []
    Object.keys(checkinsByTask).forEach((id) => {
      const t = taskMap[id]
      if (!t || (childFilter !== 0 && t.child_member_id !== childFilter)) return
      const rows = checkinsByTask[id]
      if (!rows.length) return
      out.push({
        id: 'task-' + id,
        childName: nameMap[t.child_member_id] || '孩子',
        title: '✅ ' + t.name + '完成 ' + rows.length + ' 次',
        meta: goalSuffix(t) + ' · 责任：' + (nameMap[t.owner_member_id] || '—')
      })
    })
    schedules
      .filter((i) => i.occur_date <= fmt(new Date()))
      .slice(0, 6)
      .forEach((i) => {
        out.push({
          id: 'schedule-' + i.instance_id,
          childName: nameMap[i.child_member_id] || '孩子',
          title: scheduleIcon(i.type) + ' ' + i.title,
          meta: i.occur_date.slice(5).replace('-', '/') + ' ' + hhmm(i.start_at) + ' · 责任：' + (nameMap[i.owner_member_id] || '—')
        })
      })
    return out.slice(0, 8)
  },

  selectChild(e) {
    this.applyFilter(Number(e.currentTarget.dataset.id))
  },

  applyFilter(childFilter) {
    const report = this.buildReport(childFilter)
    this.setData(Object.assign({ childFilter }, report))
  },

  goTaskTemplates() {
    wx.navigateTo({ url: '/pages/task/index?batch=1' })
  },

  goBack() { wx.navigateBack() },

  onShareAppMessage() {
    return {
      title: '本周家庭小结：' + this.data.weekLabel,
      path: '/pages/report/index'
    }
  },

  onPullDownRefresh() {
    this.refresh().then(() => wx.stopPullDownRefresh())
  }
})
