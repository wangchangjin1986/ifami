// 拍照导入日程 ①选图 + 标孩子 → ②上传并识别（进度轮询，可「先去忙」），对齐 prototype scr-import / scr-scan。
// 链路：POST /v1/ingest/jobs → wx.uploadFile 逐张（并发 3）→ POST start → GET 轮询 → 跳转确认页。
const app = getApp()

const MAX_IMAGES = 9
const POLL_MS = 2000
const POLL_LIMIT_MS = 90000

Page({
  data: {
    stage: 'pick', // pick | scanning | failed
    isAdmin: false,
    images: [], // [{path, name, childId}]
    children: [], // [{member_id, name}]
    pendingJobs: [],
    stepText: '',
    progress: 0,
    failMsg: ''
  },

  onLoad() {
    if (!app.globalData.isAdmin) {
      wx.showToast({ title: '只有管理员才能录入', icon: 'none' })
      setTimeout(() => wx.navigateBack(), 500)
      return
    }
    this.setData({ isAdmin: true })
    app.ensureLogin()
      .then(() => Promise.all([
        app.request(`/v1/families/${app.globalData.familyId}/members`),
        app.request('/v1/ingest/jobs?status=done')
      ]))
      .then(([members, jobs]) => {
        const children = (members || []).filter((m) => m.type === 'virtual')
          .map((m) => ({ member_id: m.member_id, name: m.name || '孩子' + m.member_id }))
        this.setData({ children, pendingJobs: jobs || [] })
      })
      .catch((e) => { if (e.code !== 4001) wx.showToast({ title: e.message || '加载失败', icon: 'none' }) })
  },

  onUnload() { this._stopped = true },

  choose(e) {
    const source = e.currentTarget.dataset.source
    const left = MAX_IMAGES - this.data.images.length
    if (left <= 0) return wx.showToast({ title: '一次最多 9 张', icon: 'none' })
    wx.chooseMedia({
      count: left,
      mediaType: ['image'],
      sourceType: [source],
      sizeType: ['compressed'],
      success: (r) => {
        const defaultChild = this.data.children.length === 1 ? this.data.children[0].member_id : 0
        const added = r.tempFiles.map((f, i) => ({
          path: f.tempFilePath,
          name: (source === 'camera' ? '拍摄' : '相册') + '图片 ' + (this.data.images.length + i + 1),
          childId: defaultChild,
          tooBig: f.size > 5 * 1024 * 1024
        }))
        this.setData({ images: this.data.images.concat(added) })
        if (added.some((x) => x.tooBig)) wx.showToast({ title: '有图片超过 5MB，请重新选择', icon: 'none' })
      }
    })
  },

  pickChild(e) {
    const { i, id } = e.currentTarget.dataset
    this.setData({ ['images[' + i + '].childId']: Number(id) })
  },

  removeImage(e) {
    const images = this.data.images.slice()
    images.splice(Number(e.currentTarget.dataset.i), 1)
    this.setData({ images })
  },

  preview(e) {
    wx.previewImage({ urls: this.data.images.map((x) => x.path), current: this.data.images[Number(e.currentTarget.dataset.i)].path })
  },

  openJob(e) {
    wx.navigateTo({ url: '/pages/import-confirm/index?job_id=' + e.currentTarget.dataset.id })
  },

  start() {
    const images = this.data.images.filter((x) => !x.tooBig)
    if (images.length === 0) return wx.showToast({ title: '请先拍照或从相册选图', icon: 'none' })
    this._stopped = false
    this.setData({ stage: 'scanning', progress: 5, stepText: '正在上传图片…' })
    app.request('/v1/ingest/jobs', { method: 'POST' })
      .then((r) => {
        this._jobId = r.job_id
        return this.uploadAll(r.job_id, images)
      })
      .then(() => {
        this.setData({ progress: 30, stepText: '正在识别第 1 / ' + images.length + ' 张…' })
        return app.request(`/v1/ingest/jobs/${this._jobId}/start`, { method: 'POST' })
      })
      .then(() => this.poll(Date.now()))
      .catch((e) => this.fail(e.message || '上传失败'))
  },

  // 并发 3 路上传；任一失败整体失败（保留已选图片可重试）。
  uploadAll(jobId, images) {
    let next = 0
    let done = 0
    const total = images.length
    const worker = () => {
      if (next >= total) return Promise.resolve()
      const seq = next++
      return this.uploadOne(jobId, seq, images[seq]).then(() => {
        done++
        this.setData({ progress: 5 + Math.round(25 * done / total), stepText: '正在上传 ' + done + ' / ' + total + '…' })
        return worker()
      })
    }
    const lanes = []
    for (let i = 0; i < Math.min(3, total); i++) lanes.push(worker())
    return Promise.all(lanes)
  },

  uploadOne(jobId, seq, img) {
    return new Promise((resolve, reject) => {
      wx.uploadFile({
        url: app.globalData.apiBase + `/v1/ingest/jobs/${jobId}/images`,
        filePath: img.path,
        name: 'file',
        formData: { seq: String(seq), child_member_id: String(img.childId || 0) },
        header: { Authorization: 'Bearer ' + app.globalData.token, 'X-Family-Id': String(app.globalData.familyId) },
        success: (res) => {
          let body = {}
          try { body = JSON.parse(res.data) } catch (e) { /* 非 JSON 响应按失败处理 */ }
          if (res.statusCode === 200 && body.code === 0) resolve()
          else reject(new Error(body.message || '上传失败'))
        },
        fail: () => reject(new Error('网络异常，上传失败'))
      })
    })
  },

  poll(begin) {
    if (this._stopped) return
    app.request(`/v1/ingest/jobs/${this._jobId}`)
      .then((j) => {
        if (this._stopped) return
        if (j.status === 'done') {
          this.setData({ progress: 100 })
          wx.redirectTo({ url: '/pages/import-confirm/index?job_id=' + this._jobId })
          return
        }
        if (j.status === 'failed') return this.fail('没识别出来，可以换张更清晰的图，或手动安排')
        const shown = Math.min(j.total, j.done + 1)
        this.setData({
          progress: 30 + Math.round(65 * j.done / Math.max(1, j.total)),
          stepText: j.done >= j.total ? '整理成日程…' : '正在识别第 ' + shown + ' / ' + j.total + ' 张…'
        })
        if (Date.now() - begin > POLL_LIMIT_MS) {
          wx.showToast({ title: '识别较慢，好了会提醒你', icon: 'none' })
          return
        }
        setTimeout(() => this.poll(begin), POLL_MS)
      })
      .catch(() => setTimeout(() => this.poll(begin), POLL_MS))
  },

  fail(msg) {
    this._stopped = true
    this.setData({ stage: 'failed', failMsg: msg })
  },

  retry() { this.setData({ stage: 'pick', failMsg: '' }) },

  leave() {
    this._stopped = true
    wx.showToast({ title: '识别好了会提醒你，也可从 ＋ 拍照导入里查看', icon: 'none' })
    setTimeout(() => wx.navigateBack(), 800)
  },

  goManual() { wx.redirectTo({ url: '/pages/event/index' }) },

  goBack() { wx.navigateBack() }
})
