const app = getApp()
const { todayStr, addDays, fmt, hhmm } = require('../../utils/date')

const WEEK_CN = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

function dateLabel(d) {
  return (d.getMonth() + 1) + '月' + d.getDate() + '日 ' + WEEK_CN[d.getDay()]
}

function nameOf(m) {
  return m.name || m.relation_label || ('成员' + m.member_id)
}

function pickContact(members) {
  const admins = (members || []).filter((m) => m.type === 'real' && (m.is_admin || m.role === 'admin'))
  const parents = (members || []).filter((m) => m.type === 'real' && m.role === 'parent')
  return admins.find((m) => m.phone) || parents.find((m) => m.phone) || admins[0] || parents[0] || null
}

Page({
  data: {
    logged: false,
    hasFamily: false,
    dateStr: '',
    notifyReddots: [],
    hasReddot: false,
    items: [],
    empty: true,
    tomorrowText: '',
    contactName: '',
    contactPhone: '',
    callText: '📞 联系家长',
    feedbackOpen: false,
    feedbackTitle: '',
    feedbackReasons: [
      { value: '今天去不了', label: '今天去不了', desc: '我这次没法接送，请家长重新安排' },
      { value: '时间不对', label: '时间不对', desc: '看到的时间和实际不一致' },
      { value: '地点/携带物不清楚', label: '地点/携带物不清楚', desc: '需要家长补充说明' },
      { value: '其他，电话说', label: '其他，电话说', desc: '保留人工兜底' }
    ]
  },

  onShow() {
    this.refresh()
  },

  refresh() {
    const now = new Date()
    this.setData({ dateStr: dateLabel(now) })
    if (!app.globalData.token || !app.globalData.user) {
      this.setData({ logged: false, hasFamily: false, notifyReddots: [], hasReddot: false, items: [], empty: true, tomorrowText: '', contactName: '', contactPhone: '', callText: '📞 联系家长' })
      return
    }
    app.ensureLogin()
      .then(() => this.loadAggregate())
      .catch((e) => {
        if (e.code === 4001) {
          app.logout()
          this.setData({ logged: false, hasFamily: false, notifyReddots: [], hasReddot: false, items: [], empty: true, tomorrowText: '', contactName: '', contactPhone: '', callText: '📞 联系家长' })
          return
        }
        wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  loadAggregate() {
    const memberships = (app.globalData.memberships || []).filter((m) => m.role === 'grandparent' && !m.is_admin)
    if (memberships.length === 0) {
      wx.reLaunch({ url: '/pages/today/index' })
      return Promise.resolve()
    }
    const familyIds = memberships.map((m) => m.family_id)
    const today = todayStr()
    const tomorrow = fmt(addDays(new Date(), 1))
    return app.request('/v1/families')
      .then((families) => {
        const familyName = {}
        ;(families || []).forEach((f) => { familyName[f.family_id] = f.name || '家庭' })
        return Promise.all(familyIds.map((fid) => Promise.all([
          app.request(`/v1/families/${fid}/members`, { familyId: fid }),
          app.request(`/v1/schedules/instances?start=${today}&end=${today}`, { familyId: fid }),
          app.request(`/v1/schedules/instances?start=${tomorrow}&end=${tomorrow}`, { familyId: fid }),
          app.request('/v1/notify/reddots', { familyId: fid }).catch(() => [])
        ]).then(([members, todayRows, tomorrowRows, reddots]) => ({ fid, familyName: familyName[fid] || '家庭', members, todayRows, tomorrowRows, reddots }))))
      })
      .then((groups) => {
        const items = []
        const tomorrowItems = []
        const notifyReddots = []
        let contact = null
        groups.forEach((g) => {
          const names = {}
          ;(g.members || []).forEach((m) => { names[m.member_id] = nameOf(m) })
          if (!contact) contact = pickContact(g.members || [])
          ;(g.todayRows || []).forEach((it) => {
            if (it.status === 'cancelled') return
            items.push({
              familyId: g.fid,
              familyName: g.familyName,
              instanceId: it.instance_id,
              start: hhmm(it.start_at),
              startAt: it.start_at,
              title: it.title,
              status: it.status,
              childName: names[it.child_member_id] || '孩子',
              meta: [it.location ? '📍 ' + it.location : '', it.note ? '🎒 带：' + it.note : ''].filter(Boolean).join('\n')
            })
          })
          ;(g.tomorrowRows || []).forEach((it) => {
            if (it.status === 'cancelled') return
            tomorrowItems.push({ familyName: g.familyName, start: hhmm(it.start_at), title: it.title, startAt: it.start_at })
          })
          ;(g.reddots || []).forEach((job) => {
            notifyReddots.push(Object.assign({}, job, { familyId: g.fid, familyName: g.familyName }))
          })
        })
        items.sort((a, b) => a.startAt < b.startAt ? -1 : 1)
        tomorrowItems.sort((a, b) => a.startAt < b.startAt ? -1 : 1)
        const firstTomorrow = tomorrowItems[0]
        const contactName = contact ? nameOf(contact) : ''
        const contactPhone = contact && contact.phone ? contact.phone : ''
        this.setData({
          logged: true,
          hasFamily: true,
          notifyReddots,
          hasReddot: notifyReddots.length > 0,
          items,
          empty: items.length === 0,
          tomorrowText: firstTomorrow ? `明天 · ${firstTomorrow.familyName} ${firstTomorrow.start} ${firstTomorrow.title}` : '',
          contactName,
          contactPhone,
          callText: contactName ? `📞 联系${contactName}` : '📞 联系家长'
        })
      })
  },

  tapReddot(e) {
    const id = e.currentTarget.dataset.id
    const familyId = e.currentTarget.dataset.familyId
    const job = (this.data.notifyReddots || []).find((x) => String(x.job_id) === String(id) && String(x.familyId) === String(familyId))
    if (!job) return
    app.reportNotifyReceipt(job.job_id)
      .then(() => this.loadAggregate())
    const page = job.payload && job.payload.page
    app.openNotifyPage(page)
  },

  completeItem(e) {
    const id = e.currentTarget.dataset.id
    const familyId = e.currentTarget.dataset.familyId
    if (!id || !familyId) return
    app.request(`/v1/schedules/instances/${id}/complete`, { method: 'POST', familyId })
      .then(() => {
        const items = this.data.items.map((it) => String(it.instanceId) === String(id) && String(it.familyId) === String(familyId) ? Object.assign({}, it, { status: 'done' }) : it)
        this.setData({ items })
        wx.showToast({ title: '已完成', icon: 'success' })
      })
      .catch((e) => wx.showToast({ title: e.message || '提交失败', icon: 'none' }))
  },

  openFeedback(e) {
    this._feedback = {
      instanceId: e.currentTarget.dataset.id,
      familyId: e.currentTarget.dataset.familyId
    }
    this.setData({ feedbackOpen: true, feedbackTitle: e.currentTarget.dataset.title || '要调整' })
  },

  closeFeedback() { this.setData({ feedbackOpen: false }) },
  noop() {},

  submitFeedback(e) {
    const f = this._feedback || {}
    const reason = e.currentTarget.dataset.reason
    if (!f.instanceId || !f.familyId || !reason) return
    app.request(`/v1/schedules/instances/${f.instanceId}/feedback`, { method: 'POST', familyId: f.familyId, data: { reason } })
      .then(() => {
        this.setData({ feedbackOpen: false })
        wx.showToast({ title: '已通知家长处理', icon: 'none' })
      })
      .catch((err) => wx.showToast({ title: err.message || '提交失败', icon: 'none' }))
  },

  callBackup() {
    const phone = this.data.contactPhone
    if (!phone) {
      wx.showToast({ title: '家长未设置电话，请用微信联系', icon: 'none' })
      return
    }
    wx.makePhoneCall({
      phoneNumber: phone,
      fail: () => wx.showToast({ title: '拨号取消或失败', icon: 'none' })
    })
  },

  goSettings() { wx.navigateTo({ url: '/pages/settings/index' }) },

  onPullDownRefresh() {
    this.refresh()
    wx.stopPullDownRefresh()
  }
})
