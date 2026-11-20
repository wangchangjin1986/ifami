// qrcode-generator 1.4.4 (MIT) 的小程序包装层。
// 上游源码与许可固定在 vendor/qrcode-generator，二维码完全在本地生成。
const qrcode = require('../vendor/qrcode-generator/qrcode')

qrcode.stringToBytes = qrcode.stringToBytesFuncs['UTF-8']

function createMatrix(content) {
  if (typeof content !== 'string' || !content) throw new Error('二维码内容不能为空')
  const qr = qrcode(0, 'M')
  qr.addData(content, 'Byte')
  qr.make()
  const count = qr.getModuleCount()
  return Array.from({ length: count }, (_, row) =>
    Array.from({ length: count }, (_, col) => qr.isDark(row, col)))
}

function canvasPixelSize(windowWidth, canvasRpx = 420) {
  const width = Number(windowWidth)
  if (!Number.isFinite(width) || width <= 0) return 210
  // rpx 按 750 设计宽换算；向下取整确保任何亚像素布局都不会裁掉右/下 quiet zone。
  return Math.max(1, Math.floor(width * canvasRpx / 750))
}

function drawCanvas(canvasId, content, size = 420, quietZone = 4) {
  const matrix = createMatrix(content)
  const count = matrix.length
  const total = count + quietZone * 2
  // 二维码模块必须落在完整物理像素上；分数模块逐格取整会让相邻模块宽度不一致，
  // 高密度码在小画布上会因此无法识别。将含 quiet zone 的整体居中，余量留白。
  const cell = Math.floor(size / total)
  if (cell < 1) throw new Error('二维码画布过小')
  const offset = Math.floor((size - total * cell) / 2) + quietZone * cell
  const ctx = wx.createCanvasContext(canvasId)
  ctx.setFillStyle('#FFFFFF')
  ctx.fillRect(0, 0, size, size)
  ctx.setFillStyle('#1F1D1A')
  matrix.forEach((row, y) => row.forEach((dark, x) => {
    if (!dark) return
    ctx.fillRect(offset + x * cell, offset + y * cell, cell, cell)
  }))
  ctx.draw()
  return matrix
}

module.exports = { createMatrix, canvasPixelSize, drawCanvas }
