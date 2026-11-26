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

// trace 按顺时针方向把多边形加进光栅器。
func (c *canvas) trace(p poly) {
	if area(p) < 0 {
		p = slices.Clone(p)
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

// fill 用纯色填充形状。
func (c *canvas) fill(col color.Color, shape poly) {
	c.z.Reset(size, size)
	c.trace(shape)
	c.z.Draw(c.img, c.img.Bounds(), image.NewUniform(col), image.Point{})
}

// mask 把形状画成覆盖率遮罩。
func (c *canvas) mask(shapes ...poly) *image.Alpha {
	m := image.NewAlpha(c.img.Bounds())
	c.z.Reset(size, size)
	for _, p := range shapes {
		c.trace(p)
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

// plate 画带边框和软影的纯白底板。底板是 824 见方，四周各留 100 像素。
func (c *canvas) plate() {
	const radius = 412
	body := squircle(size/2, size/2, radius)
	c.shade(color.NRGBA{10, 18, 32, 77}, 0, 14, 8, nil, body)
	c.fill(color.NRGBA{0, 0, 0, 26}, squircle(size/2, size/2, radius+1.2))
	c.fill(color.White, body)
}
