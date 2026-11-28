package notifier

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"sync"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/luolongfei/ColabOne/console/internal/brandicon"
)

const (
	colabOneMaxRawPreviewBytes  = 16 << 20
	colabOneMaxPreviewDimension = 16384
	colabOneMaxPreviewPixels    = int64(40_000_000)
)

var (
	colabOneTestPreviewOnce  sync.Once
	colabOneTestPreviewBytes []byte
)

// PreparePreviewImage decodes PNG, JPEG, WebP, or GIF image bytes, scales the image
// preserving aspect ratio when either dimension exceeds maxEdge, and encodes either
// PNG (when preferPNG is true and output fits maxBytes) or JPEG (stepping quality
// 88, 78, 68 and downscaling if necessary to stay within maxBytes).
func PreparePreviewImage(
	raw []byte,
	maxEdge int,
	maxBytes int,
	preferPNG bool,
) (data []byte, mimeType string, ext string, width int, height int, err error) {
	if len(raw) == 0 {
		return nil, "", "", 0, 0, errors.New("预览图像数据为空")
	}
	if len(raw) > colabOneMaxRawPreviewBytes {
		return nil, "", "", 0, 0, fmt.Errorf("预览图像数据过大，超出 %d MiB 限制", colabOneMaxRawPreviewBytes>>20)
	}
	cfg, _, cfgErr := image.DecodeConfig(bytes.NewReader(raw))
	if cfgErr != nil {
		return nil, "", "", 0, 0, fmt.Errorf("无法解码预览图像：%w", cfgErr)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > colabOneMaxPreviewDimension || cfg.Height > colabOneMaxPreviewDimension ||
		int64(cfg.Width)*int64(cfg.Height) > colabOneMaxPreviewPixels {
		return nil, "", "", 0, 0, errors.New("预览图像尺寸超出安全限制")
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", "", 0, 0, fmt.Errorf("无法解码预览图像：%w", err)
	}
	bounds := img.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	if srcW <= 0 || srcH <= 0 ||
		srcW > colabOneMaxPreviewDimension || srcH > colabOneMaxPreviewDimension ||
		int64(srcW)*int64(srcH) > colabOneMaxPreviewPixels {
		return nil, "", "", 0, 0, errors.New("预览图像尺寸无效")
	}

	cur := img
	w, h := srcW, srcH
	if maxEdge > 0 && (w > maxEdge || h > maxEdge) {
		dstW, dstH := colabOneScaleDims(w, h, maxEdge)
		cur = colabOneResizeImage(cur, dstW, dstH)
		w, h = dstW, dstH
	}

	if preferPNG {
		var pngBuf bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if encErr := enc.Encode(&pngBuf, cur); encErr == nil {
			if maxBytes <= 0 || pngBuf.Len() <= maxBytes {
				return pngBuf.Bytes(), "image/png", ".png", w, h, nil
			}
		}
	}

	baseForJPEG := cur
	if !colabOneIsOpaque(baseForJPEG) {
		baseForJPEG = colabOneFlattenToWhite(baseForJPEG)
	}

	qualities := []int{88, 78, 68}
	workImg := baseForJPEG
	workW, workH := w, h
	for round := 0; round < 8; round++ {
		for _, q := range qualities {
			var jpgBuf bytes.Buffer
			if encErr := jpeg.Encode(&jpgBuf, workImg, &jpeg.Options{Quality: q}); encErr != nil {
				return nil, "", "", 0, 0, fmt.Errorf("编码预览图像失败：%w", encErr)
			}
			if maxBytes <= 0 || jpgBuf.Len() <= maxBytes {
				return jpgBuf.Bytes(), "image/jpeg", ".jpg", workW, workH, nil
			}
		}
		if workW <= 64 && workH <= 64 {
			break
		}
		nextW := int(math.Round(float64(workW) * 0.72))
		nextH := int(math.Round(float64(workH) * 0.72))
		if nextW < 1 {
			nextW = 1
		}
		if nextH < 1 {
			nextH = 1
		}
		if workW > 1 && nextW >= workW {
			nextW = workW - 1
		}
		if workH > 1 && nextH >= workH {
			nextH = workH - 1
		}
		// Always scale from original baseForJPEG to prevent multi-generation bilinear blur.
		workImg = colabOneResizeImage(baseForJPEG, nextW, nextH)
		workW, workH = nextW, nextH
	}

	return nil, "", "", 0, 0, fmt.Errorf("预览图像压缩后仍超出大小限制 (%d bytes)", maxBytes)
}

func colabOneIsOpaque(img image.Image) bool {
	if img == nil {
		return false
	}
	if op, ok := img.(interface{ Opaque() bool }); ok {
		return op.Opaque()
	}
	return false
}

func colabOneScaleDims(w, h, maxEdge int) (int, int) {
	if maxEdge <= 0 || (w <= maxEdge && h <= maxEdge) {
		return w, h
	}
	var scale float64
	if w >= h {
		scale = float64(maxEdge) / float64(w)
	} else {
		scale = float64(maxEdge) / float64(h)
	}
	dstW := int(math.Round(float64(w) * scale))
	dstH := int(math.Round(float64(h) * scale))
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	if dstW > maxEdge {
		dstW = maxEdge
	}
	if dstH > maxEdge {
		dstH = maxEdge
	}
	return dstW, dstH
}

func colabOneResizeImage(src image.Image, dstW, dstH int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}

func colabOneFlattenToWhite(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	xdraw.Draw(dst, dst.Bounds(), image.NewUniform(color.RGBA{R: 255, G: 255, B: 255, A: 255}), image.Point{}, xdraw.Src)
	xdraw.Draw(dst, dst.Bounds(), src, b.Min, xdraw.Over)
	return dst
}

// BuildTestPreviewCardPNG generates a compact, crisp 640x360 brand preview card
// PNG used by TestChannel so users can verify rich visual delivery across channels.
func BuildTestPreviewCardPNG() []byte {
	colabOneTestPreviewOnce.Do(func() {
		colabOneTestPreviewBytes = colabOneRenderTestPreviewCardPNG()
	})
	if len(colabOneTestPreviewBytes) == 0 {
		return nil
	}
	cp := make([]byte, len(colabOneTestPreviewBytes))
	copy(cp, colabOneTestPreviewBytes)
	return cp
}

func colabOneRenderTestPreviewCardPNG() []byte {
	const width = 640
	const height = 360

	canvas := image.NewRGBA(image.Rect(0, 0, width, height))

	// Subtle Apple-inspired ambient gradient canvas (#F5F6FA with soft radial bloom).
	cx1, cy1 := float64(width)*0.22, float64(height)*0.24
	cx2, cy2 := float64(width)*0.80, float64(height)*0.78
	for y := 0; y < height; y++ {
		fy := float64(y) / float64(height-1)
		for x := 0; x < width; x++ {
			fx := float64(x) / float64(width-1)
			r := 244.0 - 12.0*fy + 6.0*fx
			g := 246.0 - 10.0*fy + 4.0*fx
			b := 251.0 - 6.0*fy + 3.0*fx

			d1 := math.Hypot(float64(x)-cx1, float64(y)-cy1) / 300.0
			if d1 < 1.0 {
				w := (1.0 - d1) * (1.0 - d1)
				r -= 14.0 * w
				g -= 4.0 * w
				b += 14.0 * w
			}
			d2 := math.Hypot(float64(x)-cx2, float64(y)-cy2) / 320.0
			if d2 < 1.0 {
				w := (1.0 - d2) * (1.0 - d2)
				r -= 16.0 * w
				g += 8.0 * w
				b -= 2.0 * w
			}

			canvas.SetRGBA(x, y, color.RGBA{
				R: colabOneClampByte(r),
				G: colabOneClampByte(g),
				B: colabOneClampByte(b),
				A: 255,
			})
		}
	}

	// Centered elevated white rounded card tile.
	colabOneFillRoundedRect(canvas, image.Rect(140, 56, 500, 304), 28, color.NRGBA{R: 255, G: 255, B: 255, A: 248})
	colabOneStrokeRoundedRect(canvas, image.Rect(140, 56, 500, 304), 28, color.NRGBA{R: 226, G: 229, B: 236, A: 255})

	// Subtle decorative status bar & accent pills inside the card.
	colabOneFillRoundedRect(canvas, image.Rect(240, 232, 400, 246), 7, color.NRGBA{R: 29, G: 29, B: 31, A: 225})
	colabOneFillRoundedRect(canvas, image.Rect(268, 258, 372, 268), 5, color.NRGBA{R: 21, G: 122, B: 62, A: 205})

	// Composite the embedded ColabOne brand icon centered above the accent bars.
	iconRaw := brandicon.AppleTouchIconPNGBytes()
	if iconImg, err := png.Decode(bytes.NewReader(iconRaw)); err == nil {
		const iconSize = 112
		scaled := colabOneResizeImage(iconImg, iconSize, iconSize)
		destRect := image.Rect((width-iconSize)/2, 92, (width+iconSize)/2, 92+iconSize)
		colabOneBlitRounded(canvas, destRect, scaled, 24)
	}

	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, canvas); err != nil {
		return iconRaw
	}
	return buf.Bytes()
}

