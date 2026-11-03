// 拍照导入日程 ③草稿确认屏：按图分组、低置信标黄、冲突就地标出、批量指派责任人、一次确认入库。
// 对齐 prototype scr-scan-confirm / sheet-bulk / scr-import-done。单条修改走 pages/import-edit（回写本页 updateDraft）。
const app = getApp()
const { fmt, addDays, hhmm } = require('../../utils/date')
const ig = require('../../utils/ingest')

const CONFLICT_DAYS = 14

Page({
  data: {
    loading: true,
    done: false,
    groups: [], // [{seq, title, meta, rows:[...]}]
    selectedCount: 0,
    unassigned: 0,
    lowCount: 0,
    conflictCount: 0,
    bulkOpen: false,
    bulkRows: [],
    submitting: false,
    result: null
  },

  onLoad(options) {
    this.jobId = options.job_id
    this.drafts = []
    this.existing = []
    app.ensureLogin()
      .then(() => {
        const fid = app.globalData.familyId
        const start = fmt(new Date())
        const end = fmt(addDays(new Date(), CONFLICT_DAYS))
        return Promise.all([
          app.request(`/v1/ingest/jobs/${this.jobId}`),
          app.request(`/v1/families/${fid}/members`),
          app.request(`/v1/schedules/instances?start=${start}&end=${end}`)
        ])
      })
      .then(([job, members, instances]) => {
        if (job.status === 'confirmed') {
          this.setData({ loading: false, done: true, result: { total: 0, already: true } })
          return
        }
        if (job.status !== 'done') {
          wx.showToast({ title: job.status === 'failed' ? '这次没识别出来' : '还在识别，请稍后', icon: 'none' })
          setTimeout(() => wx.navigateBack(), 800)
          return
        }
        this.children = (members || []).filter((m) => m.type === 'virtual')
          .map((m) => ({ member_id: m.member_id, name: m.name || '孩子' + m.member_id }))
        this.owners = (members || []).filter((m) => m.type !== 'virtual')
          .map((m) => ({ member_id: m.member_id, name: m.name || m.relation_label || '成员' + m.member_id }))
        this.existing = (instances || []).filter((i) => i.status !== 'cancelled').map((i) => ({
          title: i.title, child_member_id: i.child_member_id, owner_member_id: i.owner_member_id,
          occ: [{ date: i.occur_date, s: toMin(hhmm(i.start_at)), e: toMin(i.end_at ? hhmm(i.end_at) : hhmm(i.start_at)) }]
        }))
        this.imageKinds = {}
        ;(job.images || []).forEach((im) => { this.imageKinds[im.seq] = im.image_kind })
        this.drafts = (job.drafts || []).map((d, idx) => Object.assign({}, d, {
          key: 'd' + idx,
          selected: !d.unreadable,
          owner_member_id: 0,
          weekdays: d.weekdays || []
        }))
        this.rebuild()
        this.setData({ loading: false })
      })
      .catch((e) => {
        this.setData({ loading: false })
        if (e.code !== 4001) wx.showToast({ title: e.message || '加载失败', icon: 'none' })
      })
  },

  // ---- 视图派生 ----

  rebuild() {
    const childName = (id) => { const c = (this.children || []).find((x) => x.member_id === id); return c ? c.name : '' }
    const ownerName = (id) => { const o = (this.owners || []).find((x) => x.member_id === id); return o ? o.name : '' }
    const conflicts = this.computeConflicts()
    const groups = []
    const bySeq = {}
    let selectedCount = 0; let unassigned = 0; let lowCount = 0
    this.drafts.forEach((d, i) => {
      if (!bySeq[d.image_seq]) {
        bySeq[d.image_seq] = { seq: d.image_seq, title: '第 ' + (d.image_seq + 1) + ' 张图', kids: {}, rows: [] }
        groups.push(bySeq[d.image_seq])
      }
      const g = bySeq[d.image_seq]
      if (d.child_member_id) g.kids[childName(d.child_member_id)] = true
      const needOwner = d.type !== 'school'
      if (d.selected) {
        selectedCount++
        if (needOwner && !d.owner_member_id) unassigned++
      }
      if (d.low_confidence || d.unreadable) lowCount++
      const chips = []
      if (d.type === 'school') chips.push({ t: '每周重复', c: '' }, { t: '不需要责任人', c: 'gray' })
      else {
        chips.push(d.date ? { t: '单次', c: 'gray' } : { t: d.type === 'pickup' ? '接送' : '每周重复', c: '' })
        chips.push(d.owner_member_id ? { t: '责任人：' + ownerName(d.owner_member_id), c: '' } : { t: '责任人：待指派', c: 'gray' })
      }
      if (!d.child_member_id && !d.unreadable) chips.push({ t: '待选孩子', c: 'warn' })
      if (d.selected && conflicts[i]) chips.push({ t: '⚠ ' + conflicts[i], c: 'warn' })
      g.rows.push({
        i,
        key: d.key,
        selected: d.selected,
        low: d.low_confidence || d.unreadable,
        unreadable: d.unreadable,
        l1: d.unreadable ? '一行没看清' : d.title + ' · ' + ig.whenText(d),
        l1Tag: d.unreadable ? '需手动补' : (d.low_confidence ? '需要看一眼' : ''),
        l2: d.unreadable ? '原文“' + (d.source_text || '') + '”'
          : [d.location, childName(d.child_member_id), d.note, d.low_confidence && d.source_text ? '原文“' + d.source_text + '”' : '']
            .filter(Boolean).join(' · '),
        canExpand: d.type === 'school' && (d.merged_items || []).length > 0,
        mergedCount: (d.merged_items || []).length,
        chips
      })
    })
    groups.forEach((g) => {
      const kind = { course_table: '机构课表', school_timetable: '学校作息，已合并为整块', notice: '通知截图' }[this.imageKinds[g.seq]] || ''
      const kids = Object.keys(g.kids).filter(Boolean).join('、')
      g.meta = [kids, g.rows.length + ' 项', kind].filter(Boolean).join(' · ')
    })
    const conflictCount = Object.keys(conflicts).length
    this.setData({ groups, selectedCount, unassigned, lowCount, conflictCount })
  },

  // 本地冲突：选中草稿 × 未来 14 天已有实例、选中草稿两两（规则同后端）。返回 {index: 文案}。
  computeConflicts() {
    const out = {}
    const sel = []
    this.drafts.forEach((d, i) => {
      if (d.selected && !d.unreadable) sel.push({ i, d, occ: ig.occurrences(d, CONFLICT_DAYS) })
    })
    sel.forEach((a) => {
      this.existing.forEach((x) => {
        if (!out[a.i] && ig.related(a.d, x) && a.occ.some((o) => x.occ.some((p) => ig.overlap(o, p)))) out[a.i] = '与已有「' + x.title + '」时间重叠'
      })
    })
    for (let m = 0; m < sel.length; m++) {
      for (let n = m + 1; n < sel.length; n++) {
        const a = sel[m]; const b = sel[n]
        if (!ig.related(a.d, b.d)) continue
        if (a.occ.some((o) => b.occ.some((p) => ig.overlap(o, p)))) {
          if (!out[b.i]) out[b.i] = '与「' + a.d.title + '」时间重叠'
          if (!out[a.i]) out[a.i] = '与「' + b.d.title + '」时间重叠'
        }
      }
    }
    return out
  },

  // ---- 交互 ----

  toggle(e) {
    const i = Number(e.currentTarget.dataset.i)
    const d = this.drafts[i]
    if (d.unreadable && !d.selected) return this.edit(e)
    d.selected = !d.selected
    this.rebuild()
  },

  edit(e) {
    const i = Number(e.currentTarget.dataset.i)
    this.editingIndex = i
    app.globalData.ingestEdit = {
      draft: this.drafts[i],
      children: this.children,
      owners: this.owners
    }
    wx.navigateTo({ url: '/pages/import-edit/index' })
  },

  // import-edit 回写：draft=null 表示删除这一条。
  updateDraft(i, draft) {
    if (draft === null) this.drafts.splice(i, 1)
    else this.drafts[i] = Object.assign({}, this.drafts[i], draft, { unreadable: false, low_confidence: false, selected: true })
    this.rebuild()
  },

  // 在校块展开逐节：逐节仍为在校类型（无责任人），保留接放学草稿。
  expand(e) {
    const i = Number(e.currentTarget.dataset.i)
    const block = this.drafts[i]
    const lessons = (block.merged_items || []).map((l, k) => Object.assign({}, l, {
      key: block.key + '-' + k, type: 'school', child_member_id: block.child_member_id,
      selected: true, owner_member_id: 0, weekdays: l.weekdays || []
    }))
    this.drafts.splice(i, 1, ...lessons)
    this.rebuild()
  },

  openBulk() {
    this.buildBulk()
    this.setData({ bulkOpen: true })
  },

  closeBulk() {
    this.setData({ bulkOpen: false })
    this.rebuild()
  },

  noop() {},

  // 批量指派：列出选中且需要责任人的草稿；某成员同时段已有安排则置灰。
  buildBulk() {
    const rows = []
    this.drafts.forEach((d, i) => {
      if (!d.selected || d.unreadable || d.type === 'school') return
      const occ = ig.occurrences(d, CONFLICT_DAYS)
      const opts = (this.owners || []).map((o) => {
        const busyWith = this.ownerBusy(i, o.member_id, occ)
        return { member_id: o.member_id, name: o.name, on: d.owner_member_id === o.member_id, busy: !!busyWith, busyWith: busyWith || '' }
      })
      rows.push({ i, label: d.title + ' · ' + ig.whenText(d), opts })
    })
    this.setData({ bulkRows: rows })
  },

  ownerBusy(i, ownerId, occ) {
    for (let k = 0; k < this.drafts.length; k++) {
      const x = this.drafts[k]
      if (k === i || !x.selected || x.owner_member_id !== ownerId) continue
      const xo = ig.occurrences(x, CONFLICT_DAYS)
      if (occ.some((o) => xo.some((p) => ig.overlap(o, p)))) return x.title
    }
    for (const x of this.existing) {
      if (x.owner_member_id === ownerId && occ.some((o) => x.occ.some((p) => ig.overlap(o, p)))) return x.title
    }
    return ''
  },

  pickOwner(e) {
    const { i, id, busy, with: busyWith } = e.currentTarget.dataset
    if (busy) {
      wx.showToast({ title: '同一时间要负责「' + busyWith + '」', icon: 'none' })
      return
    }
    this.drafts[Number(i)].owner_member_id = Number(id)
    this.buildBulk()
  },

  submit(force) {
    if (this.data.submitting) return
    const picked = this.drafts.filter((d) => d.selected)
    if (picked.length === 0) return wx.showToast({ title: '请至少保留一项', icon: 'none' })
    if (picked.some((d) => d.unreadable)) return wx.showToast({ title: '有没看清的行，请先补全或取消勾选', icon: 'none' })
    if (picked.some((d) => !d.child_member_id)) return wx.showToast({ title: '有条目还没选孩子，点「改」补上', icon: 'none' })
    if (picked.some((d) => d.type !== 'school' && !d.owner_member_id)) {
      wx.showToast({ title: '还有安排没指派责任人', icon: 'none' })
      return this.openBulk()
    }
    const today = fmt(new Date())
    const schedules = picked.map((d) => ({
      title: d.title,
      type: d.type,
      child_member_id: d.child_member_id,
      owner_member_id: d.type === 'school' ? 0 : d.owner_member_id,
      start_date: d.date || today,
      rrule: ig.buildRRule(d),
      start_time: d.start_time,
      end_time: d.end_time || '',
      location: d.location || '',
      note: d.note || ''
    }))
    this.setData({ submitting: true })
    app.request(`/v1/ingest/jobs/${this.jobId}/confirm`, { method: 'POST', data: { schedules, force: force === true } })
      .then((r) => {
        const ids = (r && r.schedule_ids) || []
        const weekly = picked.filter((d) => !d.date).length
        const owners = {}
        picked.forEach((d) => { if (d.owner_member_id) owners[d.owner_member_id] = true })
        this.setData({
          submitting: false, done: true,
          result: { total: ids.length, weekly, single: picked.length - weekly, owners: Object.keys(owners).length }
        })
      })
      .catch((e) => {
        this.setData({ submitting: false })
        if (e.code === 2005) {
          const list = (e.data && e.data.conflicts) || []
          const lines = list.slice(0, 3).map((c) => {
            const a = picked[c.index] ? picked[c.index].title : ''
            const b = c.instance ? c.instance.title : (picked[c.other_index] ? picked[c.other_index].title : '')
            return a + ' × ' + b
          })
          wx.showModal({
            title: '存在时间冲突',
            content: lines.join('\n') + (list.length > 3 ? '\n…' : '') + '\n\n有些冲突是真实的（如两人分头接送），仍要加入吗？',
            confirmText: '仍然加入',
            success: (m) => { if (m.confirm) this.submit(true) }
          })
        } else {
          wx.showToast({ title: e.message || '加入失败', icon: 'none' })
        }
      })
  },

  onSubmit() { this.submit(false) },

  viewWeek() { wx.switchTab({ url: '/pages/week/index' }) },
  importMore() { wx.redirectTo({ url: '/pages/import/index' }) },
  goBack() { wx.navigateBack() }
})

function toMin(t) {
  const [h, m] = (t || '00:00').split(':').map(Number)
  return h * 60 + m
}
