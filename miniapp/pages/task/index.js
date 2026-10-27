// 创建打卡任务：模板 chips + 目标量/频次/责任父母 seg，对齐 prototype scr-task-create。
const app = getApp()

const TEMPLATES = [
  { name: '阅读', goal_type: 'timer', goal_value: 20 },
  { name: '跳绳', goal_type: 'count', goal_value: 100 },
  { name: '练琴', goal_type: 'timer', goal_value: 30 },
  { name: '英语', goal_type: 'timer', goal_value: 20 },
  { name: '自定义', goal_type: 'timer', goal_value: 0 }
]

const GOAL_PRESETS = {
  timer: [
    { label: '20 分钟', value: 20 },
    { label: '30 分钟', value: 30 },
    { label: '自定义', value: 0, custom: true }
  ],
  count: [
    { label: '50 个', value: 50 },
    { label: '100 个', value: 100 },
    { label: '自定义', value: 0, custom: true }
  ]
}

const FREQS = [
  { label: '每天', value: 'daily' },
  { label: '工作日', value: 'weekdays' },
  { label: '自定义', value: 'custom' }
]

Page({
  data: {
    templates: TEMPLATES,
    templateIndex: 0,
    goalOptions: GOAL_PRESETS.timer,
    goalIndex: 0,
    freqs: FREQS,
    freqIndex: 0,
    children: [],
    owners: [],
    childIndex: 0,
    ownerIndex: 0,
    customName: '',
    customGoal: '',
    goalUnit: '分钟',
    submitting: false
  },

  onLoad() {
    this.loadMembers()
  },

  loadMembers() {
    app.ensureLogin()
      .then(() => {
        const fid = app.globalData.familyId
        if (!fid) return
        return app.request(`/v1/families/${fid}/members`)
      })
      .then((members) => {
        const children = []
        const owners = []
        const myId = app.globalData.user && app.globalData.user.user_id
        let ownerIndex = 0
        ;(members || []).forEach((m) => {
          if (m.type === 'virtual') {
            children.push({ member_id: m.member_id, name: m.name || '孩子' + m.member_id })
          } else {
            const idx = owners.length
            if (m.user_id === myId) ownerIndex = idx
            owners.push({ member_id: m.member_id, user_id: m.user_id, name: m.name || '成员' + m.member_id })
          }
        })
        this.setData({ children, owners, ownerIndex })
      })
      .catch((e) => {
        if (e.code !== 4001) wx.showToast({ title: e.message || '加载成员失败', icon: 'none' })
      })
  },

  selectTemplate(e) {
    const i = Number(e.currentTarget.dataset.i)
    const t = this.data.templates[i]
    this.setData({
      templateIndex: i,
      goalOptions: GOAL_PRESETS[t.goal_type] || GOAL_PRESETS.timer,
      goalIndex: 0,
      goalUnit: t.goal_type === 'count' ? '个' : '分钟'
    })
  },

  selectGoal(e) { this.setData({ goalIndex: Number(e.currentTarget.dataset.i) }) },
  selectFreq(e) { this.setData({ freqIndex: Number(e.currentTarget.dataset.i) }) },
  selectOwner(e) { this.setData({ ownerIndex: Number(e.currentTarget.dataset.i) }) },
  onChild(e) { this.setData({ childIndex: Number(e.detail.value) }) },
  onCustomName(e) { this.setData({ customName: e.detail.value }) },
  onCustomGoal(e) { this.setData({ customGoal: e.detail.value }) },

  submit() {
    if (this.data.submitting) return
    const t = this.data.templates[this.data.templateIndex]
    const child = this.data.children[this.data.childIndex]
    const owner = this.data.owners[this.data.ownerIndex]
    const goal = this.data.goalOptions[this.data.goalIndex]

    const name = (t.name === '自定义' ? this.data.customName : t.name).trim()
    if (!name) return wx.showToast({ title: '请输入任务名', icon: 'none' })
    if (!child) return wx.showToast({ title: '请先到「家庭」页添加孩子', icon: 'none' })
    if (!owner) return wx.showToast({ title: '请选择责任人', icon: 'none' })
    const goalValue = goal.custom ? parseInt(this.data.customGoal, 10) : goal.value
    if (!goalValue || goalValue <= 0) return wx.showToast({ title: '请输入目标量', icon: 'none' })

    const payload = {
      name,
      goal_type: t.goal_type,
      goal_value: goalValue,
      freq: this.data.freqs[this.data.freqIndex].value,
      child_member_id: child.member_id,
      owner_member_id: owner.member_id,
      remind_time: ''
    }
    this.setData({ submitting: true })
    app.request('/v1/tasks', { method: 'POST', data: payload })
      .then(() => {
        wx.showToast({ title: '已创建', icon: 'success' })
        setTimeout(() => wx.navigateBack(), 600)
      })
      .catch((e) => {
        this.setData({ submitting: false })
        if (e.code === 4001) { app.logout(); wx.navigateBack() }
        else wx.showToast({ title: e.message || '创建失败', icon: 'none' })
      })
  },

  goBack() { wx.navigateBack() }
})
