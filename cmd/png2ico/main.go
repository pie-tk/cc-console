package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: png2ico <input.png> [output.ico]")
		os.Exit(1)
	}

	pngPath := os.Args[1]
	icoPath := "icon.ico"
	if len(os.Args) >= 3 {
		icoPath = os.Args[2]
	}

	data, err := os.ReadFile(pngPath)
	if err != nil {
		fmt.Println("读取失败:", err)
		os.Exit(1)
	}

	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		fmt.Println("PNG 解码失败:", err)
		os.Exit(1)
	}
	fmt.Printf("原始: %dx%d (%d 字节)\n", src.Bounds().Dx(), src.Bounds().Dy(), len(data))

	// ICO 标准尺寸：生成多个分辨率打包到一个 .ico 中
	sizes := []int{256, 48, 32, 16}
	var entries []icoEntry
	var imageDatas [][]byte

	for _, sz := range sizes {
		if sz > src.Bounds().Dx() && sz > src.Bounds().Dy() {
			continue // 源图比目标还小时跳过
		}
		// 若源图不是正方形，取短边等比缩放
		rs := resizeToSquare(src, sz)
		var buf bytes.Buffer
		if err := png.Encode(&buf, rs); err != nil {
			fmt.Printf("PNG 编码 %dx%d 失败: %v\n", sz, sz, err)
			continue
		}

		w, h := uint8(sz), uint8(sz)
		if sz >= 256 {
			w, h = 0, 0 // ICO 规范：0 表示 256
		}
		entries = append(entries, icoEntry{
			w: w, h: h, bpp: 32, size: uint32(buf.Len()),
		})
		imageDatas = append(imageDatas, buf.Bytes())
		fmt.Printf("  → %dx%d PNG (%d 字节)\n", sz, sz, buf.Len())
	}

	if len(entries) == 0 {
		fmt.Println("没有可生成的尺寸")
		os.Exit(1)
	}

	var out bytes.Buffer

	// ICO 头：reserved(2) + type(2) + count(2)
	binary.Write(&out, binary.LittleEndian, uint16(0))
	binary.Write(&out, binary.LittleEndian, uint16(1)) // ICO
	binary.Write(&out, binary.LittleEndian, uint16(len(entries)))

	// 计算各图像偏移
	offset := uint32(6 + 16*len(entries))
	for i := range entries {
		entries[i].offset = offset
		offset += entries[i].size
	}

	// 写入目录项
	for _, e := range entries {
		binary.Write(&out, binary.LittleEndian, e.w)
		binary.Write(&out, binary.LittleEndian, e.h)
		binary.Write(&out, binary.LittleEndian, uint8(0))  // palette
		binary.Write(&out, binary.LittleEndian, uint8(0))  // reserved
		binary.Write(&out, binary.LittleEndian, uint16(1)) // planes
		binary.Write(&out, binary.LittleEndian, e.bpp)
		binary.Write(&out, binary.LittleEndian, e.size)
		binary.Write(&out, binary.LittleEndian, e.offset)
	}

	// 写入图像数据
	for _, d := range imageDatas {
		out.Write(d)
	}

	if err := os.WriteFile(icoPath, out.Bytes(), 0644); err != nil {
		fmt.Println("写入失败:", err)
		os.Exit(1)
	}
	fmt.Printf("✓ 已生成: %s (%d 字节, %d 张图)\n", icoPath, out.Len(), len(entries))
}

type icoEntry struct {
	w, h                 uint8
	bpp                  uint16
	size, offset         uint32
}

// resizeToSquare 将图像等比缩放并居中裁剪为正方形，再缩放到目标尺寸。
func resizeToSquare(src image.Image, targetSize int) *image.RGBA {
	srcW := src.Bounds().Dx()
	srcH := src.Bounds().Dy()

	// 先裁剪为正方形（取中心区域）
	var crop image.Image
	if srcW > srcH {
		left := (srcW - srcH) / 2
		crop = src.(interface {
			SubImage(r image.Rectangle) image.Image
		}).SubImage(image.Rect(left, 0, left+srcH, srcH))
	} else if srcH > srcW {
		top := (srcH - srcW) / 2
		crop = src.(interface {
			SubImage(r image.Rectangle) image.Image
		}).SubImage(image.Rect(0, top, srcW, top+srcW))
	} else {
		crop = src
	}

	// 缩放到目标尺寸
	dst := image.NewRGBA(image.Rect(0, 0, targetSize, targetSize))
	cropW := crop.Bounds().Dx()
	cropH := crop.Bounds().Dy()

	for y := 0; y < targetSize; y++ {
		for x := 0; x < targetSize; x++ {
			sx := float64(x) * float64(cropW-1) / float64(targetSize-1)
			sy := float64(y) * float64(cropH-1) / float64(targetSize-1)
			dst.Set(x, y, bilinear(crop, sx, sy))
		}
	}

	return dst
}

// bilinear 双线性插值采样。
func bilinear(img image.Image, x, y float64) color.Color {
	x0 := int(x)
	y0 := int(y)
	x1 := x0 + 1
	y1 := y0 + 1

	bounds := img.Bounds()
	if x1 >= bounds.Max.X {
		x1 = bounds.Max.X - 1
	}
	if y1 >= bounds.Max.Y {
		y1 = bounds.Max.Y - 1
	}

	fx := x - float64(x0)
	fy := y - float64(y0)

	c00 := rgbaColor(img.At(x0, y0))
	c10 := rgbaColor(img.At(x1, y0))
	c01 := rgbaColor(img.At(x0, y1))
	c11 := rgbaColor(img.At(x1, y1))

	// 双线性插值
	blend := func(a, b float64, t float64) float64 { return a + (b-a)*t }
	topR := blend(float64(c00.R), float64(c10.R), fx)
	botR := blend(float64(c01.R), float64(c11.R), fx)
	topG := blend(float64(c00.G), float64(c10.G), fx)
	botG := blend(float64(c01.G), float64(c11.G), fx)
	topB := blend(float64(c00.B), float64(c10.B), fx)
	botB := blend(float64(c01.B), float64(c11.B), fx)
	topA := blend(float64(c00.A), float64(c10.A), fx)
	botA := blend(float64(c01.A), float64(c11.A), fx)

	return color.RGBA{
		R: uint8(blend(topR, botR, fy) + 0.5),
		G: uint8(blend(topG, botG, fy) + 0.5),
		B: uint8(blend(topB, botB, fy) + 0.5),
		A: uint8(blend(topA, botA, fy) + 0.5),
	}
}

// rgbaColor 返回 0-255 的 RGBA 分量。
func rgbaColor(c color.Color) struct{ R, G, B, A uint32 } {
	r, g, b, a := c.RGBA()
	return struct{ R, G, B, A uint32 }{r >> 8, g >> 8, b >> 8, a >> 8}
}

