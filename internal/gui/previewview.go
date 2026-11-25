package gui

import (
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
	ui.Scroll(c.Key(s.id)).TrackScroll(&s.mdScroll).Grow(1).Padding(previewInset, previewSideInset).Label("Markdown 预览").Children(func() {
		ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
			ui.Column(c).MaxWidth(textWidth).FillWidth().Children(func() {
				s.md.Theme = a.themes.preview
				markdown.View(c, s.text(), &s.md).FillWidth()
			})
		})
	})
}

// typstPreviewView 显示 PDF 页面，首次加载时显示进度或错误。
func (a *App) typstPreviewView(c *ui.Context, s *session) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).Background(t.Surface).Label("Typst 预览").Children(func() {
		a.diagnosticsView(c, s)
		ui.Column(c.Key(s.id)).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			if a.preview != nil {
				layout := struct {
					mode                      int
					width, split              float32
					focus, diagnostics, alert bool
				}{a.mode, a.contentWidth(c), a.editorPercent, a.focus, s.previewErrOpen && a.diagReport.text != "", s.conflict || s.err != ""}
				a.preview.View(c.Key("pdf"), layout)
			}
			if !s.previewReady {
				a.typstEmptyView(c, s)
			}
		})
	})
}

// diagnosticsView 是由状态栏展开的诊断面板，列出 tinymist 报告的全部错误和警告。
func (a *App) diagnosticsView(c *ui.Context, s *session) {
	text := a.diagReport.text
	if text == "" || !s.previewErrOpen {
		return
	}
	t := c.Theme()
	padY := t.Space(1)
	ui.Row(c.Key("typst-diagnostics")).Gap(t.Space(2)).Padding(padY, t.Space(2), padY, t.Space(4)).
		MinHeight(barButton+2*padY+1).MaxHeightPercent(diagnosticsHeightPercent).
		AlignItems(ui.Start).Background(t.Background).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Clip().Transition(bannerMotion).Children(func() {
		ui.Scroll(c).MaxHeight(diagnosticsHeight).AlignSelf(ui.Stretch).MinHeight(0).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, text).Font(codeFamily(a.state.Appearance.CodeFont)).FontSize(detailFontSize).Selectable()
		})
		ghostButton(c, "复制", iconCopy).OnClick(func() {
			a.services.WriteClipboard(text)
			a.notify("已复制")
		})
		ghostButton(c, "收起", iconChevronUp).OnClick(func() { s.previewErrOpen = false })
	})
}

func (a *App) typstEmptyView(c *ui.Context, s *session) {
	t := c.Theme()
	ui.Column(c.Key("pdf-status")).Absolute().Fill().Center().Padding(emptyInset).Children(func() {
		switch {
		case s.previewErr != "":
			ui.Text(c, s.previewErr).TextColor(t.TextMuted).Selectable()
		case s.previewStarting:
			ui.Spinner(c).Label("正在启动预览")
		}
	})
}
