const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const crypto = require('node:crypto')
const jsQR = require('jsqr')

const ROOT = path.resolve(__dirname, '..')

function clone(value) {
  return JSON.parse(JSON.stringify(value))
}

function assignPath(target, key, value) {
  const parts = key.split('.')
  let cur = target
  while (parts.length > 1) {
    const part = parts.shift()
    cur[part] = cur[part] || {}
    cur = cur[part]
  }
  cur[parts[0]] = value
}

function loadPage(name, app, wxOverrides = {}) {
  let definition
  const calls = []
  const storage = new Map()
  const wx = new Proxy(Object.assign({
    getStorageSync: (key) => storage.get(key),
    setStorageSync: (key, value) => storage.set(key, value),
    removeStorageSync: (key) => storage.delete(key),
    nextTick: (fn) => fn()
  }, wxOverrides), {
    get(target, key) {
      if (key in target) return target[key]
      return (options = {}) => {
        calls.push({ method: key, options })
        if (options.success) options.success({ confirm: true })
      }
    }
  })
  const filename = path.join(ROOT, name + '.js')
  const mod = { exports: {} }
  const context = {
    getApp: () => app,
    Page: (value) => { definition = value },
    Component: (value) => { definition = Object.assign({}, value, value.methods) },
    require: (request) => require(path.resolve(path.dirname(filename), request)),
    module: mod,
    exports: mod.exports,
    wx,
    console,
    Date,
    Promise,
    setTimeout: (fn) => { fn(); return 1 },
    clearTimeout: () => {},
    setInterval: () => 1,
    clearInterval: () => {},
    getCurrentPages: () => []
  }
  vm.runInNewContext(fs.readFileSync(filename, 'utf8'), context, { filename })
  definition.data = clone(definition.data || {})
  definition.setData = function (patch, callback) {
    Object.entries(patch).forEach(([key, value]) => assignPath(this.data, key, value))
    if (callback) callback()
  }
  return { page: definition, wx, calls, storage, exports: mod.exports }
}

async function settle() {
  await Promise.resolve()
  await Promise.resolve()
  await Promise.resolve()
}

function baseApp(overrides = {}) {
  return Object.assign({
    globalData: {
      token: 'token', user: { user_id: 7 }, memberships: [], familyId: null,
      role: null, isAdmin: false
    },
    ensureLogin: () => Promise.resolve(),
    refreshMe: () => Promise.resolve(),
    setFamily(fid) { this.globalData.familyId = fid },
    request: () => Promise.resolve(),
    reportNotifyReceipt: () => Promise.resolve(),
    requestNotifySubscribe: () => Promise.resolve(false),
    routeGrandparentHome: () => false
  }, overrides)
}

function qrPixels(matrix, scale = 6, quiet = 4) {
  const modules = matrix.length
  const size = (modules + quiet * 2) * scale
  const data = new Uint8ClampedArray(size * size * 4)
  data.fill(255)
  for (let row = 0; row < modules; row++) {
    for (let col = 0; col < modules; col++) {
      if (!matrix[row][col]) continue
      for (let y = 0; y < scale; y++) {
        for (let x = 0; x < scale; x++) {
          const p = (((row + quiet) * scale + y) * size + (col + quiet) * scale + x) * 4
          data[p] = data[p + 1] = data[p + 2] = 0
          data[p + 3] = 255
        }
      }
    }
  }
  return { data, size }
}

function realJoinPayload(familyId, joinCode, generationId, expiresAt) {
  const unsigned = `${familyId}.${expiresAt}`
  const signature = crypto.createHmac('sha256', 'test-invite-secret')
    .update(`IFAMI_JOIN_V1:${unsigned}`)
    .digest('base64url')
  assert.match(signature, /^[A-Za-z0-9_-]{43}$/)
  return `IFAMI_JOIN_V1:${joinCode}:${generationId}:${unsigned}.${signature}`
}

function rasterCanvas(size) {
  const data = new Uint8ClampedArray(size * size * 4)
  data.fill(255)
  let color = '#FFFFFF'
  const ctx = {
    setFillStyle(value) { color = value },
    fillRect(left, top, width, height) {
      const value = color === '#FFFFFF' ? 255 : 0
      const x0 = Math.max(0, Math.floor(left)); const y0 = Math.max(0, Math.floor(top))
      const x1 = Math.min(size, Math.ceil(left + width)); const y1 = Math.min(size, Math.ceil(top + height))
      for (let y = y0; y < y1; y++) {
        for (let x = x0; x < x1; x++) {
          const p = (y * size + x) * 4
          data[p] = data[p + 1] = data[p + 2] = value
          data[p + 3] = 255
        }
      }
    },
    draw() {}
  }
  return { ctx, data, size }
}

