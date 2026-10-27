// 自定义 TabBar：emoji 图标 + 文字，对齐 prototype scr-tabbar（☀️今日/📅日历/✅打卡/🏠家庭）。
Component({
  data: {
    selected: 0,
    list: [
      { pagePath: '/pages/today/index', text: '今日', ic: '☀️' },
      { pagePath: '/pages/week/index', text: '日历', ic: '📅' },
      { pagePath: '/pages/daka/index', text: '打卡', ic: '✅' },
      { pagePath: '/pages/family/index', text: '家庭', ic: '🏠' }
    ]
  },
  methods: {
    switchTab(e) {
      const idx = Number(e.currentTarget.dataset.index)
      if (idx === this.data.selected) return
      wx.switchTab({ url: this.data.list[idx].pagePath })
    }
  }
})
