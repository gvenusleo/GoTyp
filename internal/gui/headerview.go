package gui

import (
	"path"
	"strings"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/vault"
)

var (
	modeIcons  = []*ui.SVG{iconPencil, iconColumns, iconEye}
	modeLabels = []string{"编辑 ⌘1", "分屏 ⌘2", "预览 ⌘3"}
)

// headerView 是内容区顶部的标题栏：目录与标题、未保存标记、模式切换和更多操作。
func (a *App) headerView(c *ui.Context, s *session) {
	t := c.Theme()
	// 侧栏收起时给固定的侧栏开关让出位置，按展开程度插值，整个动画期间标题匀速让位。
	gutter := t.Space(4)
	left := gutter + (1-a.sidebarShown)*max(toggleInset(c)+barButton+t.Space(2)-gutter, 0)
	ui.Row(c).Height(headerHeight).Shrink(0).Padding(0, t.Space(3), 0, left).Gap(t.Space(2)).
		AlignItems(ui.Center).DragWindow().Children(func() {
		id := a.openID()
		if id != "" {
			a.titleView(c, id, s)
		}
		ui.Spacer(c)
		// 只有笔记有预览，纯文本和查看器不显示模式切换。
		if s != nil && s.kind != 0 {
			a.modeSwitch(c)
		}
		if id != "" {
			ghostButton(c, "更多", iconEllipsis).Menu(a.moreMenu)
		}
	})
	ui.Divider(c)
}

// titleView 显示当前文件的目录和文件名；s 是它的编辑会话，查看器没有。
func (a *App) titleView(c *ui.Context, id string, s *session) {
	t := c.Theme()
	ui.Row(c).Gap(t.Space(1.5)).AlignItems(ui.Center).MinWidth(0).Shrink(1).Children(func() {
		if dir := parentDir(id); dir != "" {
			ui.Text(c, strings.ReplaceAll(dir, "/", " / ")+" /").TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		}
		ui.Text(c, path.Base(id)).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(id)
		switch {
		case s == nil:
		case s.conflict:
			ui.Icon(c, iconWarning).FontSize(iconSmallSize).TextColor(t.Warning)
		case s.err != "":
			ui.Icon(c, iconAlert).FontSize(iconSmallSize).TextColor(t.Danger)
		case s.dirty() || s.saving:
			ui.Box(c).Size(unsavedDotSize, unsavedDotSize).Radius(unsavedDotSize / 2).Background(t.TextMuted).Label("未保存").Tooltip("未保存")
		}
	})
}

// modeSwitch 是编辑、分屏、预览三个图标分段。
func (a *App) modeSwitch(c *ui.Context) {
	t := c.Theme()
	seg := ui.SegmentedBase(c, &a.mode, len(modeIcons))
	seg.Track.Padding(modeTrackInset).Radius(t.Radius + 1).Background(t.Surface).Label("显示模式").Children(func() {
		for i, svg := range modeIcons {
			part := seg.Segment(i).Padding(t.Space(1), t.Space(2)).Radius(t.Radius - 1).Label(modeLabels[i]).Tooltip(modeLabels[i])
			color := t.TextMuted
			part.Transition(hoverMotion)
			if i == a.mode {
				part.Background(raisedFill(t)).Shadow(0, modeShadowY, modeShadowBlur, 0, modeShadowColor)
				color = t.Text
			} else if part.Hovered() {
				color = t.Text
			}
			part.Children(func() { ui.Icon(c, svg).FontSize(iconSize).TextColor(color) })
		}
	}).OnChange(a.modeChanged)
}

// moreMenu 收纳低频动作。
func (a *App) moreMenu(m *ui.Menu) {
	id := a.openID()
	if id == "" {
		return
	}
	s := a.current
	if s == nil && m.Item("在默认应用中打开").Chosen() {
		a.openVaultFile(id)
	}
	if m.Item("复制路径").Chosen() {
		a.copyCurrentPath()
	}
	if m.Item("在文件管理器中显示").Chosen() {
		a.revealCurrent()
	}
	if m.Item("重命名…").Chosen() {
		a.startRename(id)
	}
	if s == nil {
		return
	}
	if s.kind == vault.KindTypst {
		if m.Item("导出 PDF…").Shortcut(ui.Cmd, ui.KeyE).Disabled(s.exporting).Chosen() {
			a.exportPDFDialog()
		}
	}
	m.Separator()
	if m.Item("显示行号").Checked(a.lineNumbers).Chosen() {
		a.toggleLineNumbers()
	}
	if m.Item("专注模式").Checked(a.focus).Chosen() {
		a.toggleFocus()
	}
}
