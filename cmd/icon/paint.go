package main

import (
	"image"
	"image/color"
	"image/draw"
	"slices"

	"golang.org/x/image/vector"
)

// size 是画布的边长，也是输出图片的像素数；mygo 按需再缩小。
const size = 1024

// canvas 是一张画布，连同往上填形状用的光栅器。
type canvas struct {
	img *image.RGBA
	z   *vector.Rasterizer
}

func newCanvas() *canvas {
	return &canvas{
		img: image.NewRGBA(image.Rect(0, 0, size, size)),
		z:   vector.NewRasterizer(size, size),
	}
}

// trace 把多边形加进光栅器。光栅器按带符号的面积累加：实心的统一成正向，孔反向，
// 同向的形状相叠不会变深，反向的才会抵消。
func (c *canvas) trace(p poly, hole bool) {
	p = slices.Clone(p)
	if (area(p) > 0) == hole {
		slices.Reverse(p)
	}
	for i, v := range p {
		if i == 0 {
			c.z.MoveTo(float32(v.x), float32(v.y))
		} else {
			c.z.LineTo(float32(v.x), float32(v.y))
		}
	}
	c.z.ClosePath()
}

// fill 用 src 填充 shapes，holes 是其中挖掉的部分。
func (c *canvas) fill(src image.Image, shapes []poly, holes ...poly) {
	c.z.Reset(size, size)
	for _, p := range shapes {
		c.trace(p, false)
	}
	for _, p := range holes {
		c.trace(p, true)
	}
	c.z.Draw(c.img, c.img.Bounds(), src, image.Point{})
}

// mask 把形状画成覆盖率遮罩。
func (c *canvas) mask(shapes ...poly) *image.Alpha {
	m := image.NewAlpha(c.img.Bounds())
	c.z.Reset(size, size)
	for _, p := range shapes {
		c.trace(p, false)
	}
	c.z.Draw(m, m.Bounds(), image.NewUniform(color.Alpha{255}), image.Point{})
	return m
}

// shade 画 shapes 平移 (dx, dy) 后投下的软影，模糊半径为 r；clip 非空时只画在它之内。
func (c *canvas) shade(
	col color.NRGBA, dx, dy float64, r int, clip *image.Alpha, shapes ...poly,
) {
	moved := make([]poly, len(shapes))
	for i, p := range shapes {
		moved[i] = shift(p, dx, dy)
	}
	m := c.mask(moved...)
	blur(m, r)
	for i, v := range m.Pix {
		a := int(v) * int(col.A) / 255
		if clip != nil {
			a = a * int(clip.Pix[i]) / 255
		}
		m.Pix[i] = uint8(a)
	}
	src := image.NewUniform(color.RGBA{col.R, col.G, col.B, 255})
	draw.DrawMask(c.img, c.img.Bounds(), src, image.Point{}, m, image.Point{}, draw.Over)
}

// blur 用三遍盒式模糊近似半径为 r 的高斯模糊。
func blur(a *image.Alpha, r int) {
	w, h := a.Bounds().Dx(), a.Bounds().Dy()
	src, tmp := make([]float32, w*h), make([]float32, w*h)
	for i, v := range a.Pix {
		src[i] = float32(v)
	}
	for range 3 {
		box(tmp, src, w, r, 1, h, w) // 横向
		box(src, tmp, h, r, w, w, 1) // 纵向
	}
	for i, v := range src {
		a.Pix[i] = uint8(v + 0.5)
	}
}

// box 对 lines 条线各做一遍宽 2r+1 的均值：每条线有 n 个样本，样本间隔 stride，
// 线与线间隔 lineStride；越界取边缘的值。
func box(dst, src []float32, n, r, stride, lines, lineStride int) {
	width := float32(2*r + 1)
	for l := range lines {
		base := l * lineStride
		at := func(i int) float32 { return src[base+min(max(i, 0), n-1)*stride] }
		var sum float32
		for i := -r; i <= r; i++ {
			sum += at(i)
		}
		for i := range n {
			dst[base+i*stride] = sum / width
			sum += at(i+r+1) - at(i-r)
		}
	}
}

// pixels 是由函数逐点给出颜色的图，用作渐变的来源。
type pixels func(x, y int) color.Color

func (pixels) ColorModel() color.Model   { return color.RGBAModel }
func (pixels) Bounds() image.Rectangle   { return image.Rect(0, 0, size, size) }
func (p pixels) At(x, y int) color.Color { return p(x, y) }

func lerp(a, b color.RGBA, t float64) color.RGBA {
	t = min(max(t, 0), 1)
	mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.RGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 255}
}

// ramp 是纵坐标从 y0 到 y1 之间 a 到 b 的竖向渐变。
func ramp(y0, y1 float64, a, b color.RGBA) image.Image {
	return pixels(func(_, y int) color.Color { return lerp(a, b, (float64(y)-y0)/(y1-y0)) })
}

// plate 画底板：落在背景上的软影与发丝边、自左上到右下的渐变，再叠上顶部的柔光
// 与沿内缘的一圈高光。底板是 824 见方，四周各留 100 像素，是 macOS 图标的规格。
func (c *canvas) plate(a, b color.RGBA) {
	body := []poly{squircle(size/2, size/2, 412)}
	c.shade(color.NRGBA{10, 18, 32, 77}, 0, 14, 8, nil, body...)
	c.fill(image.NewUniform(color.NRGBA{0, 0, 0, 26}), []poly{squircle(size/2, size/2, 413.2)})
	c.fill(pixels(func(x, y int) color.Color {
		return lerp(a, b, float64(x+y-200)/1648)
	}), body)
	c.fill(pixels(func(_, y int) color.Color { // 顶部的柔光，到六成高度消失
		return color.NRGBA{255, 255, 255, uint8(76 * max(0, 1-float64(y-100)/494))}
	}), body)
	c.fill(pixels(func(_, y int) color.Color { // 内缘的高光，到半高消失
		return color.NRGBA{255, 255, 255, uint8(166 * max(0, 1-float64(y-100)/412))}
	}), body, squircle(size/2, size/2, 409))
}