function deferred() {
  let resolve
  let reject
  const promise = new Promise((ok, fail) => { resolve = ok; reject = fail })
  return { promise, resolve, reject }
}

const tests = []
function test(name, fn) { tests.push({ name, fn }) }

test('本地二维码可被独立 jsQR 解出完整协议', () => {
  const { createMatrix } = require('../utils/qrcode')
  const payload = 'IFAMI_JOIN_V1:004219:0123456789abcdef0123456789abcdef:42.1795200000.signature'
  const pixels = qrPixels(createMatrix(payload))
  const decoded = jsQR(pixels.data, pixels.size, pixels.size, { inversionAttempts: 'dontInvert' })
  assert.ok(decoded)
  assert.equal(decoded.data, payload)
})

test('家庭码页面按320/375/414设备实际canvas像素绘制且均可解码', () => {
  const payload = 'IFAMI_JOIN_V1:004219:0123456789abcdef0123456789abcdef:42.1795200000.signature'
  const markup = fs.readFileSync(path.join(ROOT, 'pages/family/index.wxml'), 'utf8')
  assert.match(markup, /width:\{\{joinCodeCanvasSize\}\}px;height:\{\{joinCodeCanvasSize\}\}px/)
  for (const windowWidth of [320, 375, 414]) {
    const app = baseApp({ globalData: { token: 't', user: { user_id: 7 }, memberships: [], familyId: 42, role: 'parent', isAdmin: true } })
    let raster
    const loaded = loadPage('pages/family/index', app, {
      getSystemInfoSync: () => ({ windowWidth }),
      hideShareMenu: () => {},
      createCanvasContext: () => raster.ctx
    })
    loaded.page.onLoad()
    loaded.page.data.inviteSheet = true
    loaded.page.data.isAdmin = true
    raster = rasterCanvas(loaded.page.data.joinCodeCanvasSize)
    const previousWx = global.wx
    global.wx = { createCanvasContext: () => raster.ctx }
    try {
      assert.equal(loaded.page.showJoinCode({
        has_code: true,
        join_code: '004219',
        generation_id: '0123456789abcdef0123456789abcdef',
        qr_content: payload,
        expires_at: new Date(Date.now() + 3600000).toISOString()
      }), true)
    } finally {
      global.wx = previousWx
    }
    assert.equal(raster.size, Math.floor(windowWidth * 420 / 750))
    const decoded = jsQR(raster.data, raster.size, raster.size, { inversionAttempts: 'dontInvert' })
    assert.ok(decoded, `windowWidth=${windowWidth} 应可解码`)
    assert.equal(decoded.data, payload)
  }
})

test('家庭码页面在三种设备尺寸均可解码真实长度HMAC凭证', () => {
  const generationId = '9a21702983277463fd8e474967e2607f'
  const cases = [
    { windowWidth: 320, familyId: 100, joinCode: '540298', expiresAt: 1795242255, payloadBytes: 112 },
    { windowWidth: 375, familyId: 42, joinCode: '004219', expiresAt: 1795242256, payloadBytes: 111 },
    { windowWidth: 414, familyId: 7, joinCode: '900001', expiresAt: 1795242257, payloadBytes: 110 }
  ]
  for (const item of cases) {
    const payload = realJoinPayload(item.familyId, item.joinCode, generationId, item.expiresAt)
    assert.equal(Buffer.byteLength(payload), item.payloadBytes)
    assert.equal(require('../utils/qrcode').createMatrix(payload).length, 45)

    const app = baseApp({ globalData: { token: 't', user: { user_id: 7 }, memberships: [], familyId: item.familyId, role: 'parent', isAdmin: true } })
    let raster
    const loaded = loadPage('pages/family/index', app, {
      getSystemInfoSync: () => ({ windowWidth: item.windowWidth }),
      hideShareMenu: () => {},
      createCanvasContext: () => raster.ctx
    })
    loaded.page.onLoad()
    loaded.page.data.inviteSheet = true
    loaded.page.data.isAdmin = true
    raster = rasterCanvas(loaded.page.data.joinCodeCanvasSize)
    const previousWx = global.wx
    global.wx = { createCanvasContext: () => raster.ctx }
    try {
      assert.equal(loaded.page.showJoinCode({
        has_code: true,
        join_code: item.joinCode,
        generation_id: generationId,
        qr_content: payload,
        expires_at: new Date(Date.now() + 3600000).toISOString()
      }), true)
    } finally {
      global.wx = previousWx
    }
    const decoded = jsQR(raster.data, raster.size, raster.size, { inversionAttempts: 'dontInvert' })
    assert.ok(decoded, `windowWidth=${item.windowWidth}, payloadBytes=${item.payloadBytes} 应可解码`)
    assert.equal(decoded.data, payload)
  }
})

