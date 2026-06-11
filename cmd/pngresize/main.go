package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("用法: pngresize <input.png> <output.png> [size]")
		os.Exit(1)
	}

	srcPath := os.Args[1]
	dstPath := os.Args[2]
	size := 64
	if len(os.Args) >= 4 {
		fmt.Sscanf(os.Args[3], "%d", &size)
	}

	f, err := os.Open(srcPath)
	if err != nil {
		fmt.Println("打开失败:", err)
		os.Exit(1)
	}
	defer f.Close()

	src, err := png.Decode(f)
	if err != nil {
		fmt.Println("解码失败:", err)
		os.Exit(1)
	}
	fmt.Printf("原始: %dx%d\n", src.Bounds().Dx(), src.Bounds().Dy())

	dst := resizeToSquare(src, size)

	out, _ := os.Create(dstPath)
	defer out.Close()
	png.Encode(out, dst)
	fmt.Printf("已生成: %s (%dx%d)\n", dstPath, size, size)
}

func resizeToSquare(src image.Image, sz int) *image.RGBA {
	srcW := src.Bounds().Dx()
	srcH := src.Bounds().Dy()

	// 中心裁剪
	var crop image.Image
	if srcW > srcH {
		left := (srcW - srcH) / 2
		crop = src.(interface{ SubImage(r image.Rectangle) image.Image }).SubImage(image.Rect(left, 0, left+srcH, srcH))
	} else if srcH > srcW {
		top := (srcH - srcW) / 2
		crop = src.(interface{ SubImage(r image.Rectangle) image.Image }).SubImage(image.Rect(0, top, srcW, top+srcW))
	} else {
		crop = src
	}

	dst := image.NewRGBA(image.Rect(0, 0, sz, sz))
	cropW := crop.Bounds().Dx()
	cropH := crop.Bounds().Dy()

	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			sx := float64(x) * float64(cropW-1) / float64(sz-1)
			sy := float64(y) * float64(cropH-1) / float64(sz-1)
			dst.Set(x, y, bilinear(crop, sx, sy))
		}
	}
	return dst
}

func bilinear(img image.Image, x, y float64) color.Color {
	x0 := int(x)
	y0 := int(y)
	b := img.Bounds()
	x1 := x0 + 1
	y1 := y0 + 1
	if x1 >= b.Max.X {
		x1 = b.Max.X - 1
	}
	if y1 >= b.Max.Y {
		y1 = b.Max.Y - 1
	}

	fx := x - float64(x0)
	fy := y - float64(y0)

	blend := func(a, b float64, t float64) float64 { return a + (b-a)*t }

	c00 := toRGBA(img.At(x0, y0))
	c10 := toRGBA(img.At(x1, y0))
	c01 := toRGBA(img.At(x0, y1))
	c11 := toRGBA(img.At(x1, y1))

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

type rgba struct{ R, G, B, A uint32 }

func toRGBA(c color.Color) rgba {
	r, g, b, a := c.RGBA()
	return rgba{r >> 8, g >> 8, b >> 8, a >> 8}
}
