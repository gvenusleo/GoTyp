package main

import "math"

// pt 是画布上的一点，坐标单位为像素，原点在左上角。
type pt struct{ x, y float64 }

// poly 是一个闭合的多边形。
type poly []pt

// area 返回多边形带符号的面积，屏幕坐标系里顺时针为正。
func area(p poly) float64 {
	var s float64
	for i, v := range p {
		w := p[(i+1)%len(p)]
		s += v.x*w.y - v.y*w.x
	}
	return s / 2
}

// shift 返回平移后的多边形。
func shift(p poly, dx, dy float64) poly {
	q := make(poly, len(p))
	for i, v := range p {
		q[i] = pt{v.x + dx, v.y + dy}
	}
	return q
}

// unit 返回 (x, y) 方向上的单位向量。
func unit(x, y float64) pt {
	l := math.Hypot(x, y)
	return pt{x / l, y / l}
}

// squircle 是以 (cx, cy) 为中心、半边长 r 的连续圆角方块（超椭圆，指数 5），
// 与 macOS 图标的轮廓相近。
func squircle(cx, cy, r float64) poly {
	const steps = 1440
	p := make(poly, steps)
	for i := range p {
		s, c := math.Sincos(2 * math.Pi * float64(i) / steps)
		p[i] = pt{
			cx + r*math.Copysign(math.Pow(math.Abs(c), 0.4), c),
			cy + r*math.Copysign(math.Pow(math.Abs(s), 0.4), s),
		}
	}
	return p
}

// fillet 把各角都是直角的多边形每个角倒成半径 r 的圆角，凸角和凹角一样处理。
func fillet(corners poly, r float64) poly {
	const steps = 32
	var p poly
	for i, v := range corners {
		prev, next := corners[(i+len(corners)-1)%len(corners)], corners[(i+1)%len(corners)]
		a, b := unit(prev.x-v.x, prev.y-v.y), unit(next.x-v.x, next.y-v.y)
		// 直角的圆心在两条边各退 r 处；圆弧从一条边上的切点 v+r·a 走到另一条边上的 v+r·b，
		// 相对圆心分别是 -b 与 -a 方向。
		c := pt{v.x + r*(a.x+b.x), v.y + r*(a.y+b.y)}
		for k := range steps + 1 {
			t := float64(k) / steps
			d := unit(-(1-t)*b.x-t*a.x, -(1-t)*b.y-t*a.y)
			p = append(p, pt{c.x + r*d.x, c.y + r*d.y})
		}
	}
	return p
}

// sheet 是左下角在 (x0, y1)、边长 n、圆角 r 的方块。notch 大于 0 时从它的左下角
// 去掉边长 notch 的一块，成 L 形，好让内一层连同缝放进去。
func sheet(x0, y1, n, notch, r float64) poly {
	x1, y0 := x0+n, y1-n
	if notch == 0 {
		return fillet(poly{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}, r)
	}
	return fillet(poly{
		{x0, y0}, {x1, y0}, {x1, y1},
		{x0 + notch, y1}, {x0 + notch, y1 - notch}, {x0, y1 - notch},
	}, r)
}
