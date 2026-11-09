// 打卡页：按孩子分组的任务列表，对齐 prototype scr-daka（dot + 任务名 + 责任 meta + 去打卡 chip）。
const app = getApp()

const FREQ_LABEL = { daily: '每天', weekdays: '工作日' }

function freqLabel(t) {
  if (t.freq === 'weekly') return '每周 ' + (t.weekly_target || 'N') + ' 次'
  return FREQ_LABEL[t.freq] || t.freq
}

function goalSuffix(t) {
  if (t.goal_type === 'timer') return t.goal_value + ' 分钟'
  if (t.goal_type === 'count') return t.goal_value + ' 个'
  if (t.goal_type === 'photo') return '拍照'
  return ''
}

Page({
  data: {
    logged: false,
    hasFamily: false,
    groups: [],
    isAdmin: false,
    sheetOpen: false,
    loading: false,
    skipNextRefresh: false
  },

  onShow() {
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 2 })
    }
    if (this.data.skipNextRefresh) {
      this.setData({ skipNextRefresh: false })
      return
    }
    this.refresh()
  },

  refresh() {
    if (!app.globalData.token) {
      this.setData({ logged: false, hasFamily: false, groups: [], loading: false })
      return Promise.resolve()
    }
    this.setData({ loading: this.data.groups.length === 0 })
    const ready = app.globalData.familyId && (app.globalData.memberships || []).length > 0
    const ensureSession = ready ? Promise.resolve() : app.refreshMe()
    return ensureSession
      .then(() => this.loadTasks())
      .catch((e) => {
        if (e.code === 4001) { app.logout(); this.setData({ logged: false, hasFamily: false, groups: [] }) }
        else wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
      .finally(() => this.setData({ loading: false }))
  },

  loadTasks() {
    const fid = app.globalData.familyId
    this.setData({ logged: true })
    if (!fid) {
      this.setData({ hasFamily: false, groups: [] })
      return Promise.resolve()
    }
    this.setData({ hasFamily: true, isAdmin: !!app.globalData.isAdmin })

    return Promise.all([
      app.request('/v1/tasks'),
      app.request(`/v1/families/${fid}/members`)
    ])
      .then(([tasks, members]) => {
        const nameMap = {}
        ;(members || []).forEach((m) => { nameMap[m.member_id] = m.name || '成员' + m.member_id })

        const buildTask = (t, childName) => {
          const suffix = goalSuffix(t)
          const meta = freqLabel(t) + ' · 责任：' + (nameMap[t.owner_member_id] || '—') +
            (t.today_checked ? ' · 已完成' : '')
          return Object.assign({}, t, { title: t.name + (suffix ? ' ' + suffix : ''), meta, childName })
        }

        const byChild = {}
        ;(tasks || []).forEach((t) => {
          const cid = t.child_member_id || 0
          ;(byChild[cid] = byChild[cid] || []).push(t)
        })

        const groups = []
        ;(members || []).forEach((m) => {
          if (m.type !== 'virtual') return
          const list = byChild[m.member_id]
          if (list && list.length) groups.push({ childId: m.member_id, childName: m.name || '孩子', tasks: list.map((t) => buildTask(t, m.name || '孩子')) })
        })
        if (byChild[0] && byChild[0].length) groups.push({ childId: 0, childName: '未分配', tasks: byChild[0].map((t) => buildTask(t, '未分配')) })

        this.setData({ groups })
      })
  },

  markTaskChecked(taskId) {
    const groups = (this.data.groups || []).map((g) => Object.assign({}, g, {
      tasks: (g.tasks || []).map((t) => {
        if (String(t.task_id) !== String(taskId)) return t
        const baseMeta = (t.meta || '').replace(/ · 已完成/g, '')
        return Object.assign({}, t, { today_checked: true, meta: baseMeta + ' · 已完成' })
      })
    }))
    this.setData({ groups, skipNextRefresh: true })
  },

  openTaskDetail(e) {
    const t = e.currentTarget.dataset.task
    if (!this.data.isAdmin || !t) return
    wx.navigateTo({
      url: '/pages/task-detail/index?task_id=' + t.task_id +
        '&name=' + encodeURIComponent(t.name || '') +
        '&title=' + encodeURIComponent(t.title || '') +
        '&goal_type=' + (t.goal_type || '') +
        '&goal_value=' + (t.goal_value || 0) +
        '&freq=' + (t.freq || 'daily') +
        '&weekly_target=' + (t.weekly_target || 0) +
        '&child_member_id=' + (t.child_member_id || 0) +
        '&owner_member_id=' + (t.owner_member_id || 0) +
        '&remind_time=' + encodeURIComponent(t.remind_time || '')
    })
  },

  editTask(e) {
    const t = e.currentTarget.dataset.task
    if (!this.data.isAdmin || !t) return
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

  tapTask(e) {
    const t = e.currentTarget.dataset.task
    if (t.today_checked) {
      wx.showToast({ title: '今日已打卡', icon: 'none' })
      return
    }
    if (t.goal_type === 'photo') {
      wx.showToast({ title: '拍照打卡待对象存储接入', icon: 'none' })
      return
    }
    wx.navigateTo({
      url: '/pages/daka-flow/index?task_id=' + t.task_id +
        '&title=' + encodeURIComponent(t.title) +
        '&goal_type=' + t.goal_type +
        '&goal_value=' + (t.goal_value || 0) +
        '&child=' + encodeURIComponent(t.childName || '')
    })
  },

  openFab() { this.setData({ sheetOpen: true }) },
  closeFab() { this.setData({ sheetOpen: false }) },
  noop() {},
  goEvent() {
    this.closeFab()
    wx.navigateTo({ url: '/pages/event/index' })
  },
  goTask() {
    this.closeFab()
    wx.navigateTo({ url: '/pages/task/index' })
  },
  goTaskTemplates() {
    this.closeFab()
    wx.navigateTo({ url: '/pages/task/index?batch=1' })
  },
  goImport() {
    this.closeFab()
    wx.navigateTo({ url: '/pages/import/index' })
  },

  onPullDownRefresh() {
    this.refresh()
      .finally(() => wx.stopPullDownRefresh())
  }
})
