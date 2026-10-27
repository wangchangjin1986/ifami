// 日程实例 → 详情页导航 URL：把实例字段透传给 pages/event-detail，由详情页按需展示/操作。
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
  put('location', it.location)
  put('note', it.note)
  put('child_member_id', it.childMemberId)
  put('owner_member_id', it.ownerMemberId)
  return '/pages/event-detail/index?' + q.join('&')
}

module.exports = { detailUrl }
