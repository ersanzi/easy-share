// 汇报卡 PDF 导出：把 canvas 直接嵌进 A4 页面。
// 刻意不引 jspdf（约 300KB，只为塞一张图）——单页 PDF 结构很短，
// 这里手写 PDF 对象（图片 XObject + 一条内容流），浏览器端零依赖、体积只有图本身。
// 图片用 FlateDecode：canvas 的 toBlob('image/png') 出来是经过 IDAT 压缩的 zlib 流，
// 与 PDF 的 /FlateDecode 同一编码，剥掉 PNG 头尾即可直接当图像流用。

function encodeAscii(text: string): Uint8Array {
  const out = new Uint8Array(text.length)
  for (let i = 0; i < text.length; i += 1) out[i] = text.charCodeAt(i) & 0xff
  return out
}

/** 从 canvas 取像素并编码成 PNG 的 IDAT 数据（zlib 流）与尺寸。 */
export function canvasToFlateData(canvas: HTMLCanvasElement): { data: Uint8Array; width: number; height: number } {
  const bytes = canvas.toDataURL('image/png')
  const binary = atob(bytes.slice(bytes.indexOf(',') + 1))
  const png = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) png[i] = binary.charCodeAt(i)
  // 解析 PNG 块：拼接所有 IDAT 的负载，跳过 IHDR/IEND 等元数据块
  const parts: Uint8Array[] = []
  let total = 0
  let offset = 8
  while (offset < png.length) {
    const length = ((png[offset] << 24) | (png[offset + 1] << 16) | (png[offset + 2] << 8) | png[offset + 3]) >>> 0
    const type = String.fromCharCode(png[offset + 4], png[offset + 5], png[offset + 6], png[offset + 7])
    if (type === 'IDAT') {
      const chunk = png.subarray(offset + 8, offset + 8 + length)
      parts.push(chunk)
      total += chunk.length
    }
    if (type === 'IEND') break
    offset += 12 + length
  }
  const data = new Uint8Array(total)
  let cursor = 0
  for (const part of parts) {
    data.set(part, cursor)
    cursor += part.length
  }
  return { data, width: canvas.width, height: canvas.height }
}

/** 单页 A4 PDF（595.28x841.89pt），图片按宽度铺满、超出部分裁掉。 */
export function buildA4Pdf(image: { data: Uint8Array; width: number; height: number }): Blob {
  const pageWidth = 595.28
  const pageHeight = 841.89
  const drawHeight = Math.min((image.height / image.width) * pageWidth, pageHeight)
  const top = pageHeight - drawHeight

  const chunks: (Uint8Array | string)[] = []
  const offsets: number[] = []
  let length = 0
  const push = (data: Uint8Array | string) => {
    chunks.push(data)
    length += typeof data === 'string' ? data.length : data.length
  }
  const addObject = (body: Uint8Array | string) => {
    offsets.push(length)
    push(`${offsets.length} 0 obj\n`)
    push(body)
    push('\nendobj\n')
  }

  push('%PDF-1.4\n')
  push(new Uint8Array([0x25, 0xe2, 0xe3, 0xcf, 0xd3, 0x0a]))

  // 1 Catalog / 2 Pages / 3 Page / 4 内容流 / 5 图像
  addObject('<< /Type /Catalog /Pages 2 0 R >>')
  addObject('<< /Type /Pages /Kids [3 0 R] /Count 1 >>')
  addObject(
    `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 ${pageWidth} ${pageHeight}] `
      + '/Resources << /XObject << /Img1 5 0 R >> >> /Contents 4 0 R >>',
  )
  const content = `q\n${pageWidth} 0 0 ${drawHeight} 0 ${top} cm\n/Img1 Do\nQ\n`
  addObject(`<< /Length ${content.length} >>\nstream\n${content}endstream`)

  offsets.push(length)
  push(`${offsets.length} 0 obj\n`)
  push(
    `<< /Type /XObject /Subtype /Image /Width ${image.width} /Height ${image.height} `
      + '/ColorSpace /DeviceRGB /BitsPerComponent 8 '
      + `/Filter /FlateDecode /Length ${image.data.length} >>\nstream\n`,
  )
  push(image.data)
  push('\nendstream\nendobj\n')

  const xrefStart = length
  let xref = `xref\n0 ${offsets.length + 1}\n0000000000 65535 f \n`
  for (const offset of offsets) xref += `${String(offset).padStart(10, '0')} 00000 n \n`
  push(xref)
  push(`trailer\n<< /Size ${offsets.length + 1} /Root 1 0 R >>\nstartxref\n${xrefStart}\n%%EOF`)

  return new Blob(chunks as BlobPart[], { type: 'application/pdf' })
}
