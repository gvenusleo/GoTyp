package gui

import (
	"fmt"
	"path"

	"github.com/egoist/mygo/ui"
)

// viewerView 显示不进编辑器的文件：图片铺在浅灰画布上，其余是类型图标、原因和
// 「在默认应用中打开」。
func (a *App) viewerView(c *ui.Context, vw *viewer) {
	t := c.Theme()
	if vw.img != nil {
		ui.Scroll(c.Key("viewer-" + vw.id)).Grow(1).MinHeight(0).Background(t.Surface).Label(path.Base(vw.id)).Children(func() {
			ui.Column(c).FillWidth().MinHeight(0).AlignItems(ui.Center).Padding(previewInset).Children(func() {
				w, h := ui.ImageSize(vw.img)
				ui.Image(c, vw.img).MaxWidth(w).MaxWidthPercent(100).AspectRatio(w/h).
					Border(1, t.Border).Shadow(0, 1, 4, 0, pageShadowColor).Label(path.Base(vw.id))
			})
		})
		return
	}
	ui.Column(c).Grow(1).Center().Gap(t.Space(2)).Padding(emptyInset).Children(func() {
		ui.Icon(c, fileIcon(vw.id)).FontSize(emptyIconSize).TextColor(t.TextMuted.Alpha(0.6)).Margin(0, 0, t.Space(1))
		ui.Text(c, path.Base(vw.id)).SingleLine().Ellipsis("…").MaxWidth(textWidth)
		if vw.note != "" {
			ui.Text(c, vw.note).FontSize(detailFontSize).TextColor(t.TextMuted).TextAlign(ui.Center).Selectable()
		}
		ui.Button(c, "在默认应用中打开").Margin(t.Space(2), 0, 0).OnClick(func() { a.openVaultFile(vw.id) })
	})
}

// viewerStatus 是查看图片时状态栏右侧的尺寸和文件大小。
func (a *App) viewerStatus(c *ui.Context, vw *viewer) {
	t := c.Theme()
	if vw.width > 0 {
		statusItem(c, "像素尺寸", nil, fmt.Sprintf("%d × %d", vw.width, vw.height), t.TextMuted, nil, false)
	}
	if vw.size > 0 {
		statusItem(c, "文件大小", nil, byteSize(vw.size), t.TextMuted, nil, false)
	}
}

// byteSize 把字节数显示为「812 KB」或「2.3 MB」，按 1000 进位，与访达一致。
func byteSize(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d 字节", n)
	case n < 1000*1000:
		return fmt.Sprintf("%d KB", (n+500)/1000)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
}
