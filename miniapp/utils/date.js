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

// RFC3339 时间点 → "HH:MM"。后端把用户时钟按字面值存为 UTC，
// 取 [11,16) 即用户输入的原时钟值，展示往返一致。
function hhmm(rfc3339) {
  return (rfc3339 || '').slice(11, 16)
}

// YYYY-MM-DD → RRULE 星期代码（MO..SU）。
function weekdayCode(dateStr) {
  const codes = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA']
  return codes[new Date(dateStr + 'T00:00:00').getDay()]
}

module.exports = { pad, fmt, todayStr, mondayOf, addDays, hhmm, weekdayCode }
