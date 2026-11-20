const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const jsQR = require('/tmp/ifami_join_validation_tools/node_modules/jsqr')

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
