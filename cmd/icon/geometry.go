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

// slantedStep 沿一条等宽的斜折缝把圆角方块分成旋转对称的两片。
func slantedStep() (poly, poly) {
	const (
		middle      = size / 2
		nearEdge    = middle - 225
		farEdge     = middle + 225
		upperTurn   = middle - 64
		lowerTurn   = middle + 64
		halfGap     = 19
		outerRadius = 56
		endRadius   = 26
		turnRadius  = 52
		turnCenterX = upperTurn + turnRadius
		turnCenterY = upperTurn - turnRadius*(math.Sqrt2-1)
	)
	var left poly
	left = left.arc(pt{nearEdge + outerRadius, nearEdge + outerRadius}, outerRadius, 180, 270)
	left = left.arc(pt{upperTurn - halfGap - endRadius, nearEdge + endRadius}, endRadius, 270, 360)
	// 缝两侧的弧线与中心线同心，半径各差半个缝宽。
	left = left.arc(pt{turnCenterX, turnCenterY}, turnRadius+halfGap, 180, 135)
	left = left.arc(pt{size - turnCenterX, size - turnCenterY}, turnRadius-halfGap, -45, 0)
	left = left.arc(pt{lowerTurn - halfGap - endRadius, farEdge - endRadius}, endRadius, 0, 90)
	left = left.arc(pt{nearEdge + outerRadius, farEdge - outerRadius}, outerRadius, 90, 180)

	right := make(poly, len(left))
	for i, p := range left {
		right[i] = pt{size - p.x, size - p.y}
	}
	return left, right
}

// arc 将指定角度范围的圆弧接到轮廓上，角度单位为度。
func (p poly) arc(center pt, radius, from, to float64) poly {
	const steps = 32
	for i := range steps + 1 {
		angle := from + (to-from)*float64(i)/steps
		sin, cos := math.Sincos(angle * math.Pi / 180)
		p = append(p, pt{center.x + radius*cos, center.y + radius*sin})
	}
	return p
}
