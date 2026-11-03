// 拍照导入日程的共享工具：草稿文案、RRULE、本地冲突计算（import / import-confirm / import-edit 共用）。
const { fmt, addDays } = require('./date')

const CN = ['', '一', '二', '三', '四', '五', '六', '日']
const BYDAY = ['', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA', 'SU']

// [1..7] → 「每周一至五」「每周二/四」「每天」
function weekdaysText(wds) {
  const d = (wds || []).slice().sort((a, b) => a - b)
  if (d.length === 0) return ''
  if (d.length === 7) return '每天'
  const contiguous = d.length >= 3 && d[d.length - 1] - d[0] === d.length - 1
  if (contiguous) return '每周' + CN[d[0]] + '至' + CN[d[d.length - 1]]
  return '每周' + d.map((x) => CN[x]).join('/')
}

function dateText(s) {
  if (!s) return ''
  const dt = new Date(s + 'T00:00:00')
  return (dt.getMonth() + 1) + '月' + dt.getDate() + '日 周' + CN[((dt.getDay() + 6) % 7) + 1]
}

function whenText(d) {
  const range = d.start_time + (d.end_time ? '–' + d.end_time : '')
  return (d.date ? dateText(d.date) : weekdaysText(d.weekdays)) + ' ' + range
}

function buildRRule(d) {
  if (d.date || !d.weekdays || d.weekdays.length === 0) return ''
  const days = d.weekdays.slice().sort((a, b) => a - b).map((x) => BYDAY[x]).join(',')
  let r = 'FREQ=WEEKLY;BYDAY=' + days
  if (d.until) r += ';UNTIL=' + d.until.replace(/-/g, '')
  return r
}

// 草稿 → 未来 days 天内的发生区间 [{date, s, e}]（分钟数，便于比较）。
function occurrences(d, days) {
  const toMin = (t) => { const [h, m] = (t || '00:00').split(':').map(Number); return h * 60 + m }
  const s = toMin(d.start_time)
  const e = d.end_time ? toMin(d.end_time) : s
  const out = []
  const today = new Date()
  for (let i = 0; i < days; i++) {
    const day = addDays(today, i)
    const ds = fmt(day)
    if (d.date) {
      if (d.date === ds) out.push({ date: ds, s, e })
      continue
    }
    if (d.until && ds > d.until) break
    const wd = ((day.getDay() + 6) % 7) + 1
    if ((d.weekdays || []).indexOf(wd) >= 0) out.push({ date: ds, s, e })
  }
  return out
}

function overlap(a, b) {
  return a.date === b.date && ((a.s < b.e && b.s < a.e) || a.s === b.s)
}

// 两条日程是否冲突：同孩子，或同一非空责任人，且时间重叠（与后端规则一致，在校块只按孩子判定）。
function related(a, b) {
  if (a.child_member_id && a.child_member_id === b.child_member_id) return true
  return !!(a.owner_member_id && a.owner_member_id === b.owner_member_id)
}

module.exports = { weekdaysText, dateText, whenText, buildRRule, occurrences, overlap, related }
