// 日程实例 → 详情页导航 URL：把实例字段透传给 pages/event-detail，由详情页按需展示/操作。
function repeatLabel(rrule) {
  if (!rrule) return '单次'
  if (rrule.indexOf('BYDAY=MO,TU,WE,TH,FR,SA,SU') >= 0) return '每天'
  const names = { MO: '周一', TU: '周二', WE: '周三', TH: '周四', FR: '周五', SA: '周六', SU: '周日' }
  const m = rrule.match(/BYDAY=([A-Z,]+)/)
  if (m) return '每' + m[1].split(',').map((d) => names[d] || d).join('、')
  return '重复'
}

function detailUrl(it) {
  const q = []
  const put = (k, v) => { if (v !== undefined && v !== null) q.push(k + '=' + encodeURIComponent(v)) }
  put('instance_id', it.instanceId)
  put('schedule_id', it.scheduleId)
  put('title', it.title)
  put('type', it.type)
  put('occur_date', it.occurDate)
  put('start_at', it.startAt)
  put('end_at', it.endAt)
  put('rrule', it.rrule)
  put('repeat_label', it.repeatLabel || repeatLabel(it.rrule))
  put('location', it.location)
  put('note', it.note)
  put('child_member_id', it.childMemberId)
  put('owner_member_id', it.ownerMemberId)
  return '/pages/event-detail/index?' + q.join('&')
}

module.exports = { detailUrl, repeatLabel }
