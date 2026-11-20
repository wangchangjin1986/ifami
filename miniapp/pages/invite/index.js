// 家庭加入：扫码/手输 → 身份 → 称谓 → 核对实际家庭后直接加入。
const app = getApp()

const IDENTITY = {
  parent: {
    label: '家长',
    desc: '查看家庭安排、参与打卡，进入完整四 Tab；默认无录入/编辑入口',
    relations: ['妈妈', '爸爸', '其他家长']
  },
  grandparent: {
    label: '祖辈',
    desc: '只看分配给本人的安排，完成或发起调整',
    relations: ['奶奶', '爷爷', '姥姥', '姥爷']
  },
  caregiver: {
    label: '阿姨',
    desc: '只看今天分配给本人的安排',
    relations: ['阿姨', '保姆阿姨', '接送阿姨']
  }
}

function parseJoinQRCode(raw) {
  const matched = /^IFAMI_JOIN_V1:([0-9]{6}):([a-f0-9]{32}):([^:\s]{1,160})$/.exec(String(raw || '').trim())
  if (!matched) return null
  return { join_code: matched[1], generation_id: matched[2], credential: matched[3] }
}

function membershipForFamily(familyID) {
  return (app.globalData.memberships || []).find((m) => String(m.family_id) === String(familyID)) || null
}

