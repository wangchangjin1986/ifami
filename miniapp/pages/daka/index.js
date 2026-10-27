// 打卡页：按孩子分组的任务列表，对齐 prototype scr-daka（dot + 任务名 + 责任 meta + 去打卡 chip）。
const app = getApp()

const FREQ_LABEL = { daily: '每天', weekdays: '工作日', custom: '自定义' }

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
    sheetOpen: false
  },

  onShow() {
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 2 })
    }
    this.refresh()
  },

  refresh() {
    if (!app.globalData.token) {
      this.setData({ logged: false, hasFamily: false, groups: [] })
      return
    }
    const fid = app.globalData.familyId
    this.setData({ logged: true })
    if (!fid) {
      this.setData({ hasFamily: false, groups: [] })
      return
    }
    this.setData({ hasFamily: true })

    Promise.all([
      app.request('/v1/tasks'),
      app.request(`/v1/families/${fid}/members`)
    ])
      .then(([tasks, members]) => {
        const nameMap = {}
        ;(members || []).forEach((m) => { nameMap[m.member_id] = m.name || '成员' + m.member_id })

        const buildTask = (t, childName) => {
          const suffix = goalSuffix(t)
          const meta = (FREQ_LABEL[t.freq] || t.freq) + ' · 责任：' + (nameMap[t.owner_member_id] || '—') +
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
      .catch((e) => {
        if (e.code === 4001) { app.logout(); this.setData({ logged: false, hasFamily: false, groups: [] }) }
        else wx.showToast({ title: e.message || '加载失败', icon: 'none' })
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

  onPullDownRefresh() {
    this.refresh()
    wx.stopPullDownRefresh()
  }
})
