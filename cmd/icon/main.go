// Command icon 画出 GoTyp 的应用图标并写成 PNG：三块共用左下角、往右上逐层放大的方块，
// 像一沓错落的纸，层间留一道露出底板的缝，放在淡色的底板上。生成两张：正式版 icon.png，
// 与开发版 icon-dev.png，后者换一个主题色，在程序坞里与正式版区分。
//
//	go run ./cmd/icon [-o resources]
package main

import (
	"flag"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

// 三块方块共用左下角 (left, bottom)，sizes 与 radii 由外向内；gap 是层间的缝宽。
// 整组 540 见方，放在 1024 画布的正中。
const left, bottom, gap = 242.0, 782.0, 24.0

var (
	sizes = []float64{540, 368, 196}
	radii = []float64{64, 48, 32}
)

// style 是一张图标的配色。
type style struct {
	plate  [2]color.RGBA    // 底板渐变的两端，左上到右下
	layers [3][2]color.RGBA // 三层由外向内，各自从上到下的渐变
}

func rgb(v uint32) color.RGBA { return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255} }

// themed 以主题色配出一套图标：最里一层就是主题色，中间一层是它的浅色，最外一层与
// 底板是更淡的底。
func themed(theme uint32) style {
	c, white := rgb(theme), rgb(0xFFFFFF)
	tint := func(t float64) color.RGBA { return lerp(white, c, t) } // t 越大越接近主题色
	// 上浅下深的渐变，以 x 为中点。
	layer := func(x color.RGBA) [2]color.RGBA {
		top := lerp(x, white, 0.03)
		mirror := func(m, t uint8) uint8 { return uint8(max(0, 2*int(m)-int(t))) }
		return [2]color.RGBA{top, {mirror(x.R, top.R), mirror(x.G, top.G), mirror(x.B, top.B), 255}}
	}
	return style{
		plate:  [2]color.RGBA{tint(0.03), tint(0.12)},
		layers: [3][2]color.RGBA{layer(tint(0.22)), layer(tint(0.55)), layer(c)},
	}
}

var styles = map[string]style{
	"icon.png":     themed(0xC0704A), // 正式版：赭石
	"icon-dev.png": themed(0x1F8A80), // 开发版：青绿
}

func main() {
	dir := flag.String("o", "resources", "输出目录")
	flag.Parse()
	for name, st := range styles {
		if err := write(filepath.Join(*dir, name), st); err != nil {
			fmt.Fprintln(os.Stderr, "icon:", err)
			os.Exit(1)
		}
	}
}

// write 按配色画出图标，写到 path。
func write(path string, st style) error {
	c := newCanvas()
	c.plate(st.plate[0], st.plate[1])
	c.draw(st)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(f, c.img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// draw 由外向内画三层。外两层是去掉了左下一角的 L 形，缺口正好放下内一层连同缝，
// 所以各层自带圆角，不必跟着内层的轮廓挖缝。
func (c *canvas) draw(st style) {
	for i, n := range sizes {
		notch := 0.0
		if i+1 < len(sizes) {
			notch = sizes[i+1] + gap
		}
		c.fill(ramp(bottom-n, bottom, st.layers[i][0], st.layers[i][1]),
			[]poly{sheet(left, bottom, n, notch, radii[i])})
	}
}
