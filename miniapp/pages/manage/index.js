// 全部安排：日程 + 打卡任务按孩子分组，对齐 prototype scr-manage。
const app = getApp()
const { hhmm } = require('../../utils/date')

const TYPE_EMOJI = { course: '📚', pickup: '🚗', activity: '🎈', reminder: '⏰' }
const TASK_EMOJI = { timer: '⏱', count: '🔢', photo: '📷' }
const FREQ_LABEL = { daily: '每天', weekdays: '工作日', custom: '自定义' }
const BYDAY_CN = { MO: '每周一', TU: '每周二', WE: '每周三', TH: '每周四', FR: '每周五', SA: '每周六', SU: '每周日' }

function repeatLabel(rrule) {
  if (!rrule) return '单次'
  if (rrule.indexOf('FREQ=DAILY') >= 0) return '每天'
  const m = rrule.match(/BYDAY=([A-Z]{2})/)
  if (m) return BYDAY_CN[m[1]] || '每周'
  return '重复'
}

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
          nameMap[m.member_id] = m.name || '成员' + m.member_id
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
            title: (TYPE_EMOJI[s.type] || '📅') + ' ' + s.title + ' · ' + repeatLabel(s.rrule) + ' ' + hhmm(s.start_time),
            meta: [s.location, who].filter(Boolean).join(' · ')
          })
        })
        ;(tasks || []).forEach((t) => {
          add(t.child_member_id, {
            id: 't' + t.task_id,
            isTask: true,
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
    wx.showToast({ title: (v === 'org' ? '按机构' : '已暂停') + '视图开发中', icon: 'none' })
  },

  goBack() { wx.navigateBack() }
})
