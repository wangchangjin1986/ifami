// 今日页：今日聚合视图，对齐 prototype scr-today（时间 + 标题含责任人 chip + 地点·备注）。
// 点击条目进入日程详情页（详情页按角色决定是否可编辑/取消）。
const app = getApp()
const { todayStr, hhmm } = require('../../utils/date')
const { detailUrl } = require('../../utils/nav')

const WEEK_CN = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

Page({
  data: {
    logged: false,
    hasFamily: false,
    dateStr: '',
    familyName: '',
    notifyReddots: [],
    hasReddot: false,
    items: [],
    empty: true,
    isAdmin: false,
    sheetOpen: false
  },

  onLoad(options) {
    app.reportNotifyReceipt(options.job_id)
  },

  onShow() {
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 0 })
    }
    if (app.routeGrandparentHome('pages/today/index')) return
    this.refresh()
  },

  refresh() {
    const now = new Date()
    this.setData({ dateStr: (now.getMonth() + 1) + '月' + now.getDate() + '日 ' + WEEK_CN[now.getDay()] })
    if (!app.globalData.token) {
      this.setData({ logged: false, hasFamily: false, items: [], empty: true, familyName: '', notifyReddots: [], hasReddot: false })
      return
    }
    const handleErr = (e, silent = false) => {
      if (e.code === 4001) {
        app.logout()
        this.setData({ logged: false, hasFamily: false, items: [], empty: true, notifyReddots: [], hasReddot: false })
        return
      }
      if (!silent) wx.showToast({ title: e.message || '加载失败', icon: 'none' })
    }
    const ready = app.globalData.familyId && (app.globalData.memberships || []).length > 0
    if (!ready) {
      app.ensureLogin().then(() => {
        if (app.routeGrandparentHome('pages/today/index')) return
        return this.loadToday()
      }).catch((e) => handleErr(e))
      return
    }
    this.loadToday().catch((e) => handleErr(e))
    app.maybeRefreshMe().catch((e) => handleErr(e, true))
  },

  loadToday() {
    const fid = app.globalData.familyId
    this.setData({ logged: true })
    if (!fid) {
      this.setData({ hasFamily: false, items: [], empty: true, notifyReddots: [], hasReddot: false })
      return
    }
    this.setData({ hasFamily: true, isAdmin: !!app.globalData.isAdmin })

    const today = todayStr()
    return Promise.all([
      app.request(`/v1/schedules/instances?start=${today}&end=${today}`),
      app.request(`/v1/families/${fid}/members`),
      app.request('/v1/families'),
      app.loadNotifyReddots().catch(() => [])
    ])
      .then(([instances, members, families, reddots]) => {
        const nameMap = {}
        ;(members || []).forEach((m) => { nameMap[m.member_id] = m.name || m.relation_label || '成员' + m.member_id })
        const cur = (families || []).find((f) => f.family_id === fid) || {}
        const items = (instances || [])
          .filter((it) => it.status !== 'cancelled')
          .map((it) => ({
            instanceId: it.instance_id,
            scheduleId: it.schedule_id,
            type: it.type,
            occurDate: it.occur_date,
            startAt: it.start_at,
            endAt: it.end_at,
            location: it.location,
            note: it.note,
            childMemberId: it.child_member_id,
            ownerMemberId: it.owner_member_id,
            start: hhmm(it.start_at),
            title: it.title,
            owner: nameMap[it.owner_member_id] || '',
            meta: [it.location, it.note].filter(Boolean).join(' · ')
          }))
          .sort((a, b) => (a.start < b.start ? -1 : 1))
        this.setData({ familyName: cur.name || '', notifyReddots: reddots || [], hasReddot: (reddots || []).length > 0, items, empty: items.length === 0 })
      })
  },

  tapReddot(e) {
    const id = e.currentTarget.dataset.id
    const job = (this.data.notifyReddots || []).find((x) => String(x.job_id) === String(id))
    if (!job) return
    app.reportNotifyReceipt(job.job_id)
      .then(() => this.loadToday())
    const page = job.payload && job.payload.page
    app.openNotifyPage(page)
  },

  // 点击条目 → 日程详情（改分工/编辑/取消本次都在详情页完成）。
  tapItem(e) {
    const id = e.currentTarget.dataset.id
    const it = this.data.items.find((x) => String(x.instanceId) === String(id))
    if (!it) return
    wx.navigateTo({ url: detailUrl(it) })
  },

  openFab() { if (this.data.isAdmin) this.setData({ sheetOpen: true }) },
  closeFab() { this.setData({ sheetOpen: false }) },
  noop() {},
  goEvent() {
    if (!this.data.isAdmin) return
    this.closeFab()
    wx.navigateTo({ url: '/pages/event/index' })
  },
  goTask() {
    if (!this.data.isAdmin) return
    this.closeFab()
    wx.navigateTo({ url: '/pages/task/index' })
  },
  goImport() {
    if (!this.data.isAdmin) return
    this.closeFab()
    wx.navigateTo({ url: '/pages/import/index' })
  },

  onPullDownRefresh() {
    this.refresh()
    wx.stopPullDownRefresh()
  }
})