test('扫码仅接受完整 IFAMI 协议，拒绝外链和残缺字段', () => {
  const app = baseApp()
  const { exports } = loadPage('pages/invite/index', app)
  assert.deepEqual(exports.parseJoinQRCode('https://example.com/482913'), null)
  assert.deepEqual(exports.parseJoinQRCode('IFAMI_JOIN_V1:482913:bad:credential'), null)
  assert.equal(JSON.stringify(exports.parseJoinQRCode('IFAMI_JOIN_V1:482913:0123456789abcdef0123456789abcdef:cred')), JSON.stringify({
    join_code: '482913', generation_id: '0123456789abcdef0123456789abcdef', credential: 'cred'
  }))
})

test('数字码改变立即清空确认，旧 preview 响应不能覆盖新码', async () => {
  let resolvePreview
  const app = baseApp({ request: () => new Promise((resolve) => { resolvePreview = resolve }) })
  const { page } = loadPage('pages/invite/index', app)
  page.data.joinCode = '482913'
  const pending = page.previewJoinCode()
  await settle()
  page.onCodeInput({ detail: { value: '123456' } })
  resolvePreview({ family_id: 42, family_name: '旧家庭', join_code: '482913', generation_id: 'a'.repeat(32), credential: 'old' })
  await pending
  assert.equal(page.data.joinCode, '123456')
  assert.equal(page.data.previewReady, false)
  assert.equal(page.data.familyName, '')
})

test('未校验或无效码不可确认加入', async () => {
  let requests = 0
  const app = baseApp({ request: () => { requests++; return Promise.resolve({}) } })
  const { page, calls } = loadPage('pages/invite/index', app)
  page.data.joinCode = '123'
  await page.confirmJoin()
  assert.equal(requests, 0)
  assert.ok(calls.some((x) => x.method === 'showToast'))
})

test('confirm 明确返回码失效后清除旧确认，不能再次提交', async () => {
  let requests = 0
  const app = baseApp({ request: () => { requests++; return Promise.reject(Object.assign(new Error('加入码已失效'), { code: 1011, statusCode: 400 })) } })
  const { page } = loadPage('pages/invite/index', app)
  Object.assign(page.data, {
    joinCode: '482913', identity: 'parent', relation: '妈妈', previewReady: true,
    preview: { family_id: 42, join_code: '482913', generation_id: 'a'.repeat(32), credential: 'cred' }
  })
  await page.confirmJoin()
  assert.equal(page.data.previewReady, false)
  assert.equal(page.data.preview, null)
  await page.confirmJoin()
  assert.equal(requests, 1)
})

test('confirm 返回的实际照护人身份覆盖所选家长并落到本人页', async () => {
  const navigations = []
  const app = baseApp({
    request: (url) => url.endsWith('/confirm')
      ? Promise.resolve({ family_id: 42, member_id: 99, role: 'grandparent', is_admin: false, relation_label: '奶奶' })
      : Promise.resolve(),
    refreshMe() {
      this.globalData.memberships = [{ family_id: 42, member_id: 99, role: 'grandparent', is_admin: false }]
      return Promise.resolve()
    }
  })
  const { page } = loadPage('pages/invite/index', app, { reLaunch: ({ url }) => navigations.push(url) })
  Object.assign(page.data, {
    joinCode: '482913', identity: 'parent', relation: '妈妈', previewReady: true,
    preview: { family_id: 42, join_code: '482913', generation_id: 'a'.repeat(32), credential: 'cred' }
  })
  await page.confirmJoin()
  assert.equal(app.globalData.familyId, 42)
  assert.deepEqual(navigations, ['/pages/gp-today/index'])
})

