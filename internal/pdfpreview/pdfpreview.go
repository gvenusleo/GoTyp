// Package pdfpreview 通过 MyGo 原生模块嵌入 macOS PDFKit 预览。
package pdfpreview

import (
	"context"
	"io"

	"github.com/egoist/mygo/native"
)

// Viewer 显示 PDF，文档刷新时保留阅读位置和缩放。
//
//mygo:native
type Viewer interface {
	native.View
	io.Closer
	// Load 在后台解码 PDF，取消后不替换当前文档。
	Load(ctx context.Context, data []byte) error
	// Clear 取消正在加载的文档，并释放当前 PDF。
	Clear()
	// SetBackground 让阅读画布跟随应用主题。
	SetBackground(red, green, blue float64)
}
