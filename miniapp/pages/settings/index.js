// 设置页：对齐 prototype scr-settings。V1 先落本机可用配置与通道状态展示；真实触达通道接入后再同步到后端。
const app = getApp()

const DEFAULT_SETTINGS = {
  smsFallback: false,
  dndStart: '22:00',
  dndEnd: '07:00',
  strongOnlyInDnd: true,
  largeText: false
}

function settingsKey(fid, uid) {
  return 'settings:' + (fid || 'no-family') + ':' + (uid || 'no-user')
}

function loadSettings(fid, uid) {
  return Object.assign({}, DEFAULT_SETTINGS, wx.getStorageSync(settingsKey(fid, uid)) || {})
}

function todayStr() {
  const d = new Date()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return d.getFullYear() + '-' + m + '-' + day
}

function childLabel(c) {
  if (!c) return '请选择孩子'
  return c.name || c.relation_label || ('孩子' + c.member_id)
}

Page({
  data: {
    logged: false,
    hasFamily: false,
    familyName: '',
    serviceAccountStatus: '未绑定',
    children: [],
    childIndex: 0,
    childPickerLabel: '请选择孩子',
    exporting: false,
    deletingChild: false,
    smsFallback: false,
    dndStart: '22:00',
    dndEnd: '07:00',
    strongOnlyInDnd: true,
    largeText: false
  },

  onLoad() {
    this.refresh()
  },

  refresh() {
    const user = app.globalData.user
    const fid = app.globalData.familyId
    if (!app.globalData.token || !user) {
      this.setData({ logged: false, hasFamily: false })
      return
    }
    const s = loadSettings(fid, user.user_id)
    this.setData(Object.assign({
      logged: true,
      hasFamily: !!fid,
      familyName: wx.getStorageSync('familyName:' + fid) || '当前家庭',
      serviceAccountStatus: '订阅授权需在提醒时确认'
    }, s))
    if (fid) this.loadChildren()
  },

  loadChildren() {
    const fid = app.globalData.familyId
    if (!fid) return Promise.resolve([])
    return app.request(`/v1/families/${fid}/members`)
      .then((members) => {
        const children = (members || []).filter((m) => m.type === 'virtual')
        const index = Math.min(this.data.childIndex || 0, Math.max(children.length - 1, 0))
        this.setData({ children, childIndex: index, childPickerLabel: childLabel(children[index]) })
        return children
      })
      .catch((e) => {
        wx.showToast({ title: e.message || '加载孩子失败', icon: 'none' })
        return []
      })
  },

  persist(patch) {
    const user = app.globalData.user || {}
    const fid = app.globalData.familyId
    const next = Object.assign({}, loadSettings(fid, user.user_id), patch)
    wx.setStorageSync(settingsKey(fid, user.user_id), next)
    this.setData(next)
  },

  goBack() { wx.navigateBack() },

  toggleSmsFallback() {
    const next = !this.data.smsFallback
    this.persist({ smsFallback: next })
    wx.showToast({ title: next ? '已保存本机短信偏好' : '已关闭本机短信偏好', icon: 'none' })
  },

  onDndStart(e) {
    this.persist({ dndStart: e.detail.value })
    wx.showToast({ title: '已保存免打扰开始时间', icon: 'none' })
  },

  onDndEnd(e) {
    this.persist({ dndEnd: e.detail.value })
    wx.showToast({ title: '已保存免打扰结束时间', icon: 'none' })
  },

  toggleStrongOnly() {
    this.persist({ strongOnlyInDnd: !this.data.strongOnlyInDnd })
  },

  toggleLargeText() {
    const next = !this.data.largeText
    this.persist({ largeText: next })
    wx.showToast({ title: next ? '已开启大字模式' : '已关闭大字模式', icon: 'none' })
  },

  onChildPick(e) {
    const idx = Number(e.detail.value || 0)
    this.setData({ childIndex: idx, childPickerLabel: childLabel(this.data.children[idx]) })
  },

  exportChildData() {
    if (this.data.exporting) return
    const child = this.data.children[this.data.childIndex]
    if (!child) return wx.showToast({ title: '请先选择孩子', icon: 'none' })
    this.setData({ exporting: true })
    const end = todayStr()
    Promise.all([
      app.request('/v1/schedules'),
      app.request('/v1/tasks'),
      app.request(`/v1/checkins?start=2000-01-01&end=${end}&child_id=${child.member_id}`)
    ])
      .then(([schedules, tasks, checkins]) => {
        const data = {
          exported_at: new Date().toISOString(),
          family_id: app.globalData.familyId,
          child: {
            member_id: child.member_id,
            name: child.name || '',
            relation_label: child.relation_label || ''
          },
          schedules: (schedules || []).filter((x) => String(x.child_member_id) === String(child.member_id)),
          tasks: (tasks || []).filter((x) => String(x.child_member_id) === String(child.member_id)),
          checkins: checkins || []
        }
        const text = JSON.stringify(data, null, 2)
        wx.setClipboardData({ data: text })
        const fs = wx.getFileSystemManager && wx.getFileSystemManager()
        if (fs && wx.env && wx.env.USER_DATA_PATH) {
          const path = `${wx.env.USER_DATA_PATH}/ifami-child-${child.member_id}.json`
          fs.writeFile({ filePath: path, data: text, encoding: 'utf8', fail: () => {} })
        }
        wx.showModal({
          title: '已导出孩子数据',
          content: '已将该孩子的日程、任务和打卡记录整理为 JSON 并复制到剪贴板，可粘贴保存。',
          showCancel: false,
          confirmText: '知道了'
        })
      })
      .catch((e) => wx.showToast({ title: e.message || '导出失败', icon: 'none' }))
      .finally(() => this.setData({ exporting: false }))
  },

  deleteChildProfile() {
    if (this.data.deletingChild) return
    const child = this.data.children[this.data.childIndex]
    if (!child) return wx.showToast({ title: '请先选择孩子', icon: 'none' })
    const name = childLabel(child)
    wx.showModal({
      title: '删除孩子档案',
      content: `确认删除「${name}」吗？删除后该孩子不会再出现在家庭成员列表；历史日程、任务和打卡记录仍作为家庭事实记录保留。`,
      confirmText: '删除',
      confirmColor: '#BF4A3C',
      success: (r) => {
        if (!r.confirm) return
        this.setData({ deletingChild: true })
        const fid = app.globalData.familyId
        app.request(`/v1/families/${fid}/members/${child.member_id}`, { method: 'DELETE' })
          .then(() => {
            wx.showToast({ title: '已删除孩子档案', icon: 'success' })
            this.setData({ childIndex: 0 })
            return this.loadChildren()
          })
          .catch((e) => wx.showToast({ title: e.message || '删除失败', icon: 'none' }))
          .finally(() => this.setData({ deletingChild: false }))
      }
    })
  }
})
