package gui

import "github.com/egoist/mygo/ui"

var (
	modeIcons  = []*ui.SVG{iconPencil, iconColumns, iconEye}
	modeLabels = []string{"编辑 ⌘1", "分屏 ⌘2", "预览 ⌘3"}
)

// headerView 将标签和文件操作放在同一条可拖动的文档头上。
func (a *App) headerView(c *ui.Context, s *session) {
	t := c.Theme()
	// 侧栏收起时给固定的侧栏开关让出位置，按展开程度插值，整个动画期间标题匀速让位。
	left := float32(tabInset) + (1-a.sidebarShown)*(toggleInset(c)+barButton)
	// 只为仍伸入文档头的右侧栏按钮预留空间。
	right := t.Space(3) + max(0, barButton+tabToolsGap-a.outlineWidth)
	header := ui.Row(c).Height(headerHeight).Shrink(0).Padding(0, right, 0, left).Gap(tabToolsGap).
		AlignItems(ui.Center).DragWindow()
	showMode := s != nil && s.kind != 0
	previous := ui.Local(header, "show-mode", func() bool { return showMode })
	// 工具栏出现时会压缩本帧的标签栏，上一帧的可见范围不再可靠。
	narrowing := showMode && !*previous
	*previous = showMode
	header.Children(func() {
		if len(a.tabs) > 0 {
			a.tabsView(c, narrowing || a.outlineResizing)
		} else {
			ui.Spacer(c)
		}
		// 只有笔记有预览，纯文本和查看器不显示模式切换。
		if showMode {
			a.modeSwitch(c)
		}
	})
	ui.Divider(c)
}

// modeSwitch 是编辑、分屏、预览三个图标分段。
func (a *App) modeSwitch(c *ui.Context) {
	t := c.Theme()
	mode := &a.activeTab.layout.mode
	seg := ui.SegmentedBase(c.Key("mode-"+a.activeTab.id), mode, len(modeIcons))
	seg.Track.Padding(modeTrackInset).Radius(t.Radius + 1).Background(t.Surface).Label("显示模式").Children(func() {
		for i, svg := range modeIcons {
			part := seg.Segment(i).Padding(t.Space(1), t.Space(2)).Radius(t.Radius - 1).Label(modeLabels[i]).Tooltip(modeLabels[i])
			color := t.TextMuted
			part.Transition(hoverMotion)
			if i == *mode {
				part.Background(raisedFill(t)).Shadow(0, modeShadowY, modeShadowBlur, 0, modeShadowColor)
				color = t.Text
			} else if part.Hovered() {
				color = t.Text
			}
			part.Children(func() { ui.Icon(c, svg).FontSize(iconSize).TextColor(color) })
		}
	}).OnChange(a.modeChanged)
}
