// 日历页：周视图（时段 × 星期网格，格子=责任人）+ 月视图（月历绿点/橙点）+ 按孩子筛选。
// 对齐 prototype scr-week + 需求 R2.4（周视图支持按孩子筛选）。
const app = getApp()
const { fmt, mondayOf, addDays, hhmm, todayStr } = require('../../utils/date')
const { detailUrl } = require('../../utils/nav')

const WEEK_CN = ['一', '二', '三', '四', '五', '六', '日'] // 周一起始

// 时间网格：纵轴每 2 小时一段（全天 00–24 共 12 段），横轴周一~周日。
const SLOT_HOURS = 2
const SLOT_COUNT = 12

function slotIndex(hh) {
  return Math.floor(parseInt(hh.slice(0, 2), 10) / SLOT_HOURS)
}

function slotLabel(i) {
  const pad = (n) => String(n).padStart(2, '0')
  return pad(i * SLOT_HOURS) + '-' + pad((i + 1) * SLOT_HOURS)
}

// 两实例时间是否重叠（HH:MM 字符串比较，固定宽度=时间序）。
function overlap(aStart, aEnd, bStart, bEnd) {
  return aStart < bEnd && bStart < aEnd
}

Page({
  data: {
    logged: false,
    hasFamily: false,
    mode: 'week',
    monthLabel: '',
    weekDows: WEEK_CN,
    weekSlots: [],
    monthBlanks: [],
    monthCells: [],
    children: [],
    childFilter: 0,
    filterOpen: false,
    childLabel: '全部孩子',
    sheetOpen: false
  },

  onShow() {
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 1 })
    }
    this.refresh()
  },

  refresh() {
    if (!app.globalData.token) {
      this.setData({ logged: false, hasFamily: false, weekSlots: [], monthCells: [] })
      return
    }
    const fid = app.globalData.familyId
    this.setData({ logged: true })
    if (!fid) {
      this.setData({ hasFamily: false, weekSlots: [], monthCells: [] })
      return
    }
    this.setData({ hasFamily: true })

    const now = new Date()
    const mon = mondayOf(now)
    this._now = now
    this._mon = mon
    this.setData({ monthLabel: now.getFullYear() + '年' + (now.getMonth() + 1) + '月' })

    const weekStart = fmt(mon)
    const weekEnd = fmt(addDays(mon, 6))
    const monthStart = fmt(new Date(now.getFullYear(), now.getMonth(), 1))
    const monthEnd = fmt(new Date(now.getFullYear(), now.getMonth() + 1, 0))

    Promise.all([
      app.request(`/v1/schedules/instances?start=${weekStart}&end=${weekEnd}`),
      app.request(`/v1/schedules/instances?start=${monthStart}&end=${monthEnd}`),
      app.request(`/v1/families/${fid}/members`)
    ])
      .then(([weekInst, monthInst, members]) => {
        const nameMap = {}
        const children = []
        ;(members || []).forEach((m) => {
          nameMap[m.member_id] = m.name || m.relation_label || '成员' + m.member_id
          if (m.type === 'virtual') children.push({ member_id: m.member_id, name: m.name || '孩子' })
        })
        this._weekInst = weekInst
        this._monthInst = monthInst
        this._nameMap = nameMap
        this.setData({ children })
        // 选中的孩子已不存在则回落到「全部」
        let filter = this.data.childFilter
        if (filter !== 0 && !children.some((c) => c.member_id === filter)) filter = 0
        this.applyFilter(filter)
      })
      .catch((e) => {
        if (e.code === 4001) { app.logout(); this.setData({ logged: false, hasFamily: false }) }
        else wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  // 用当前筛选孩子重建周/月视图（本地过滤，不重新请求）。
  applyFilter(childFilter) {
    const label = childFilter === 0 ? '全部孩子' : (this._nameMap[childFilter] || '孩子')
    this.setData({ childFilter, childLabel: label })
    this.buildWeek(this._weekInst, this._nameMap, this._mon, childFilter)
    this.buildMonth(this._monthInst, this._now, childFilter)
  },

  toggleFilter() {
    this.setData({ filterOpen: !this.data.filterOpen })
  },

  selectChild(e) {
    const id = Number(e.currentTarget.dataset.id)
    this.applyFilter(id)
    this.setData({ filterOpen: false })
  },

  // 周视图：按时段桶 × 周一到周日分组，格子显示「责任人 + 事项」，冲突标橙。
  buildWeek(instances, nameMap, mon, childFilter) {
    const items = (instances || [])
      .filter((i) => i.status !== 'cancelled' && (childFilter === 0 || i.child_member_id === childFilter))
      .map((i) => ({
        instanceId: i.instance_id,
        scheduleId: i.schedule_id,
        type: i.type,
        occurDate: i.occur_date,
        startAt: i.start_at,
        endAt: i.end_at,
        location: i.location,
        note: i.note,
        childMemberId: i.child_member_id,
        ownerMemberId: i.owner_member_id,
        owner: nameMap[i.owner_member_id] || '—',
        ownerId: i.owner_member_id,
        childId: i.child_member_id,
        childName: nameMap[i.child_member_id] || '',
        title: i.title,
        start: hhmm(i.start_at),
        end: i.end_at ? hhmm(i.end_at) : hhmm(i.start_at),
        occur: i.occur_date,
        slot: slotIndex(hhmm(i.start_at)),
        conflict: false
      }))
    // 同孩子或同责任人时间重叠 → 冲突（对齐后端 FindConflicts 语义）
    for (let i = 0; i < items.length; i++) {
      for (let j = i + 1; j < items.length; j++) {
        const a = items[i]; const b = items[j]
        const related = a.childId === b.childId || (a.ownerId && a.ownerId === b.ownerId)
        if (related && overlap(a.start, a.end, b.start, b.end)) { a.conflict = true; b.conflict = true }
      }
    }
    const weekSlots = Array.from({ length: SLOT_COUNT }, (_, i) => {
      const days = []
      for (let d = 0; d < 7; d++) {
        const date = fmt(addDays(mon, d))
        const list = items.filter((x) => x.occur === date && x.slot === i)
          .sort((a, b) => (a.start < b.start ? -1 : 1))
        days.push({ date, items: list, hot: list.some((x) => x.conflict) })
      }
      return { label: slotLabel(i), days }
    }).filter((s) => s.days.some((d) => d.items.length > 0))

    this.setData({ weekSlots })
  },

  // 月视图：月历，绿点=当天有日程，橙点=当天有冲突。
  buildMonth(instances, now, childFilter) {
    const year = now.getFullYear(); const month = now.getMonth()
    const first = new Date(year, month, 1)
    const blanks = (first.getDay() + 6) % 7
    const daysInMonth = new Date(year, month + 1, 0).getDate()
    const byDay = {}
    ;(instances || [])
      .filter((i) => i.status !== 'cancelled' && (childFilter === 0 || i.child_member_id === childFilter))
      .forEach((i) => {
        ;(byDay[i.occur_date] = byDay[i.occur_date] || []).push(i)
      })
    const today = todayStr()
    const cells = []
    for (let d = 1; d <= daysInMonth; d++) {
      const date = fmt(new Date(year, month, d))
      const list = byDay[date] || []
      let conflict = false
      for (let i = 0; i < list.length; i++) {
        for (let j = i + 1; j < list.length; j++) {
          const a = list[i]; const b = list[j]
          const related = a.child_member_id === b.child_member_id ||
            (a.owner_member_id && a.owner_member_id === b.owner_member_id)
          const as = hhmm(a.start_at); const ae = a.end_at ? hhmm(a.end_at) : as
          const bs = hhmm(b.start_at); const be = b.end_at ? hhmm(b.end_at) : bs
          if (related && overlap(as, ae, bs, be)) conflict = true
        }
      }
      cells.push({ day: d, date, today: date === today, hasSchedule: list.length > 0, conflict })
    }
    this.setData({ monthBlanks: Array.from({ length: blanks }, (_, i) => i), monthCells: cells })
  },

  setMode(e) {
    this.setData({ mode: e.currentTarget.dataset.v })
  },

  // 点击格子 → 单项直达详情；多项先弹选择再进详情。
  tapCell(e) {
    const slot = e.currentTarget.dataset.slot
    const date = e.currentTarget.dataset.date
    const sl = this.data.weekSlots.find((s) => s.label === slot)
    if (!sl) return
    const day = sl.days.find((d) => d.date === date)
    const items = (day && day.items) || []
    if (items.length === 0) return
    if (items.length === 1) return wx.navigateTo({ url: detailUrl(items[0]) })
    wx.showActionSheet({
      itemList: items.map((x) => x.childName + ' · ' + x.title),
      success: (r) => wx.navigateTo({ url: detailUrl(items[r.tapIndex]) })
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
