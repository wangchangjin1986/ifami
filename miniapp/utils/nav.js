// 日程实例 → 详情页导航 URL：只传稳定标识，业务数据必须由详情 GET 授权后返回。
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
  put('family_id', it.familyId)
  return '/pages/event-detail/index?' + q.join('&')
}

module.exports = { detailUrl, repeatLabel }