func colabOneClampByte(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func colabOneRoundedMaskAlpha(x, y int, rect image.Rectangle, radius float64) float64 {
	lx := float64(x - rect.Min.X)
	ly := float64(y - rect.Min.Y)
	w := float64(rect.Dx())
	h := float64(rect.Dy())

	dx := 0.0
	if lx < radius {
		dx = radius - lx
	} else if lx > w-1-radius {
		dx = lx - (w - 1 - radius)
	}
	dy := 0.0
	if ly < radius {
		dy = radius - ly
	} else if ly > h-1-radius {
		dy = ly - (h - 1 - radius)
	}
	if dx == 0 && dy == 0 {
		return 1.0
	}
	dist := math.Hypot(dx, dy)
	if dist <= radius-0.5 {
		return 1.0
	}
	if dist >= radius+0.5 {
		return 0.0
	}
	return radius + 0.5 - dist
}

func colabOneBlendPixel(dst *image.RGBA, x, y int, src color.NRGBA, coverage float64) {
	if coverage <= 0 {
		return
	}
	sa := (float64(src.A) / 255.0) * coverage
	if sa <= 0 {
		return
	}
	bg := dst.RGBAAt(x, y)
	inv := 1.0 - sa
	dst.SetRGBA(x, y, color.RGBA{
		R: colabOneClampByte(float64(src.R)*sa + float64(bg.R)*inv),
		G: colabOneClampByte(float64(src.G)*sa + float64(bg.G)*inv),
		B: colabOneClampByte(float64(src.B)*sa + float64(bg.B)*inv),
		A: 255,
	})
}

func colabOneBlendPremulPixel(dst *image.RGBA, x, y int, src color.RGBA, coverage float64) {
	if coverage <= 0 {
		return
	}
	sa := (float64(src.A) / 255.0) * coverage
	if sa <= 0 {
		return
	}
	bg := dst.RGBAAt(x, y)
	inv := 1.0 - sa
	dst.SetRGBA(x, y, color.RGBA{
		R: colabOneClampByte(float64(src.R)*coverage + float64(bg.R)*inv),
		G: colabOneClampByte(float64(src.G)*coverage + float64(bg.G)*inv),
		B: colabOneClampByte(float64(src.B)*coverage + float64(bg.B)*inv),
		A: 255,
	})
}

func colabOneFillRoundedRect(dst *image.RGBA, rect image.Rectangle, radius float64, c color.NRGBA) {
	clipped := rect.Intersect(dst.Bounds())
	for y := clipped.Min.Y; y < clipped.Max.Y; y++ {
		for x := clipped.Min.X; x < clipped.Max.X; x++ {
			cov := colabOneRoundedMaskAlpha(x, y, rect, radius)
			if cov > 0 {
				colabOneBlendPixel(dst, x, y, c, cov)
			}
		}
	}
}

func colabOneStrokeRoundedRect(dst *image.RGBA, rect image.Rectangle, radius float64, c color.NRGBA) {
	inner := image.Rect(rect.Min.X+1, rect.Min.Y+1, rect.Max.X-1, rect.Max.Y-1)
	innerRadius := radius - 1
	if innerRadius < 0 {
		innerRadius = 0
	}
	clipped := rect.Intersect(dst.Bounds())
	for y := clipped.Min.Y; y < clipped.Max.Y; y++ {
		for x := clipped.Min.X; x < clipped.Max.X; x++ {
			outerCov := colabOneRoundedMaskAlpha(x, y, rect, radius)
			if outerCov <= 0 {
				continue
			}
			innerCov := 0.0
			if x >= inner.Min.X && x < inner.Max.X && y >= inner.Min.Y && y < inner.Max.Y {
				innerCov = colabOneRoundedMaskAlpha(x, y, inner, innerRadius)
			}
			ring := outerCov - innerCov
			if ring > 0 {
				colabOneBlendPixel(dst, x, y, c, ring)
			}
		}
	}
}

func colabOneBlitRounded(dst *image.RGBA, rect image.Rectangle, src *image.RGBA, radius float64) {
	clipped := rect.Intersect(dst.Bounds())
	for y := clipped.Min.Y; y < clipped.Max.Y; y++ {
		for x := clipped.Min.X; x < clipped.Max.X; x++ {
			cov := colabOneRoundedMaskAlpha(x, y, rect, radius)
			if cov <= 0 {
				continue
			}
			sx := x - rect.Min.X
			sy := y - rect.Min.Y
			if sx < 0 || sy < 0 || sx >= src.Bounds().Dx() || sy >= src.Bounds().Dy() {
				continue
			}
			colabOneBlendPremulPixel(dst, x, y, src.RGBAAt(src.Bounds().Min.X+sx, src.Bounds().Min.Y+sy), cov)
		}
	}
}
