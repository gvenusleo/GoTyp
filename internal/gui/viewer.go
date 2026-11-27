package gui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/gvenusleo/mygo-pdf/pdf"

	"gotyp/internal/vault"
)

// viewer 保存不进编辑器的文件状态。PDF 字节在交给后台查看器后释放。
type viewer struct {
	id            string
	img           ui.ImageSource
	width, height int   // 原图的像素尺寸，SVG 为 0
	size          int64 // 文件字节数，未知时为 0
	note          string
	pdf           []byte
	pdfTarget     string
	pdfReady      bool
	pdfCancel     context.CancelFunc
}

// imageExts 是在应用内显示的图片扩展名。SVG 由 MyGo 解析，其余在 decodeImage 中解码。
var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true}

func isImage(id string) bool { return imageExts[strings.ToLower(path.Ext(id))] }

func isPDF(id string) bool { return strings.EqualFold(path.Ext(id), ".pdf") }

// readFile 在后台读取要打开的文件：PDF 和图片作为只读文件，其余尝试按文本读取。
func readFile(v *vault.Vault, id string) (source string, st vault.FileStat, vw *viewer, err error) {
	if isPDF(id) {
		vw, err := readPDF(v, id)
		return "", vault.FileStat{}, vw, err
	}
	if isImage(id) {
		vw, err := readImage(v, id)
		return "", vault.FileStat{}, vw, err
	}
	source, st, err = v.Read(id)
	switch {
	case errors.Is(err, vault.ErrNotText):
		return "", st, &viewer{id: id, note: "无法以文本打开"}, nil
	case errors.Is(err, vault.ErrTooLarge):
		return "", st, &viewer{id: id, note: fmt.Sprintf("文件超过 %d MiB，无法以文本打开", vault.MaxTextBytes>>20)}, nil
	}
	return source, st, nil, err
}

func readPDF(v *vault.Vault, id string) (*viewer, error) {
	data, _, target, err := v.ReadBytesWithTarget(id, pdf.MaxPDFBytes)
	vw := &viewer{id: id, pdf: data, pdfTarget: target, size: int64(len(data))}
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, vault.ErrOutside):
		return vw, err
	case errors.Is(err, vault.ErrTooLarge):
		vw.note = fmt.Sprintf("PDF 超过 %d MiB，无法预览", pdf.MaxPDFBytes>>20)
	case err != nil:
		vw.note = "无法读取 PDF：" + err.Error()
	}
	return vw, nil
}

func (a *App) loadPDF(tab *fileTab) {
	vw := tab.viewer
	if vw.pdf == nil || vw.note != "" {
		return
	}
	data := vw.pdf
	vw.pdf = nil
	ctx, cancel := context.WithCancel(a.ctx)
	vw.pdfCancel = cancel
	tab.pdf.loadBytes(ctx, data, func(state pdf.State) {
		if ctx.Err() != nil || tab.viewer != vw {
			return
		}
		vw.cancelPDF()
		if state.Err != nil {
			vw.note = "无法显示 PDF：" + state.Err.Error()
			return
		}
		vw.pdfReady = true
	})
}

func (vw *viewer) cancelPDF() {
	if vw != nil && vw.pdfCancel != nil {
		vw.pdfCancel()
		vw.pdfCancel = nil
	}
}

// readImage 读取并解码一张图片。文件不存在或路径无效时返回 err；其余读不了的情况
// 查看器带着原因，仍可交给默认应用打开。
func readImage(v *vault.Vault, id string) (*viewer, error) {
	vw := &viewer{id: id}
	data, _, err := v.ReadBytes(id, vault.MaxImageBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, vault.ErrOutside):
		return nil, err
	case errors.Is(err, vault.ErrTooLarge):
		vw.note = fmt.Sprintf("图片超过 %d MiB，无法预览", vault.MaxImageBytes>>20)
		return vw, nil
	case err != nil:
		vw.note = err.Error()
		return vw, nil
	}
	vw.size = int64(len(data))
	if strings.EqualFold(path.Ext(id), ".svg") {
		svg, err := ui.ParseSVG(data)
		if err != nil {
			vw.note = "无法解析 SVG：" + err.Error()
			return vw, nil
		}
		vw.img = svg
		return vw, nil
	}
	img, w, h, err := decodeImage(data)
	if err != nil {
		vw.note = err.Error()
		return vw, nil
	}
	vw.img, vw.width, vw.height = ui.NewBitmap(img), w, h
	return vw, nil
}