test('confirm 响应丢失且码随后失效时先用 me 恢复既成加入', async () => {
  const navigations = []
  let confirmCalls = 0
  const app = baseApp({
    request: (url) => {
      if (url.endsWith('/confirm')) { confirmCalls++; return Promise.reject(Object.assign(new Error('连接中断'), { code: 'NETWORK_FAIL' })) }
      return Promise.resolve()
    },
    refreshMe() {
      this.globalData.memberships = [{ family_id: 42, member_id: 88, role: 'parent', is_admin: false }]
      return Promise.resolve()
    }
  })
  const { page } = loadPage('pages/invite/index', app, { reLaunch: ({ url }) => navigations.push(url) })
  Object.assign(page.data, {
    joinCode: '482913', identity: 'parent', relation: '爸爸', previewReady: true,
    preview: { family_id: 42, join_code: '482913', generation_id: 'a'.repeat(32), credential: 'cred' }
  })
  await page.confirmJoin()
  assert.equal(confirmCalls, 1)
  assert.deepEqual(navigations, ['/pages/today/index'])
  assert.equal(page.data.joinOutcomeUnknown, false)
})

test('confirm 已成功但 refreshMe 失败时明确保留成功并可重试落地', async () => {
  const navigations = []
  let refreshOK = false
  const app = baseApp({
    request: () => Promise.resolve({ family_id: 42, member_id: 99, role: 'parent', is_admin: false, relation_label: '爸爸' }),
    refreshMe() {
      if (!refreshOK) return Promise.reject(new Error('me timeout'))
      this.globalData.memberships = [{ family_id: 42, member_id: 99, role: 'parent', is_admin: false }]
      return Promise.resolve()
    }
  })
  const { page } = loadPage('pages/invite/index', app, { reLaunch: ({ url }) => navigations.push(url) })
  Object.assign(page.data, {
    joinCode: '482913', identity: 'parent', relation: '爸爸', previewReady: true,
    preview: { family_id: 42, join_code: '482913', generation_id: 'a'.repeat(32), credential: 'cred' }
  })
  await page.confirmJoin()
  assert.equal(page.data.joinedNeedsRefresh, true)
  assert.match(page.data.joinedMessage, /已加入/)
  assert.deepEqual(navigations, [])
  refreshOK = true
  await page.retryRefresh()
  assert.deepEqual(navigations, ['/pages/today/index'])
})

test('未知结果恢复期间双击只启动一条/me链并保持锁到恢复成功', async () => {
  const me = deferred()
  let meCalls = 0
  let confirmCalls = 0
  const app = baseApp({
    refreshMe() {
      meCalls++
      return me.promise.then(() => {
        this.globalData.memberships = [{ family_id: 42, member_id: 99, role: 'parent', is_admin: false }]
      })
    },
    request: () => { confirmCalls++; return Promise.resolve() }
  })
  const { page } = loadPage('pages/invite/index', app, { reLaunch: () => {} })
  Object.assign(page.data, {
    joinCode: '482913', identity: 'parent', relation: '爸爸', previewReady: true, joinOutcomeUnknown: true,
    preview: { family_id: 42, join_code: '482913', generation_id: 'a'.repeat(32), credential: 'cred' }
  })
  const first = page.confirmJoin()
  const second = page.confirmJoin()
  assert.equal(page.data.joining, true)
  assert.equal(meCalls, 1)
  assert.equal(confirmCalls, 0)
  me.resolve()
  await Promise.all([first, second])
  assert.equal(confirmCalls, 0)
  assert.equal(page.data.joining, false)
})

test('未知结果恢复未找到membership时双击只confirm一次且链结束后可重试', async () => {
  const me = deferred()
  let meCalls = 0
  let confirmCalls = 0
  const app = baseApp({
    refreshMe() { meCalls++; return me.promise },
    request: () => {
      confirmCalls++
      return Promise.reject(Object.assign(new Error('暂时不可用'), { code: 1012, statusCode: 503 }))
    }
  })
  const { page } = loadPage('pages/invite/index', app)
  Object.assign(page.data, {
    joinCode: '482913', identity: 'parent', relation: '爸爸', previewReady: true, joinOutcomeUnknown: true,
    preview: { family_id: 42, join_code: '482913', generation_id: 'a'.repeat(32), credential: 'cred' }
  })
  const first = page.confirmJoin()
  const second = page.confirmJoin()
  assert.equal(page.data.joining, true)
  assert.equal(meCalls, 1)
  me.resolve()
  await Promise.all([first, second])
  assert.equal(confirmCalls, 1)
  assert.equal(page.data.joining, false)
  assert.equal(page.data.joinCode, '482913')
  assert.equal(page.data.previewReady, true)
})

