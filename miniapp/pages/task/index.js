// 创建打卡任务：模板 chips + 目标量/频次/责任家长 seg，对齐 prototype scr-task-create。
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
  { label: '每周 N 次', value: 'weekly' }
]

const ROLE_LABEL = { admin: '管理员', parent: '家长', grandparent: '祖辈', child: '孩子' }

function withGoalLabel(t) {
  const unit = t.goal_type === 'count' ? '个' : '分钟'
  return Object.assign({}, t, { goalLabel: t.goal_value + ' ' + unit })
}

function decodeQueryText(v, fallback = '') {
  let s = v || fallback
  for (let i = 0; i < 2; i++) {
    if (!/%[0-9a-fA-F]{2}/.test(s)) break
    try {
      const decoded = decodeURIComponent(s)
      if (decoded === s) break
      s = decoded
    } catch (e) {
      break
    }
  }
  return s
}

function indexByValue(list, value, fallback = 0) {
  const i = list.findIndex((x) => x.value === value)
  return i >= 0 ? i : fallback
}

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
    weeklyTarget: '',
    goalUnit: '分钟',
    batchMode: false,
    batchTemplates: TEMPLATES.filter((t) => t.name !== '自定义').map((t) => Object.assign(withGoalLabel(t), { selected: true })),
    batchFreqs: FREQS,
    batchFreqIndex: 0,
    batchWeeklyTarget: '',
    editMode: false,
    editTaskId: '',
    editTask: null,
    submitting: false
  },

  onLoad(options = {}) {
    if (!app.globalData.isAdmin) {
      wx.showToast({ title: '只有管理员才能录入', icon: 'none' })
      setTimeout(() => wx.navigateBack(), 500)
      return
    }
    const editMode = options.edit === '1'
    const editTask = editMode ? {
      task_id: options.task_id || '',
      name: decodeQueryText(options.name, ''),
      goal_type: options.goal_type || 'timer',
      goal_value: parseInt(options.goal_value, 10) || 0,
      freq: options.freq || 'daily',
      weekly_target: parseInt(options.weekly_target, 10) || 0,
      child_member_id: parseInt(options.child_member_id, 10) || 0,
      owner_member_id: parseInt(options.owner_member_id, 10) || 0,
      remind_time: decodeQueryText(options.remind_time, '')
    } : null
    this.setData({ batchMode: !editMode && options.batch === '1', editMode, editTaskId: editTask ? editTask.task_id : '', editTask })
    this.loadMembers()
  },

  loadMembers() {
    // 先拉家庭列表定位当前家庭（对齐家庭页 refresh），避免依赖可能为空的 globalData.familyId 导致成员列表不加载。
    app.ensureLogin()
      .then(() => app.request('/v1/families'))
      .then((families) => {
        const list = families || []
        if (list.length === 0) {
          this.setData({ children: [], owners: [], ownerIndex: 0 })
          return
        }
        const cur = list.find((f) => f.family_id === app.globalData.familyId) || list[0]
        app.setFamily(cur.family_id)
        return app.request(`/v1/families/${cur.family_id}/members`)
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
            const relLabel = m.relation_label || m.name || '成员' + m.member_id
            owners.push({
              member_id: m.member_id,
              user_id: m.user_id,
              name: m.name || '成员' + m.member_id,
              label: relLabel + ' · ' + (ROLE_LABEL[m.role] || m.role)
            })
          }
        })
        const next = { children, owners, ownerIndex }
        if (this.data.editMode && this.data.editTask) {
          Object.assign(next, this.buildEditState(this.data.editTask, children, owners))
        }
        this.setData(next)
      })
      .catch((e) => {
        if (e.code !== 4001) wx.showToast({ title: e.message || '加载成员失败', icon: 'none' })
      })
  },

  buildEditState(task, children, owners) {
    const templateIndex = this.data.templates.findIndex((t) => t.name === task.name && t.goal_type === task.goal_type)
    const useTemplate = templateIndex >= 0
    const goalOptions = GOAL_PRESETS[task.goal_type] || GOAL_PRESETS.timer
    const goalIndex = goalOptions.findIndex((g) => !g.custom && g.value === task.goal_value)
    const childIndex = Math.max(0, children.findIndex((c) => c.member_id === task.child_member_id))
    const ownerIndex = Math.max(0, owners.findIndex((o) => o.member_id === task.owner_member_id))
    return {
      templateIndex: useTemplate ? templateIndex : this.data.templates.findIndex((t) => t.name === '自定义'),
      goalOptions,
      goalIndex: goalIndex >= 0 ? goalIndex : Math.max(0, goalOptions.findIndex((g) => g.custom)),
      freqIndex: indexByValue(this.data.freqs, task.freq),
      childIndex,
      ownerIndex,
      customName: useTemplate ? '' : task.name,
      customGoal: goalIndex >= 0 ? '' : String(task.goal_value || ''),
      weeklyTarget: task.freq === 'weekly' ? String(task.weekly_target || '') : '',
      goalUnit: task.goal_type === 'count' ? '个' : '分钟'
    }
  },

  selectTemplate(e) {
    const i = Number(e.currentTarget.dataset.i)
    const t = this.data.templates[i]
    const goalType = this.data.editMode && t.name === '自定义' && this.data.editTask ? this.data.editTask.goal_type : t.goal_type
    this.setData({
      templateIndex: i,
      goalOptions: GOAL_PRESETS[goalType] || GOAL_PRESETS.timer,
      goalIndex: 0,
      goalUnit: goalType === 'count' ? '个' : '分钟'
    })
  },

  selectGoal(e) { this.setData({ goalIndex: Number(e.currentTarget.dataset.i) }) },
  selectFreq(e) { this.setData({ freqIndex: Number(e.currentTarget.dataset.i) }) },
  selectBatchFreq(e) { this.setData({ batchFreqIndex: Number(e.currentTarget.dataset.i) }) },
  selectOwner(e) { this.setData({ ownerIndex: Number(e.currentTarget.dataset.i) }) },
  onChild(e) { this.setData({ childIndex: Number(e.detail.value) }) },

  toggleBatchTemplate(e) {
    const i = Number(e.currentTarget.dataset.i)
    const list = this.data.batchTemplates.slice()
    list[i].selected = !list[i].selected
    this.setData({ batchTemplates: list })
  },

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

    const freq = this.data.freqs[this.data.freqIndex].value
    let weeklyTarget = 0
    if (freq === 'weekly') {
      weeklyTarget = parseInt(this.data.weeklyTarget, 10)
      if (!weeklyTarget || weeklyTarget <= 0) return wx.showToast({ title: '请输入每周次数', icon: 'none' })
    }

    const payload = {
      name,
      goal_type: this.data.editMode && t.name === '自定义' && this.data.editTask ? this.data.editTask.goal_type : t.goal_type,
      goal_value: goalValue,
      freq,
      weekly_target: weeklyTarget,
      child_member_id: child.member_id,
      owner_member_id: owner.member_id,
      remind_time: this.data.editMode && this.data.editTask ? (this.data.editTask.remind_time || '') : ''
    }
    this.setData({ submitting: true })
    const method = this.data.editMode ? 'PATCH' : 'POST'
    const path = this.data.editMode ? `/v1/tasks/${this.data.editTaskId}` : '/v1/tasks'
    app.request(path, { method, data: payload })
      .then(() => {
        wx.showToast({ title: this.data.editMode ? '已保存' : '已创建', icon: 'success' })
        setTimeout(() => wx.navigateBack(), 600)
      })
      .catch((e) => {
        this.setData({ submitting: false })
        if (e.code === 4001) { app.logout(); wx.navigateBack() }
        else wx.showToast({ title: e.message || (this.data.editMode ? '保存失败' : '创建失败'), icon: 'none' })
      })
  },

  submitBatch() {
    if (this.data.submitting) return
    const child = this.data.children[this.data.childIndex]
    const owner = this.data.owners[this.data.ownerIndex]
    if (!child) return wx.showToast({ title: '请先到「家庭」页添加孩子', icon: 'none' })
    if (!owner) return wx.showToast({ title: '请选择责任人', icon: 'none' })
    const selected = this.data.batchTemplates.filter((t) => t.selected)
    if (selected.length === 0) return wx.showToast({ title: '请至少选择一个模板', icon: 'none' })
    const freq = this.data.batchFreqs[this.data.batchFreqIndex].value
    let weeklyTarget = 0
    if (freq === 'weekly') {
      weeklyTarget = parseInt(this.data.batchWeeklyTarget, 10)
      if (!weeklyTarget || weeklyTarget <= 0) return wx.showToast({ title: '请输入每周次数', icon: 'none' })
    }
    this.setData({ submitting: true })
    Promise.all(selected.map((t) => app.request('/v1/tasks', {
      method: 'POST',
      data: {
        name: t.name,
        goal_type: t.goal_type,
        goal_value: t.goal_value,
        freq,
        weekly_target: weeklyTarget,
        child_member_id: child.member_id,
        owner_member_id: owner.member_id,
        remind_time: ''
      }
    })))
      .then(() => {
        wx.showToast({ title: '已启用模板', icon: 'success' })
        setTimeout(() => wx.navigateBack(), 600)
      })
      .catch((e) => {
        this.setData({ submitting: false })
        if (e.code === 4001) { app.logout(); wx.navigateBack() }
        else wx.showToast({ title: e.message || '启用失败', icon: 'none' })
      })
  },

  goBack() { wx.navigateBack() }
})
