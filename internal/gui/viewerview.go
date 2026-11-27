package gui

import (
	"fmt"
	"path"

	"github.com/egoist/mygo/ui"
)

// viewerView 显示只读文件，并为无法预览的文件提供系统打开入口。
func (a *App) viewerView(c *ui.Context, vw *viewer) {
	t := c.Theme()
	if isPDF(vw.id) {
		a.pdfFileView(c, vw)
		return
	}
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
	a.viewerFallback(c, vw)
}

func (a *App) pdfFileView(c *ui.Context, vw *viewer) {
	t := c.Theme()
	ui.Column(c.Key(vw.id)).Grow(1).MinWidth(0).MinHeight(0).Background(t.Surface).Children(func() {
		preview := a.activePDF()
		if preview != nil {
			preview.View(c.Key("pdf-file"), a.contentWidth(c))
		}
		if !vw.pdfReady {
			ui.Column(c.Key("pdf-status")).Absolute().Fill().Center().Children(func() {
				if vw.note != "" || preview == nil {
					a.viewerFallback(c, vw)
				} else {
					ui.Spinner(c).Label("正在打开 PDF")
				}
			})
		}
	})
}

func (a *App) viewerFallback(c *ui.Context, vw *viewer) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(t.Space(2)).Padding(emptyInset).Children(func() {
		ui.Icon(c, fileIcon(vw.id)).FontSize(emptyIconSize).TextColor(t.TextMuted.Alpha(0.6)).Margin(0, 0, t.Space(1))
		ui.Text(c, path.Base(vw.id)).SingleLine().Ellipsis("…").MaxWidth(textWidth)
		if vw.note != "" {
			ui.Text(c, vw.note).FontSize(detailFontSize).TextColor(t.TextMuted).TextAlign(ui.Center).Selectable()
		}
		ui.Button(c, "在默认应用中打开").Margin(t.Space(2), 0, 0).OnClick(func() { a.openVaultFile(vw.id) })
	})
}

// viewerStatus 显示只读文件的大小和加载状态。
func (a *App) viewerStatus(c *ui.Context, vw *viewer) {
	t := c.Theme()
	if vw.pdfReady && vw.note != "" {
		statusItem(c, vw.note, iconAlert, "PDF 更新失败", t.TextMuted, nil, false)
	}
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
