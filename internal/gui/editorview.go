package gui

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func (a *App) editorView(c *ui.Context, s *session) {
	s.ed.ReadOnly = a.busy || a.openingVault || a.closing || a.quitting
	s.ed.Theme = a.themes.code
	editor.View(c.Key(s.id), s.ed).Grow(1).FillWidth().AutoFocus()
	// 编辑器自带 ⌘; 切换行号，这里把它的结果同步回应用设置，菜单勾选和工作区状态保持一致。
	if on := s.ed.LineNumbers != editor.LineNumbersOff; on != a.lineNumbers {
		a.lineNumbers = on
		a.persistWorkspace()
		a.refreshMenu()
	}
	a.observe(c, s)
}

// alertView 在编辑区上方常驻展示冲突与保存失败，提供就地操作，不自动消失。
func (a *App) alertView(c *ui.Context, s *session) {
	if !s.conflict && s.err == "" {
		return
	}
	t := c.Theme()
	// 外部修改和文件被删除需要用户选择，用警告色；写入失败才用错误色。
	color, svg := t.Danger, iconAlert
	if s.conflict {
		color, svg = t.Warning, iconWarning
	}
	ui.Row(c.Key("alert")).Gap(t.Space(2)).Padding(t.Space(1.5), t.Space(3), t.Space(1.5), t.Space(4)).AlignItems(ui.Center).
		Background(color.Alpha(bannerOpacity)).Transition(bannerMotion).Children(func() {
		ui.Icon(c, svg).FontSize(iconSize).TextColor(color)
		ui.Text(c, s.err).FontSize(textSize).Grow(1).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(s.err)
		switch {
		case s.deleted:
			ui.Button(c, "重新创建").OnClick(a.overwriteCurrent)
			ui.Button(c, "关闭").OnClick(a.discardCurrent)
		case s.conflict:
			ui.Button(c, "重新加载").OnClick(a.reloadCurrent)
			ui.Button(c, "覆盖").OnClick(a.overwriteCurrent)
		default:
			ui.Button(c, "重试").OnClick(a.saveCurrent)
		}
		ui.Button(c, "另存为副本").OnClick(a.saveCopyDialog)
	})
}
