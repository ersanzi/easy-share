//go:build windows

package clipboard

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// buildDIB24 构造 24bpp BI_RGB DIB：每行按 4 字节对齐（行尾填充字节填垃圾，
// 复现微信等来源的真实形态——按 w*3 紧凑读行的旧实现会被填充字节带偏）。
func buildDIB24(w, h int, topDown bool, px func(x, y int) (r, g, b byte)) []byte {
	stride := (w*3 + 3) &^ 3
	dib := make([]byte, 40+stride*h)
	binary.LittleEndian.PutUint32(dib[0:], 40)
	binary.LittleEndian.PutUint32(dib[4:], uint32(w))
	bh := int32(h)
	if topDown {
		bh = -bh
	}
	binary.LittleEndian.PutUint32(dib[8:], uint32(bh))
	binary.LittleEndian.PutUint16(dib[12:], 1)
	binary.LittleEndian.PutUint16(dib[14:], 24)
	for y := 0; y < h; y++ {
		srcY := y
		if !topDown {
			srcY = h - 1 - y // 存储行序与图像行序相反
		}
		row := dib[40+srcY*stride:]
		for x := 0; x < w; x++ {
			r, g, b := px(x, y)
			row[x*3], row[x*3+1], row[x*3+2] = b, g, r // BGR
		}
		for i := w * 3; i < stride; i++ {
			row[i] = 0xEE // 填充字节放垃圾：紧凑读行的实现会把它当像素
		}
	}
	return dib
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("解码 PNG: %v", err)
	}
	return img
}

func wantNRGBA(t *testing.T, img image.Image, x, y int, want color.NRGBA) {
	t.Helper()
	// png.Decode 可能返回 RGBA（预乘）模型：统一经 NRGBAModel 转换后比较，
	// 否则同值不同类型的像素会被类型断言误判。
	c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	if c != want {
		t.Errorf("像素 (%d,%d) = %v，期望 %v", x, y, c, want)
	}
}

// 24bpp 宽度非 4 倍数时行尾有填充：解析必须按对齐跨距取行，否则逐行漂移
// 成斜条纹 + 通道错位（微信复制图片损坏的根因）。
func TestDibToPNG24bppRowStride(t *testing.T) {
	const w, h = 5, 3 // 每行 15 字节，对齐到 16：填充 1 字节
	cases := []struct {
		name    string
		topDown bool
	}{
		{"bottom-up", false},
		{"top-down", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dib := buildDIB24(w, h, tc.topDown, func(x, y int) (r, g, b byte) {
				return byte(10 + x), byte(20 + y), byte(30 + x + y)
			})
			pngBytes, pw, ph, err := dibToPNG(dib)
			if err != nil {
				t.Fatalf("dibToPNG: %v", err)
			}
			if pw != w || ph != h {
				t.Fatalf("尺寸 %dx%d，期望 %dx%d", pw, ph, w, h)
			}
			img := decodePNG(t, pngBytes)
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					wantNRGBA(t, img, x, y, color.NRGBA{R: 10 + byte(x), G: 20 + byte(y), B: 30 + byte(x+y), A: 255})
				}
			}
		})
	}
}

// 32bpp 截图常见 alpha 全 0：应按不透明处理（A=255），否则显示成透明黑块。
func TestDibToPNG32bppOpaqueFallback(t *testing.T) {
	const w, h = 2, 2
	dib := make([]byte, 40+w*h*4)
	binary.LittleEndian.PutUint32(dib[0:], 40)
	binary.LittleEndian.PutUint32(dib[4:], w)
	bh := int32(-h) // top-down 用负高度
	binary.LittleEndian.PutUint32(dib[8:], uint32(bh))
	binary.LittleEndian.PutUint16(dib[12:], 1)
	binary.LittleEndian.PutUint16(dib[14:], 32)
	for i := 0; i < w*h; i++ {
		dib[40+i*4+0] = 0x11 // B
		dib[40+i*4+1] = 0x22 // G
		dib[40+i*4+2] = 0x33 // R
		dib[40+i*4+3] = 0x00 // alpha 全 0
	}
	pngBytes, pw, ph, err := dibToPNG(dib)
	if err != nil {
		t.Fatalf("dibToPNG: %v", err)
	}
	if pw != w || ph != h {
		t.Fatalf("尺寸 %dx%d，期望 %dx%d", pw, ph, w, h)
	}
	img := decodePNG(t, pngBytes)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			wantNRGBA(t, img, x, y, color.NRGBA{R: 0x33, G: 0x22, B: 0x11, A: 255})
		}
	}
}
