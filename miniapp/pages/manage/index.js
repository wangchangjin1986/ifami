// 全部安排：日程 + 打卡任务按孩子分组，对齐 prototype scr-manage。
const app = getApp()
const { hhmm } = require('../../utils/date')
const { repeatLabel: scheduleRepeatLabel } = require('../../utils/nav')

const TYPE_EMOJI = { course: '📚', pickup: '🚗', activity: '🎈', reminder: '⏰' }
const TASK_EMOJI = { timer: '⏱', count: '🔢', photo: '📷' }
const FREQ_LABEL = { daily: '每天', weekdays: '工作日', custom: '自定义' }

function goalSuffix(t) {
  if (t.goal_type === 'timer') return t.goal_value + ' 分钟'
  if (t.goal_type === 'count') return t.goal_value + ' 个'
  if (t.goal_type === 'photo') return '拍照'
  return ''
}

Page({
  data: {
    loading: true,
    groups: []
  },

  onLoad() {
    if (!app.globalData.isAdmin) {
      wx.showToast({ title: '只有管理员可以管理全部安排', icon: 'none' })
      setTimeout(() => wx.navigateBack(), 500)
      return
    }
    this.refresh()
  },

  refresh() {
    this.setData({ loading: true })
    app.ensureLogin()
      .then(() => {
        const fid = app.globalData.familyId
        if (!fid) return
        return Promise.all([
          app.request('/v1/schedules'),
          app.request('/v1/tasks'),
          app.request(`/v1/families/${fid}/members`)
        ])
      })
      .then(([schedules, tasks, members]) => {
        const nameMap = {}
        const children = []
        ;(members || []).forEach((m) => {
          nameMap[m.member_id] = m.name || m.relation_label || '成员' + m.member_id
          if (m.type === 'virtual') children.push(m)
        })

        const byChild = {}
        const add = (cid, item) => { (byChild[cid] = byChild[cid] || []).push(item) }

        ;(schedules || []).forEach((s) => {
          const owner = nameMap[s.owner_member_id] || '—'
          const who = s.type === 'pickup' ? '接送：' + owner : '责任：' + owner
          add(s.child_member_id, {
            id: 's' + s.schedule_id,
            isTask: false,
            schedule: s,
            title: (TYPE_EMOJI[s.type] || '📅') + ' ' + s.title + ' · ' + scheduleRepeatLabel(s.rrule) + ' ' + hhmm(s.start_time),
            meta: [s.location, who].filter(Boolean).join(' · ')
          })
        })
        ;(tasks || []).forEach((t) => {
          add(t.child_member_id, {
            id: 't' + t.task_id,
            isTask: true,
            task: t,
            title: (TASK_EMOJI[t.goal_type] || '📷') + ' ' + t.name + ' · ' + (FREQ_LABEL[t.freq] || t.freq) + ' ' + goalSuffix(t),
            meta: '责任：' + (nameMap[t.owner_member_id] || '—') + ' · 连续 ' + (t.streak_days || 0) + ' 天'
          })
        })

        const groups = []
        children.forEach((c) => {
          if (byChild[c.member_id]) groups.push({ childId: c.member_id, childName: c.name, items: byChild[c.member_id] })
        })
        if (byChild[0]) groups.push({ childId: 0, childName: '未分配', items: byChild[0] })

        this.setData({ loading: false, groups })
      })
      .catch((e) => {
        this.setData({ loading: false })
        if (e.code === 4001) { app.logout(); wx.navigateBack() }
        else wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  filter(e) {
    const v = e.currentTarget.dataset.v
    if (v === 'child') return
    wx.showToast({ title: (v === 'org' ? '按机构' : '已归档') + '视图开发中', icon: 'none' })
  },

  openItem(e) {
    const it = e.currentTarget.dataset.item
    if (!it) return
    if (!it.isTask) {
      const s = it.schedule || {}
      // 全部安排读取的是模板；直接进入服务端授权的模板编辑，避免伪造“某次实例”详情。
      wx.navigateTo({ url: '/pages/event/index?edit=1&schedule_id=' + s.schedule_id })
      return
    }
    const t = it.task || {}
    wx.navigateTo({
      url: '/pages/task-detail/index?task_id=' + t.task_id +
        '&name=' + encodeURIComponent(t.name || '') +
        '&title=' + encodeURIComponent(it.title || '') +
        '&goal_type=' + (t.goal_type || '') +
        '&goal_value=' + (t.goal_value || 0) +
        '&freq=' + (t.freq || 'daily') +
        '&weekly_target=' + (t.weekly_target || 0) +
        '&child_member_id=' + (t.child_member_id || 0) +
        '&owner_member_id=' + (t.owner_member_id || 0) +
        '&remind_time=' + encodeURIComponent(t.remind_time || '')
    })
  },

  goBack() { wx.navigateBack() }
})