test('详情在授权 GET 成功前不采用 URL 业务数据，403 后保持安全错误态', async () => {
  let rejectDetail
  const app = baseApp({ request: (url) => url.includes('/v1/schedules/instances/')
    ? new Promise((resolve, reject) => { rejectDetail = reject })
    : Promise.resolve([]) })
  const { page } = loadPage('pages/event-detail/index', app)
  page.onLoad({ instance_id: '7', family_id: '42', title: '伪造标题', location: '伪造地点', note: '伪造备注' })
  assert.equal(page.data.title, '')
  assert.equal(page.data.location, '')
  await settle()
  rejectDetail(Object.assign(new Error('无权限'), { code: 2004, statusCode: 403 }))
  await settle()
  assert.equal(page.data.loaded, false)
  assert.equal(page.data.title, '')
  assert.match(page.data.errorText, /分工已调整|无权查看/)
})

test('普通家长不能从方法绕过成员、录入、管理与任务维护入口', async () => {
  let requests = 0
  const app = baseApp({ globalData: { token: 't', user: { user_id: 7 }, memberships: [{ family_id: 1, role: 'parent', is_admin: false }], familyId: 1, role: 'parent', isAdmin: false }, request: () => { requests++; return Promise.resolve([]) } })
  const family = loadPage('pages/family/index', app).page
  family.data.childName = '孩子'
  family.addChild()
  family.goManage()
  family.openFab()
  const detail = loadPage('pages/task-detail/index', app).page
  detail.data.task = { task_id: 1 }
  detail.editTask()
  detail.pauseTask()
  detail.archiveTask()
  assert.equal(requests, 0)
  assert.equal(family.data.sheetOpen, false)
})

test('打卡删除只给管理员或稳定 submit_member_id 对应本人', async () => {
  const app = baseApp({ globalData: { token: 't', user: { user_id: 7 }, memberships: [], familyId: 1, role: 'parent', isAdmin: false } })
  const { page } = loadPage('pages/task-detail/index', app)
  page._currentMemberId = 22
  page.data.checkins = [
    { checkin_id: 1, submit_member_id: 22, canDelete: true },
    { checkin_id: 2, submit_member_id: 23, canDelete: false }
  ]
  assert.equal(page.canDeleteCheckin(page.data.checkins[0]), true)
  assert.equal(page.canDeleteCheckin(page.data.checkins[1]), false)
})

test('timeline实际页面仅允许管理员或稳定原提交者纠错且缺元数据失败关闭', async () => {
  let deleteRequests = 0
  const app = baseApp({
    globalData: { token: 't', user: { user_id: 7 }, memberships: [], familyId: 1, role: 'parent', isAdmin: false },
    refreshMe: () => Promise.resolve(),
    request: (url, options = {}) => {
      if (url.includes('/members')) return Promise.resolve([
        { member_id: 11, type: 'virtual', name: '乐乐' },
        { member_id: 22, type: 'real', user_id: 7, relation_label: '妈妈' },
        { member_id: 23, type: 'real', user_id: 8, relation_label: '爸爸' }
      ])
      if (url === '/v1/tasks') return Promise.resolve([{ task_id: 1, name: '阅读', goal_type: 'timer', goal_value: 20 }])
      if (url.includes('/schedules/instances')) return Promise.resolve([])
      if (url.startsWith('/v1/checkins?')) return Promise.resolve([
        { checkin_id: 1, task_id: 1, child_member_id: 11, submit_member_id: 22, created_at: '2026-11-20T08:00:00Z' },
        { checkin_id: 2, task_id: 1, child_member_id: 11, submit_member_id: 23, created_at: '2026-11-20T09:00:00Z' },
        { checkin_id: 3, task_id: 1, child_member_id: 11, created_at: '2026-11-20T10:00:00Z' }
      ])
      if (url.startsWith('/v1/checkins/')) { deleteRequests++; return Promise.resolve() }
      return Promise.resolve([])
    }
  })
  const loaded = loadPage('pages/timeline/index', app)
  await loaded.page.loadTimeline()
  const byID = Object.fromEntries(loaded.page.data.entries.filter((x) => x.kind === 'checkin').map((x) => [x.checkinId, x]))
  assert.equal(byID[1].canDelete, true)
  assert.equal(byID[2].canDelete, false)
  assert.equal(byID[3].canDelete, false)
  loaded.page.refresh = () => Promise.resolve()
  loaded.page.deleteCheckin({ currentTarget: { dataset: { id: 2 } } })
  loaded.page.deleteCheckin({ currentTarget: { dataset: { id: 3 } } })
  assert.equal(deleteRequests, 0)
  loaded.page.deleteCheckin({ currentTarget: { dataset: { id: 1 } } })
  await settle()
  assert.equal(deleteRequests, 1)
  const markup = fs.readFileSync(path.join(ROOT, 'pages/timeline/index.wxml'), 'utf8')
  assert.match(markup, /item\.canDelete/)
})

