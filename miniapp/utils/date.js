// 日期工具：今日/本周视图共用的纯函数。
function pad(n) {
  return n < 10 ? '0' + n : '' + n
}

// YYYY-MM-DD（本地时区）。
function fmt(d) {
  return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate())
}

function todayStr() {
  return fmt(new Date())
}

// 本周一（周一为一周起点）。
function mondayOf(d) {
  const x = new Date(d)
  const day = (x.getDay() + 6) % 7
  x.setDate(x.getDate() - day)
  return x
}

function addDays(d, n) {
  const x = new Date(d)
  x.setDate(x.getDate() + n)
  return x
}

// 时间展示 → "HH:MM"。支持 HH:MM[:SS] 与 RFC3339 时间点（按设备本地时区展示）。
function hhmm(value) {
  const s = value || ''
  if (/^\d{2}:\d{2}/.test(s)) return s.slice(0, 5)
  const d = new Date(s)
  if (!isNaN(d.getTime())) return pad(d.getHours()) + ':' + pad(d.getMinutes())
  return ''
}

// YYYY-MM-DD → RRULE 星期代码（MO..SU）。
function weekdayCode(dateStr) {
  const codes = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA']
  return codes[new Date(dateStr + 'T00:00:00').getDay()]
}

module.exports = { pad, fmt, todayStr, mondayOf, addDays, hhmm, weekdayCode }
