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
	ui.Scroll(c).Grow(1).Padding(previewInset, previewSideInset).Label("Markdown 预览").Children(func() {
		ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
			ui.Column(c).MaxWidth(textWidth).FillWidth().Children(func() {
				s.md.Theme = a.themes.preview
				markdown.View(c, s.text(), &s.md).FillWidth()
			})
		})
	})
}

// typstPreviewView 显示原生 PDF 视图；尚未成功编译时显示加载或错误状态。
func (a *App) typstPreviewView(c *ui.Context, s *session) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).Background(t.Surface).Label("Typst 预览").Children(func() {
		a.diagnosticsView(c, s)
		if s.previewReady && a.preview != nil {
			ui.NativeView(c, a.preview).Grow(1)
			return
		}
		a.typstEmptyView(c, s)
	})
}

// diagnosticsView 是由状态栏展开的诊断面板，列出 tinymist 报告的全部错误和警告。
func (a *App) diagnosticsView(c *ui.Context, s *session) {
	text, errors := a.diagReport.text, a.diagReport.errors
	if text == "" || !s.previewErrOpen {
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
		ghostButton(c, "收起", iconChevronUp).OnClick(func() { s.previewErrOpen = false })
	})
}

func (a *App) typstEmptyView(c *ui.Context, s *session) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Padding(emptyInset).Children(func() {
		switch {
		case a.preview == nil:
			ui.Text(c, a.previewError).TextColor(t.TextMuted)
		case s.previewErr != "":
			ui.Text(c, s.previewErr).TextColor(t.TextMuted).Selectable()
		case s.previewStarting:
			ui.Spinner(c).Label("正在启动预览")
		}
	})
}
