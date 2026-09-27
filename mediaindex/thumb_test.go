package mediaindex

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// solid 单色图，用来拼出可判定的测试素材。
func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// near 比较 16 位采样值与 8 位期望值，给 JPEG 有损压缩留一点容差。
func near(a uint32, b uint8) bool {
	d := int(a>>8) - int(b)
	return d >= -12 && d <= 12
}

func assertColor(t *testing.T, img image.Image, x, y int, want color.RGBA, what string) {
	t.Helper()
	r, g, b, _ := img.At(x, y).RGBA()
	if !near(r, want.R) || !near(g, want.G) || !near(b, want.B) {
		t.Errorf("%s (%d,%d) = (%d,%d,%d)，want (%d,%d,%d)",
			what, x, y, r>>8, g>>8, b>>8, want.R, want.G, want.B)
	}
}

var (
	red   = color.RGBA{R: 255, A: 255}
	green = color.RGBA{G: 255, A: 255}
	blue  = color.RGBA{B: 255, A: 255}
	white = color.RGBA{R: 255, G: 255, B: 255, A: 255}
)

// TestCoverCropScalesQuadrants 4:3 源图缩到 400×300：四角必须落在源图对应的四个角上。
//
// 回归用例。原先的实现用 image/draw 的 Draw 冒充「等比放大」，而 Draw 是 1:1 像素拷贝、
// **根本不缩放**，于是结果变成"从源图左上角抠走一块原尺寸像素"：下边和右边的内容全丢了。
// 这里四角颜色各不相同，任何"只取左上角"的实现都会立刻暴露。
func TestCoverCropScalesQuadrants(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 1600; x++ {
			c := red
			switch {
			case x >= 800 && y < 600:
				c = green
			case x < 800 && y >= 600:
				c = blue
			case x >= 800 && y >= 600:
				c = white
			}
			src.Set(x, y, c)
		}
	}
	got := coverCrop(src, CoverWidth, CoverHeight)
	if b := got.Bounds(); b.Dx() != CoverWidth || b.Dy() != CoverHeight {
		t.Fatalf("尺寸 = %dx%d，want %dx%d", b.Dx(), b.Dy(), CoverWidth, CoverHeight)
	}
	assertColor(t, got, 0, 0, red, "左上")
	assertColor(t, got, CoverWidth-1, 0, green, "右上")
	assertColor(t, got, 0, CoverHeight-1, blue, "左下")
	assertColor(t, got, CoverWidth-1, CoverHeight-1, white, "右下")
}

// TestCoverCrop16to9KeepsFullHeight 16:9 源图（剧照就是 16:9）转 4:3：只该裁掉左右，
// 高度完整保留，四个角都必须来自画面里那 4:3 的中间区域（左右两侧的绿边必须被裁掉）。
func TestCoverCrop16to9KeepsFullHeight(t *testing.T) {
	const sw, sh = 800, 450
	src := image.NewRGBA(image.Rect(0, 0, sw, sh))
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			c := blue
			if x < 100 || x >= sw-100 { // 左右各 100px 绿边：4:3 窗口是 x∈[100,700)，应全部裁掉
				c = green
			}
			src.Set(x, y, c)
		}
	}
	got := coverCrop(src, CoverWidth, CoverHeight)
	for _, p := range [][2]int{{0, 0}, {CoverWidth - 1, 0}, {0, CoverHeight - 1}, {CoverWidth - 1, CoverHeight - 1}} {
		assertColor(t, got, p[0], p[1], blue, "16:9 转 4:3 的四角")
	}
	// 整幅都不该有绿：出现绿说明裁取窗口偏了（旧实现会取到源图左上角那片）
	for y := 0; y < CoverHeight; y++ {
		for x := 0; x < CoverWidth; x++ {
			r, g, b, _ := got.At(x, y).RGBA()
			if g > r+40 && g > b+40 {
				t.Fatalf("(%d,%d) 取到了左右绿边，说明裁剪窗口没居中", x, y)
			}
		}
	}
}

// TestCoverCropUpscalesTinySource 小图也要放大到 400×300（不能原样返回小图）。
func TestCoverCropUpscalesTinySource(t *testing.T) {
	got := coverCrop(solid(100, 75, red), CoverWidth, CoverHeight)
	if b := got.Bounds(); b.Dx() != CoverWidth || b.Dy() != CoverHeight {
		t.Fatalf("尺寸 = %dx%d，want %dx%d", b.Dx(), b.Dy(), CoverWidth, CoverHeight)
	}
	assertColor(t, got, 0, 0, red, "放大后左上")
	assertColor(t, got, CoverWidth-1, CoverHeight-1, red, "放大后右下")
}

// TestResizeCoverPNGFromWideJPEG 走真实入口（JPEG 解码 → 裁剪缩放 → PNG 编码）：
// 16:9 剧照的左右边缘要被裁掉，画面不能被"只取左上角"截断。
// 蒙版是一圈红边：旧实现在右侧会取到源图之外，编码后成透明黑，右边缘直接变黑。
func TestResizeCoverPNGFromWideJPEG(t *testing.T) {
	const sw, sh = 320, 180
	src := image.NewRGBA(image.Rect(0, 0, sw, sh))
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			c := blue
			if x < 40 { // 左侧 12.5%：正好落在 4:3 窗口之外
				c = red
			}
			src.Set(x, y, c)
		}
	}
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, src, nil); err != nil {
		t.Fatal(err)
	}
	data, err := ResizeCoverPNG(raw.Bytes())
	if err != nil {
		t.Fatalf("ResizeCoverPNG 失败: %v", err)
	}
	img, err := pngDecode(data)
	if err != nil {
		t.Fatalf("产物不是可解码 PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != CoverWidth || b.Dy() != CoverHeight {
		t.Fatalf("尺寸 = %dx%d，want %dx%d", b.Dx(), b.Dy(), CoverWidth, CoverHeight)
	}
	assertColor(t, img, 0, 0, blue, "左边缘")
	assertColor(t, img, CoverWidth-1, 0, blue, "右边缘")
	assertColor(t, img, CoverWidth/2, CoverHeight/2, blue, "中心")
}

// pngDecode 单独抽出来只为让上面的断言读起来短一点。
func pngDecode(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}
