package gui

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/vault"
)

// viewer 是不进编辑器的当前文件：图片，或无法以文本打开的文件。它没有编辑会话，
// 也没有要保存的内容；img 为空时显示 note 和「在默认应用中打开」。
type viewer struct {
	id            string
	img           ui.ImageSource
	width, height int   // 原图的像素尺寸，SVG 为 0
	size          int64 // 文件字节数，未知时为 0
	note          string
}

// imageExts 是在应用内显示的图片扩展名。SVG 由 MyGo 解析，其余在 decodeImage 中解码。
var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true}

func isImage(id string) bool { return imageExts[strings.ToLower(path.Ext(id))] }

// readFile 在后台读取要打开的文件：图片解码为查看器，其余按文本读取；文本过大或不是 UTF-8 时
// 也转为查看器，不放进编辑器。只有路径或读取本身出错时返回 err。
func readFile(v *vault.Vault, id string) (source string, st vault.FileStat, vw *viewer, err error) {
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
