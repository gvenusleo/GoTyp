// Command icon 画出 GoTyp 的应用图标并写成 PNG。两块不同颜色的图形沿斜切台阶缝
// 拼合，露出纯白底板。生成正式版 icon.png 和开发版 icon-dev.png。
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

// style 保存斜缝两侧的填充色。
type style struct {
	left  color.RGBA
	right color.RGBA
}

func rgb(v uint32) color.RGBA { return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255} }

var styles = map[string]style{
	"icon.png":     {left: rgb(0x5698C3), right: rgb(0x8FB2C9)}, // 睛蓝、晴山蓝
	"icon-dev.png": {left: rgb(0x648E93), right: rgb(0x83A78D)}, // 晚波蓝、松霜绿
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
	c.plate()
	left, right := slantedStep()
	c.fill(st.left, left)
	c.fill(st.right, right)
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
