package gui

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif" // image.Decode 识别 GIF
	_ "image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // image.Decode 识别 WebP
)

// 位图的限制：解码前按尺寸拦下像素过多的图，免得一张压缩得很小的巨幅图占满内存；
// 解码后长边缩到 imageEdge 以内，高分屏上铺满窗口也够清楚。
const (
	imageMaxPixels = 40_000_000
	imageEdge      = 4096
)

// decodeImage 解码 PNG、JPEG、GIF 或 WebP，缩到 imageEdge 以内并按 EXIF 方向转正，
// 同时返回原图的像素尺寸。解码不便宜，只在后台调用。
func decodeImage(data []byte) (img image.Image, width, height int, err error) {
	size, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("无法读取图片：%w", err)
	}
	if size.Width*size.Height > imageMaxPixels {
		return nil, 0, 0, fmt.Errorf("图片 %d×%d 像素太多", size.Width, size.Height)
	}
	img, _, err = image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("无法读取图片：%w", err)
	}
	orientation := exifOrientation(data)
	width, height = size.Width, size.Height
	if orientation >= 5 {
		width, height = height, width
	}
	// 缩小之后再转，要处理的像素少得多。
	return orient(fitImage(img, imageEdge), orientation), width, height, nil
}

// fitImage 把长边缩到 edge 以内，不放大。
func fitImage(img image.Image, edge int) image.Image {
	b := img.Bounds()
	long := max(b.Dx(), b.Dy())
	if long <= edge {
		return img
	}
	w, h := max(1, b.Dx()*edge/long), max(1, b.Dy()*edge/long)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

// exifOrientation 返回 JPEG 里 EXIF 记的拍摄方向（1 到 8，1 为正）；不是 JPEG 或没有记录时为 1。
// 手机拍的照片常常靠这个标记表示竖拍，不转正就会横着显示。
func exifOrientation(data []byte) int {
	if !bytes.HasPrefix(data, []byte{0xFF, 0xD8}) {
		return 1
	}
	// 逐段往后找 APP1 里的 Exif，扫描数据（SOS）开始后不会再有。
	for i := 2; i+4 <= len(data) && data[i] == 0xFF && data[i+1] != 0xDA; {
		size := int(binary.BigEndian.Uint16(data[i+2:]))
		end := min(i+2+size, len(data))
		if data[i+1] == 0xE1 {
			if tiff, ok := bytes.CutPrefix(data[min(i+4, end):end], []byte("Exif\x00\x00")); ok {
				return tiffOrientation(tiff)
			}
		}
		i += 2 + size
	}
	return 1
}

// tiffOrientation 在 TIFF 结构的第一个目录里找方向标签（0x0112）。
func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	dir := int(order.Uint32(tiff[4:]))
	if dir+2 > len(tiff) {
		return 1
	}
	for i := range int(order.Uint16(tiff[dir:])) {
		entry := dir + 2 + 12*i
		if entry+12 > len(tiff) {
			break
		}
		if order.Uint16(tiff[entry:]) == 0x0112 {
			if v := int(order.Uint16(tiff[entry+8:])); v >= 1 && v <= 8 {
				return v
			}
			break
		}
	}
	return 1
}

// orient 按 EXIF 方向把图转正：2 到 4 是镜像与旋转 180°，5 到 8 要转 90°，宽高对调。
func orient(img image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	size := image.Rect(0, 0, w, h)
	if orientation >= 5 {
		size = image.Rect(0, 0, h, w)
	}
	dst := image.NewRGBA(size)
	for y := range h {
		for x := range w {
			dx, dy := orientedPoint(orientation, x, y, w, h)
			dst.Set(dx, dy, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// orientedPoint 返回原图 (x, y) 处的像素在转正后的位置。
func orientedPoint(orientation, x, y, w, h int) (int, int) {
	switch orientation {
	case 2:
		return w - 1 - x, y
	case 3:
		return w - 1 - x, h - 1 - y
	case 4:
		return x, h - 1 - y
	case 5:
		return y, x
	case 6:
		return h - 1 - y, x
	case 7:
		return h - 1 - y, w - 1 - x
	case 8:
		return y, w - 1 - x
	}
	return x, y
}
