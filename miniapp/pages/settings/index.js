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

Page({
  data: {
    logged: false,
    hasFamily: false,
    familyName: '',
    serviceAccountStatus: '未绑定',
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
      serviceAccountStatus: '订阅消息可用'
    }, s))
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
    wx.showToast({ title: next ? '已开启短信兜底偏好' : '已关闭短信兜底偏好', icon: 'none' })
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

  exportChildData() {
    wx.showModal({
      title: '申请导出孩子数据',
      content: 'V1 会保留家庭内日程、打卡与周报事实记录。当前版本请联系管理员从家庭周报与成长时间线查看；批量导出将在后续版本开放。',
      showCancel: false,
      confirmText: '知道了'
    })
  },

  deleteChildProfile() {
    wx.showModal({
      title: '删除孩子档案',
      content: '为避免误删成长记录，V1 不在设置页直接删除孩子档案。如需调整孩子名称或身份，请回到家庭页点孩子卡片编辑。',
      showCancel: false,
      confirmText: '知道了'
    })
  }
})