test('照护人今日只请求当前家庭，空态文案固定为本人分工', async () => {
  const requestedFamilyIds = []
  const app = baseApp({
    globalData: { token: 't', user: { user_id: 7 }, memberships: [
      { family_id: 10, role: 'grandparent', is_admin: false },
      { family_id: 20, role: 'grandparent', is_admin: false }
    ], familyId: 20, role: 'grandparent', isAdmin: false },
    request: (url, options = {}) => {
      if (options.familyId) requestedFamilyIds.push(options.familyId)
      if (url === '/v1/families') return Promise.resolve([{ family_id: 10, name: '甲' }, { family_id: 20, name: '乙' }])
      return Promise.resolve([])
    }
  })
  const { page } = loadPage('pages/gp-today/index', app)
  await page.loadAggregate()
  assert.deepEqual([...new Set(requestedFamilyIds)], [20])
  assert.equal(page.data.empty, true)
  const markup = fs.readFileSync(path.join(ROOT, 'pages/gp-today/index.wxml'), 'utf8')
  assert.match(markup, /今天没有分配给你的事项/)
})

test('码管理30分钟内复用 pending key，过窗先 GET 且不重放旧操作', async () => {
  const app = baseApp({
    globalData: { token: 't', user: { user_id: 7 }, memberships: [{ family_id: 42, role: 'parent', is_admin: true }], familyId: 42, role: 'parent', isAdmin: true }
  })
  const ctx = { setFillStyle() {}, fillRect() {}, draw() {} }
  const first = loadPage('pages/family/index', app, { createCanvasContext: () => ctx })
  first.page.data.isAdmin = true
  first.page.data.inviteSheet = true
  first.storage.set('joinCodePending:42', { key: 'pending-within-window', at: Date.now() })
  let reused = ''
  app.request = (url, options) => {
    reused = options.header['Idempotency-Key']
    return Promise.resolve({ has_code: false })
  }
  await first.page.retryJoinCode()
  assert.equal(reused, 'pending-within-window')

  const second = loadPage('pages/family/index', app, { createCanvasContext: () => ctx })
  second.page.data.isAdmin = true
  second.page.data.inviteSheet = true
  second.storage.set('joinCodePending:42', { key: 'expired-pending-key', at: Date.now() - 31 * 60 * 1000 })
  const methods = []
  const keys = []
  app.request = (url, options = {}) => {
    methods.push(options.method || 'GET')
    keys.push(options.header && options.header['Idempotency-Key'])
    return Promise.resolve({
      has_code: true, join_code: '004219', generation_id: 'a'.repeat(32),
      qr_content: 'IFAMI_JOIN_V1:004219:' + 'a'.repeat(32) + ':cred',
      expires_at: new Date(Date.now() + 3600000).toISOString()
    })
  }
  await second.page.retryJoinCode()
  assert.deepEqual(methods, ['GET'])
  assert.equal(keys.includes('expired-pending-key'), false)
})

;(async () => {
  let passed = 0
  for (const item of tests) {
    try {
      await item.fn()
      passed++
      console.log('ok - ' + item.name)
    } catch (error) {
      console.error('not ok - ' + item.name)
      console.error(error && error.stack || error)
      process.exitCode = 1
    }
  }
  console.log(`${passed}/${tests.length} behavior tests passed`)
})()
