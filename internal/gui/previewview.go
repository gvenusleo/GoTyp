package gui

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"gotyp/internal/vault"
)

func (a *App) previewView(c *ui.Context, s *session) {
	if s.kind == vault.KindMarkdown {
		a.markdownPreviewView(c, s)
		return
	}
	a.typstPreviewView(c, s)
}

// markdownPreviewView 把预览限制在阅读宽度内并居中。
func (a *App) markdownPreviewView(c *ui.Context, s *session) {
	ui.Scroll(c).Grow(1).Padding(previewInset, previewSideInset).Label("Markdown 预览").Children(func() {
		ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
			ui.Column(c).MaxWidth(textWidth).FillWidth().Children(func() {
				s.md.Theme = a.themes.md
				markdown.View(c, s.text(), &s.md).FillWidth()
			})
		})
	})
}

func (a *App) typstPreviewView(c *ui.Context, s *session) {
	t := c.Theme()
	col := ui.Column(c).Grow(1).MinWidth(0).Background(t.Surface).Label("Typst 预览")
	// 100% 的页宽取决于预览区宽度，这里用上一帧的尺寸；第一帧没有尺寸时按最大页宽。
	room := col.Bounds().W - 2*previewInset
	fit := float32(pageWidth)
	if room > 0 {
		fit = min(fit, room)
	}
	zoom := a.zoomFactor()
	width := fit * zoom
	col.Children(func() {
		a.diagnosticsView(c, s)
		if len(s.previewPages) == 0 {
			a.typstEmptyView(c, s)
			return
		}
		s.previewList.Key = func(i int) any { return i }
		s.previewList.Label = func(i int) string { return fmt.Sprintf("第 %d 页", i+1) }
		list := ui.List(c, &s.previewList, len(s.previewPages), func(i int) {
			page := s.previewPages[i]
			w, h := ui.ImageSize(page)
			ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
				img := ui.Image(c, page).Width(width).AspectRatio(w / h)
				if zoom <= 1 {
					// 窗口缩窄时上一帧的宽度还没更新，先按比例收住，不出现横向滚动。
					img.MaxWidthPercent(100)
				}
				img.Border(1, t.Border).Shadow(0, 1, 4, 0, pageShadowColor).Label(fmt.Sprintf("第 %d 页", i+1))
			})
		}).Grow(1).Gap(previewInset).Padding(previewInset).HandleInput(a.zoomInput)
		if zoom > 1 && width > room {
			list.RowWidth(width)
		}
	})
}

// diagnosticsView 是由状态栏展开的诊断面板，显示完整的编译错误或警告。
func (a *App) diagnosticsView(c *ui.Context, s *session) {
	text, errors, _ := typstDiagnostics(s)
	// 还没有成功预览时直接显示错误，不留一块空白画布。
	empty := len(s.previewPages) == 0
	if text == "" || previewUnavailable(s) || (!s.previewErrOpen && !empty) {
		return
	}
	t := c.Theme()
	color := t.Warning
	if errors > 0 {
		color = t.Danger
	}
	ui.Row(c.Key("typst-diagnostics")).Gap(t.Space(2)).Padding(t.Space(2), t.Space(2), t.Space(2), t.Space(4)).
		AlignItems(ui.Start).Background(color.Alpha(bannerOpacity)).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).
		Transition(bannerMotion).Children(func() {
		ui.Scroll(c).MaxHeight(diagnosticsHeight).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, text).Font(codeFamily(a.state.Appearance.CodeFont)).FontSize(detailFontSize).TextColor(color).Selectable()
		})
		ghostButton(c, "复制", iconCopy).OnClick(func() {
			a.services.WriteClipboard(text)
			a.notify("已复制")
		})
		if !empty {
			ghostButton(c, "收起", iconChevronUp).OnClick(func() { s.previewErrOpen = false })
		}
	})
}

func (a *App) typstEmptyView(c *ui.Context, s *session) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Padding(emptyInset).Children(func() {
		switch {
		case previewUnavailable(s):
			ui.Text(c, "未找到 typst 命令").TextColor(t.TextMuted)
		case previewBusy(c, s) || s.previewPending || s.watchWaiting:
			ui.Spinner(c).Label("正在编译")
		}
	})
}
