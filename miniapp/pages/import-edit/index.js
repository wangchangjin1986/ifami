// 拍照导入日程 ④单条修改：对照原文片段，改事项/重复/星期多选/起止时间/孩子/责任人/地点/持续到，或删除本条。
// 对齐 prototype scr-draft-edit。数据经 app.globalData.ingestEdit 传入，保存后回写上一页 updateDraft。
const app = getApp()

const DOW = ['一', '二', '三', '四', '五', '六', '日']

Page({
  data: {
    sourceText: '',
    title: '',
    isSchool: false,
    repeat: 'weekly', // weekly | once
    dows: DOW.map((t, i) => ({ t, v: i + 1, on: false })),
    date: '',
    startTime: '09:00',
    endTime: '10:00',
    until: '',
    location: '',
    note: '',
    children: [],
    childIndex: 0,
    owners: [],
    ownerIndex: 0
  },

  onLoad() {
    const ctx = app.globalData.ingestEdit
    if (!ctx) { wx.navigateBack(); return }
    const d = ctx.draft
    const children = ctx.children || []
    const owners = [{ member_id: 0, name: '待指派' }].concat(ctx.owners || [])
    const wds = d.weekdays || []
    this.setData({
      sourceText: d.source_text || '',
      title: d.unreadable ? '' : (d.title || ''),
      isSchool: d.type === 'school',
      repeat: d.date ? 'once' : 'weekly',
      dows: DOW.map((t, i) => ({ t, v: i + 1, on: wds.indexOf(i + 1) >= 0 })),
      date: d.date || '',
      startTime: d.start_time || '09:00',
      endTime: d.end_time || '10:00',
      until: d.until || '',
      location: d.location || '',
      note: d.note || '',
      children,
      childIndex: Math.max(0, children.findIndex((c) => c.member_id === d.child_member_id)),
      owners,
      ownerIndex: Math.max(0, owners.findIndex((o) => o.member_id === d.owner_member_id))
    })
    wx.setNavigationBarTitle({ title: d.unreadable ? '手动补一条' : (d.title || '修改') })
  },

  setRepeat(e) { this.setData({ repeat: e.currentTarget.dataset.v }) },
  toggleDow(e) {
    const i = Number(e.currentTarget.dataset.i)
    this.setData({ ['dows[' + i + '].on']: !this.data.dows[i].on })
  },
  onDate(e) { this.setData({ date: e.detail.value }) },
  onStart(e) { this.setData({ startTime: e.detail.value }) },
  onEnd(e) { this.setData({ endTime: e.detail.value }) },
  onUntil(e) { this.setData({ until: e.detail.value }) },
  clearUntil() { this.setData({ until: '' }) },
  onChild(e) { this.setData({ childIndex: Number(e.detail.value) }) },
  onOwner(e) { this.setData({ ownerIndex: Number(e.detail.value) }) },

  save() {
    const d = this.data
    if (!d.title.trim()) return wx.showToast({ title: '请填写事项', icon: 'none' })
    const weekdays = d.dows.filter((x) => x.on).map((x) => x.v)
    if (d.repeat === 'weekly' && weekdays.length === 0) return wx.showToast({ title: '请至少选一个星期', icon: 'none' })
    if (d.repeat === 'once' && !d.date) return wx.showToast({ title: '请选择日期', icon: 'none' })
    if (d.endTime <= d.startTime) return wx.showToast({ title: '结束时间要晚于开始', icon: 'none' })
    const child = d.children[d.childIndex]
    if (!child) return wx.showToast({ title: '请先到「家庭」页添加孩子', icon: 'none' })
    const ctx = app.globalData.ingestEdit
    const prevType = ctx.draft.type
    const patch = {
      title: d.title.trim(),
      type: prevType === 'school' || prevType === 'pickup' ? prevType : (d.repeat === 'once' ? 'activity' : 'course'),
      weekdays: d.repeat === 'weekly' ? weekdays : [],
      date: d.repeat === 'once' ? d.date : '',
      until: d.repeat === 'weekly' ? d.until : '',
      start_time: d.startTime,
      end_time: d.endTime,
      location: d.location.trim(),
      note: d.note.trim(),
      child_member_id: child.member_id,
      owner_member_id: d.isSchool ? 0 : d.owners[d.ownerIndex].member_id
    }
    this.writeBack(patch, '已更新这一条')
  },

  remove() { this.writeBack(null, '已移除这一条') },

  writeBack(patch, msg) {
    const pages = getCurrentPages()
    const prev = pages[pages.length - 2]
    if (prev && prev.updateDraft) prev.updateDraft(prev.editingIndex, patch)
    app.globalData.ingestEdit = null
    wx.showToast({ title: msg, icon: 'none' })
    wx.navigateBack()
  },

  goBack() { wx.navigateBack() }
})
