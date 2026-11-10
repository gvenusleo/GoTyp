package gui

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/vault"
)

// view 只组合原生组件，具体动作由对应的状态文件处理。
func (a *App) view(c *ui.Context) {
	a.applyTheme(c)
	a.services = c.Services()
	a.handleShortcuts(c)
	if a.vault == nil {
		a.welcomeView(c)
	} else {
		a.workspaceView(c)
	}
	a.paletteView(c)
	a.renameView(c)
	if a.toast != "" {
		c.Toast(a.toast)
		a.toast = ""
	}
}

// handleShortcuts 让测试和没有原生菜单的平台也能使用菜单栏中的快捷键。
func (a *App) handleShortcuts(c *ui.Context) {
	switch {
	case c.Shortcut(ui.Cmd, ui.Key1):
		a.setMode(modeEdit)
	case c.Shortcut(ui.Cmd, ui.Key2):
		a.setMode(modeSplit)
	case c.Shortcut(ui.Cmd, ui.Key3):
		a.setMode(modePreview)
	case c.Shortcut(ui.Cmd, ui.KeyP):
		a.openPalette()
	case c.Shortcut(ui.Cmd|ui.Alt, ui.KeyF):
		a.toggleFocus()
	case a.focus && c.Shortcut(0, ui.KeyEscape):
		a.focus = false
		a.refreshMenu()
	}
}

// dragBar 是隐藏标题栏后用于拖动窗口的顶部空白，给红绿灯留出位置。
func dragBar(c *ui.Context) {
	ui.Row(c).Height(headerHeight).FillWidth().Shrink(0).DragWindow()
}

// welcomeView 是没有打开仓库时的首页：一个主按钮和最近仓库。
func (a *App) welcomeView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		dragBar(c)
		ui.Column(c).Grow(1).Center().Gap(t.Space(6)).Padding(emptyInset).Children(func() {
			if a.openingVault {
				ui.Spinner(c).Label("正在打开")
				return
			}
			ui.Icon(c, iconFolderOpen).FontSize(emptyIconSize).TextColor(t.TextMuted)
			ui.PrimaryButton(c, "打开文件夹").Padding(t.Space(2), t.Space(5)).OnClick(a.openVaultDialog)
			a.recentList(c)
			if a.err != "" {
				ui.Text(c, a.err).TextColor(t.Danger).Selectable()
			}
		})
	})
}

func (a *App) recentList(c *ui.Context) {
	if len(a.state.Recent) == 0 {
		return
	}
	t := c.Theme()
	ui.Column(c).Width(dialogWidth).Gap(t.Space(0.5)).Children(func() {
		for _, root := range a.state.Recent {
			b := ui.ButtonBase(c.Key(root)).Label(filepath.Base(root)).Padding(t.Space(2), t.Space(3)).Radius(t.Radius).FillWidth().Transition(hoverMotion)
			if b.Hovered() {
				b.Background(rowFill(t))
			}
			b.Children(func() {
				ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).FillWidth().Children(func() {
					ui.Icon(c, iconFolder).FontSize(iconFolderSize).TextColor(t.TextMuted)
					ui.Text(c, filepath.Base(root)).SingleLine()
					ui.Text(c, shortPath(root)).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
				})
			}).OnClick(func() { a.openVault(root) })
		}
	})
}

func (a *App) contentView(c *ui.Context) {
	ui.Column(c).Grow(1).MinWidth(0).Fill().Children(func() {
		s := a.current
		if a.focus {
			dragBar(c)
		} else {
			a.headerView(c, s)
		}
		switch {
		case s == nil && a.viewing != nil:
			a.viewerView(c, a.viewing)
		case s == nil:
			a.emptyNoteView(c)
		default:
			a.alertView(c, s)
			// 非笔记的纯文本没有预览，不论模式都只显示编辑器。
			switch {
			case a.mode == modeEdit || s.kind == 0:
				a.editorView(c, s)
			case a.mode == modePreview:
				a.observe(c, s)
				a.previewView(c, s)
			default:
				a.splitView(c, s)
			}
		}
		if !a.focus {
			a.statusBarView(c, s)
		}
	})
}

// contentWidth 估算内容区宽度，供分屏选择左右或上下布局。
func (a *App) contentWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	return width - a.sidebarWidth*a.sidebarShown
}

func (a *App) splitView(c *ui.Context, s *session) {
	width := a.contentWidth(c)
	if width < splitMinWidth {
		ui.SplitVerticalPercent(c, &a.editorPercent,
			func() { a.editorView(c, s) },
			func() { a.previewView(c, s) },
		).SplitReset(50).Grow(1).OnChange(a.persistWorkspace)
		return
	}
	ui.SplitPercent(c, &a.editorPercent,
		func() { a.editorView(c, s) },
		func() { a.previewView(c, s) },
	).SplitReset(50).Grow(1).OnChange(a.persistWorkspace)
}

func (a *App) emptyNoteView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(t.Space(2)).Children(func() {
		if a.busy {
			ui.Spinner(c).Label("正在打开")
			return
		}
		if a.err != "" {
			ui.Text(c, a.err).TextColor(t.Danger).Selectable()
		}
		ui.Icon(c, iconFileText).FontSize(emptyIconSize).TextColor(t.TextMuted.Alpha(0.6)).Margin(0, 0, t.Space(2))
		a.hintButton(c, "新建笔记", "⌘N", func() { a.newNote(vault.KindMarkdown) })
		a.hintButton(c, "快速打开", "⌘P", a.openPalette)
	})
}

// hintButton 是空状态中的文字按钮，右侧附带快捷键。
func (a *App) hintButton(c *ui.Context, label, keys string, onClick func()) {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(label)).Label(label).Padding(t.Space(1.5), t.Space(3)).Radius(t.Radius).Width(hintWidth).Transition(hoverMotion)
	if b.Hovered() {
		b.Background(rowFill(t))
	}
	b.Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			ui.Text(c, label).TextColor(t.TextMuted).Grow(1)
			ui.Text(c, keys).TextColor(t.TextMuted)
		})
	}).OnClick(onClick)
}