Page({
  data: {
    joinCode: '',
    identity: 'parent',
    identityOptions: Object.keys(IDENTITY).map((value) => ({ value, label: IDENTITY[value].label, desc: IDENTITY[value].desc })),
    relations: IDENTITY.parent.relations,
    relation: IDENTITY.parent.relations[0],
    preview: null,
    previewReady: false,
    previewing: false,
    familyName: '',
    expiresText: '',
    joining: false,
    joinOutcomeUnknown: false,
    joinedNeedsRefresh: false,
    joinedMessage: '',
    errorText: ''
  },

  onLoad() {
    if (wx.hideShareMenu) wx.hideShareMenu({ menus: ['shareAppMessage', 'shareTimeline'] })
    this._previewSerial = 0
    this._scanPayload = null
    this._joinResult = null
  },

  onCodeInput(e) {
    const next = String((e.detail && e.detail.value) || '').replace(/\D/g, '').slice(0, 6)
    this._previewSerial++
    this._scanPayload = null
    this.setData({
      joinCode: next,
      preview: null,
      previewReady: false,
      previewing: false,
      familyName: '',
      expiresText: '',
      joinOutcomeUnknown: false,
      joinedNeedsRefresh: false,
      joinedMessage: '',
      errorText: ''
    })
  },

  scanCode() {
    wx.scanCode({
      onlyFromCamera: true,
      scanType: ['qrCode'],
      success: (result) => {
        const parsed = parseJoinQRCode(result.result)
        if (!parsed) {
          wx.showToast({ title: '不是有效的家庭加入码', icon: 'none' })
          return
        }
        this._previewSerial++
        this._scanPayload = parsed
        this.setData({ joinCode: parsed.join_code, preview: null, previewReady: false, familyName: '', errorText: '' })
        this.previewJoinCode()
      },
      fail: (error) => {
        const msg = String(error && error.errMsg || '')
        if (msg.indexOf('cancel') < 0) wx.showToast({ title: '无法扫码，可改为手输六位码', icon: 'none' })
      }
    })
  },

  previewJoinCode() {
    const joinCode = String(this.data.joinCode || '')
    if (!/^\d{6}$/.test(joinCode)) {
      wx.showToast({ title: '请输入六位家庭加入码', icon: 'none' })
      return Promise.resolve(false)
    }
    const scan = this._scanPayload && this._scanPayload.join_code === joinCode ? this._scanPayload : null
    const payload = scan
      ? { join_code: joinCode, generation_id: scan.generation_id, credential: scan.credential }
      : { join_code: joinCode }
    const serial = ++this._previewSerial
    this.setData({ previewing: true, previewReady: false, preview: null, familyName: '', errorText: '' })
    return app.ensureLogin()
      .then(() => app.request('/v1/family-join/preview', { method: 'POST', familyId: 0, data: payload }))
      .then((preview) => {
        if (serial !== this._previewSerial || this.data.joinCode !== joinCode) return false
        if (!preview || preview.join_code !== joinCode || !preview.family_id || !preview.generation_id || !preview.credential) {
          throw new Error('加入码校验结果不完整，请重新输入')
        }
        if (preview.expires_at && Date.parse(preview.expires_at) <= Date.now()) throw new Error('家庭加入码已失效，请让家人重新展示')
        this.setData({
          preview,
          previewing: false,
          previewReady: true,
          familyName: preview.family_name || '家庭',
          expiresText: preview.expires_at ? new Date(preview.expires_at).toLocaleString() : '',
          errorText: ''
        })
        return true
      })
      .catch((error) => {
        if (serial !== this._previewSerial) return false
        this.setData({ previewing: false, previewReady: false, preview: null, familyName: '', errorText: error.message || '校验失败，请重试' })
        return false
      })
  },

  selectIdentity(e) {
    const identity = e.currentTarget.dataset.identity
    if (!IDENTITY[identity]) return
    const relations = IDENTITY[identity].relations
    this.setData({ identity, relations, relation: relations[0] })
  },

  selectRelation(e) {
    const relation = e.currentTarget.dataset.relation
    if (this.data.relations.indexOf(relation) < 0) return
    this.setData({ relation })
  },

  confirmJoin() {
    if (this.data.joining) return Promise.resolve(false)
    const preview = this.data.preview
    if (!this.data.previewReady || !preview || preview.join_code !== this.data.joinCode) {
      wx.showToast({ title: '请先校验有效的家庭加入码', icon: 'none' })
      return Promise.resolve(false)
    }
    return this.withJoinLock(() => {
      if (this.data.joinOutcomeUnknown) {
        return this.recoverJoinedMembership(preview.family_id).then((recovered) => {
          if (recovered === 'joined') return true
          if (recovered === 'unavailable') return false
          return this.sendConfirm(preview)
        })
      }
      return this.sendConfirm(preview)
    })
  },

  withJoinLock(work) {
    if (this.data.joining) return Promise.resolve(false)
    this.setData({ joining: true })
    let result
    try { result = work() } catch (error) { result = Promise.reject(error) }
    return Promise.resolve(result).finally(() => this.setData({ joining: false }))
  },

  sendConfirm(preview) {
    const payload = {
      join_code: preview.join_code,
      generation_id: preview.generation_id,
      credential: preview.credential,
      identity: this.data.identity,
      relation_label: this.data.relation
    }
    this.setData({ errorText: '', joinedNeedsRefresh: false })
    return app.request('/v1/family-join/confirm', { method: 'POST', familyId: 0, data: payload })
      .then((result) => {
        this._joinResult = result
        this.setData({ joinOutcomeUnknown: false })
        return this.finishConfirmedJoin(result)
      })
      .catch((error) => {
        const resultUnknown = error && (error.code === 'NETWORK_FAIL' || Number(error.statusCode || 0) >= 500)
        if (!resultUnknown) {
          const invalidPreview = error && [1008, 1011, 1017].indexOf(Number(error.code)) >= 0
          this.setData({
            joinOutcomeUnknown: false,
            previewReady: invalidPreview ? false : this.data.previewReady,
            preview: invalidPreview ? null : this.data.preview,
            familyName: invalidPreview ? '' : this.data.familyName,
            errorText: error.message || '加入失败，请核对后重试'
          })
          return false
        }
        this.setData({ joinOutcomeUnknown: true, errorText: '加入结果待确认，正在核对家庭信息…' })
        return this.recoverJoinedMembership(preview.family_id).then((recovered) => {
          if (recovered === 'joined') return true
          if (recovered === 'unavailable') return false
          this.setData({ errorText: error.message || '网络中断，保留当前输入；请点重试先核对加入结果' })
          return false
        })
      })
  },

  finishConfirmedJoin(result) {
    return app.refreshMe()
      .then(() => this.routeByActualMembership(result))
      .catch(() => {
        app.setFamily(result.family_id)
        this.setData({
          joinedNeedsRefresh: true,
          joinedMessage: `已加入「${this.data.familyName || '家庭'}」，家庭信息刷新失败，请重试`,
          errorText: ''
        })
        return true
      })
  },

  recoverJoinedMembership(familyID) {
    return app.refreshMe()
      .then(() => {
        const membership = membershipForFamily(familyID)
        if (!membership) return 'absent'
        this.routeByActualMembership(Object.assign({ family_id: familyID }, membership))
        return 'joined'
      })
      .catch(() => {
        this.setData({ joinOutcomeUnknown: true, errorText: '暂时无法核对加入结果，当前输入已保留，请稍后重试' })
        return 'unavailable'
      })
  },

  retryRefresh() {
    if (this.data.joining) return Promise.resolve(false)
    const result = this._joinResult
    return this.withJoinLock(() => {
      if (!result) return this.recoverJoinedMembership(this.data.preview && this.data.preview.family_id).then((recovered) => recovered === 'joined')
      return app.refreshMe()
        .then(() => this.routeByActualMembership(result))
        .catch(() => this.setData({ joinedNeedsRefresh: true, joinedMessage: '已加入，家庭信息仍未刷新，请稍后重试' }))
    })
  },

  routeByActualMembership(result) {
    const actual = membershipForFamily(result.family_id) || result
    app.setFamily(result.family_id)
    this.setData({ joinOutcomeUnknown: false, joinedNeedsRefresh: false, errorText: '' })
    const caregiver = actual.role === 'grandparent' && !actual.is_admin
    wx.reLaunch({ url: caregiver ? '/pages/gp-today/index' : '/pages/today/index' })
  },

  goBack() { wx.navigateBack() }
})

if (typeof module !== 'undefined') module.exports = { parseJoinQRCode }
