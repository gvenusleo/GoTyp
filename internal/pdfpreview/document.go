package pdfpreview

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"math"
	"runtime"
	"unsafe"
)

// MaxPDFBytes 是单份 PDF 的读取上限。
const MaxPDFBytes = 256 << 20

const (
	maxPages  = 100_000
	maxPixels = 4_000_000
	maxSide   = 4096
)

type pageSize struct{ width, height float32 }

type document struct {
	id     uint64
	handle uintptr
	pin    runtime.Pinner
	pages  []pageSize
	copy   bool
}

func (e *engine) open(ctx context.Context, id uint64, data []byte) (*document, error) {
	if len(data) == 0 || len(data) > MaxPDFBytes {
		return nil, fmt.Errorf("PDF 为空或超过 256 MiB")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	doc := &document{id: id}
	// PDFium 在关闭文档之前仍会读取这块 Go 内存。
	doc.pin.Pin(&data[0])
	doc.handle = e.loadDocument(&data[0], uintptr(len(data)), nil)
	if doc.handle == 0 {
		doc.pin.Unpin()
		return nil, fmt.Errorf("无法读取 PDF（PDFium 错误 %d）", e.lastError())
	}
	if err := e.readPages(ctx, doc); err != nil {
		e.close(doc)
		return nil, err
	}
	doc.copy = e.userPermissions(doc.handle)&16 != 0
	return doc, nil
}

func (e *engine) readPages(ctx context.Context, doc *document) error {
	count := e.pageCount(doc.handle)
	if count <= 0 || count > maxPages {
		return fmt.Errorf("PDF 页数超出范围（%d 页）", count)
	}
	doc.pages = make([]pageSize, count)
	for i := range doc.pages {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := &doc.pages[i]
		if e.pageSize(doc.handle, int32(i), size) == 0 || !validSize(size.width) || !validSize(size.height) {
			return fmt.Errorf("无法读取 PDF 第 %d 页的尺寸", i+1)
		}
	}
	return nil
}

func validSize(size float32) bool {
	return size >= 0.01 && size <= 1_000_000
}

func (e *engine) close(doc *document) {
	if doc != nil {
		e.closeDocument(doc.handle)
		doc.pin.Unpin()
	}
}

func rasterSize(width, height float64, pixelLimit int) image.Point {
	factor := min(1, maxSide/max(width, height), math.Sqrt(float64(pixelLimit)/(width*height)))
	w := max(1, int(width*factor))
	h := max(1, int(height*factor))
	if w*h > pixelLimit {
		if w > h {
			w = pixelLimit / h
		} else {
			h = pixelLimit / w
		}
	}
	return image.Pt(w, h)
}

func (e *engine) drawPage(page uintptr, key rasterKey) (*image.RGBA, error) {
	region := key.region()
	img := image.NewRGBA(image.Rectangle{Max: region.Size()})
	width, height := int32(region.Dx()), int32(region.Dy())
	const bgra = 4
	var pixels runtime.Pinner
	pixels.Pin(&img.Pix[0])
	defer pixels.Unpin()
	bitmap := e.createBitmap(width, height, bgra, unsafe.Pointer(&img.Pix[0]), int32(img.Stride))
	if bitmap == 0 {
		return nil, fmt.Errorf("无法分配 PDF 页面位图")
	}
	defer e.destroyBitmap(bitmap)
	if e.fillBitmap(bitmap, 0, 0, width, height, 0xffffffff) == 0 {
		return nil, fmt.Errorf("无法填充 PDF 页面位图")
	}
	const annotations = 1
	const reverseByteOrder = 0x10
	e.renderBitmap(bitmap, page, -int32(region.Min.X), -int32(region.Min.Y), int32(key.size.X), int32(key.size.Y), 0, annotations|reverseByteOrder)
	return img, nil
}

// render 调用者持有 PDFium 执行权，直至释放页句柄。
func (e *engine) render(ctx context.Context, doc *document, key rasterKey) (*pageFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	page := e.loadPage(doc.handle, int32(key.page))
	if page == 0 {
		return nil, fmt.Errorf("无法读取 PDF 第 %d 页", key.page+1)
	}
	defer e.closePage(page)
	img, err := e.drawPage(page, key)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !key.tile.Empty() {
		return &pageFrame{key: key, pixels: img}, nil
	}
	return e.frame(ctx, doc, page, key, img)
}

func trimCString(data []byte) string {
	text, _, _ := bytes.Cut(data, []byte{0})
	return string(text)
}
