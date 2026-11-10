package gui

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// hoverView 是 tinymist 悬停文档的内容。Typst 函数签名可能有几十行，限制高度并在悬停框内滚动。
func hoverView(c *ui.Context, text string, md *markdown.Options) {
	ui.Scroll(c).MaxHeight(hoverHeight).Children(func() { markdown.View(c, text, md) })
}
