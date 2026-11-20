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

function drawCanvas(canvasId, content, size = 420, quietZone = 4) {
  const matrix = createMatrix(content)
  const count = matrix.length
  const total = count + quietZone * 2
  const cell = size / total
  const ctx = wx.createCanvasContext(canvasId)
  ctx.setFillStyle('#FFFFFF')
  ctx.fillRect(0, 0, size, size)
  ctx.setFillStyle('#1F1D1A')
  matrix.forEach((row, y) => row.forEach((dark, x) => {
    if (!dark) return
    const left = Math.floor((x + quietZone) * cell)
    const top = Math.floor((y + quietZone) * cell)
    const right = Math.ceil((x + quietZone + 1) * cell)
    const bottom = Math.ceil((y + quietZone + 1) * cell)
    ctx.fillRect(left, top, right - left, bottom - top)
  }))
  ctx.draw()
  return matrix
}

module.exports = { createMatrix, drawCanvas }
