package gui

import (
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *App) tabsView(c *ui.Context, narrowing bool) {
	directories := make(map[string][]string, len(a.tabs))
	for _, tab := range a.tabs {
		name := path.Base(tab.id)
		directories[name] = append(directories[name], path.Dir(tab.id))
	}
	strip := ui.ScrollHorizontal(c.Key("file-tabs-scroll")).Height(tabHeight).Grow(1).MinWidth(0).
		TrackScroll(&a.tabScroll).Scrollbars(false).Label("打开的文件").HandleInput(a.scrollTabs)
	if fade, ok := tabFadeMask(c, &a.tabScroll); ok {
		strip.MaskLinearGradient(fade)
	}
	var closeTabs func()
	strip.Children(func() {
		tabs := ui.TabsBase(c, &a.tabIndex, len(a.tabs))
		tabs.List.Gap(tabGap).Label("文件标签页").OnChange(func() {
			if i := a.tabIndex; i >= 0 && i < len(a.tabs) {
				a.openNote(a.tabs[i].id)
			}
		}).Children(func() {
			for i, tab := range a.tabs {
				name := path.Base(tab.id)
				dir := tabDirectory(path.Dir(tab.id), directories[name])
				row := a.tabView(c, tabs, i, tab, name, dir)
				if tab == a.activeTab && a.revealTab {
					if narrowing || !a.tabInClearArea(c, row.Bounds()) {
						row.ScrollIntoView(ui.ScrollIntoViewOptions{Inline: ui.ScrollCenter})
					}
					a.revealTab = false
				}
				row.ContextMenu(func(m *ui.Menu) {
					closeTabs = a.tabMenu(m, tab)
				})
			}
		})
	})
	if closeTabs != nil {
		closeTabs()
	}
}

func (a *App) tabView(c *ui.Context, tabs ui.TabsParts, i int, tab *fileTab, name, dir string) ui.Element {
	t := c.Theme()
	active := tab == a.activeTab
	row := ui.Row(c.Key(tab.id)).Height(tabHeight).AlignItems(ui.Center).Shrink(0).Radius(t.Radius)
	hovered := row.Hovered()
	if active && len(a.tabs) > 1 {
		row.Background(fileSelectionFill(t))
	}
	return row.Children(func() {
		part := tabs.Tab(i).Height(tabHeight).Gap(t.Space(1)).Padding(0, tabGap, 0, tabLabelInset).
			AlignItems(ui.Center).Disabled(a.busy).Label(tab.id).Tooltip(tab.id)
		status, glyph, statusColor := tabStatus(t, tab.session)
		if status != "" {
			part.Description(status)
		}
		if a.activeTab == nil {
			part.OnClick(func() { a.openNote(tab.id) })
		}
		color := t.TextMuted
		if active || hovered {
			color = t.Text
		}
		part.Children(func() { tabTitle(c, name, dir, color) })
		a.tabCloseButton(c, tab, glyph, statusColor, status != "" && !hovered, active || hovered || row.FocusWithin())
	})
}

func (a *App) tabMenu(m *ui.Menu, tab *fileTab) func() {
	unavailable := a.busy || a.findTab(tab.id) != tab
	switch {
	case m.Item("关闭").Disabled(unavailable).Chosen():
		return func() { a.closeTab(tab) }
	case m.Item("关闭其他标签页").Disabled(unavailable || len(a.tabs) < 2).Chosen():
		return func() { a.closeTabs(func(other *fileTab) bool { return other != tab }) }
	case m.Item("关闭全部标签页").Disabled(unavailable).Chosen():
		return func() { a.closeTabs(func(*fileTab) bool { return true }) }
	}
	return nil
}

func tabDirectory(dir string, directories []string) string {
	if len(directories) < 2 {
		return ""
	}
	if dir == "." {
		return "根目录"
	}
	for suffix := path.Base(dir); suffix != dir; {
		unique := true
		for _, other := range directories {
			if other != dir && (other == suffix || strings.HasSuffix(other, "/"+suffix)) {
				unique = false
				break
			}
		}
		if unique {
			return suffix
		}
		parent := strings.TrimSuffix(dir, "/"+suffix)
		suffix = path.Base(parent) + "/" + suffix
	}
	return dir
}

func tabTitle(c *ui.Context, name, dir string, color ui.Color) {
	width := float32(tabTextWidth)
	if dir != "" {
		width -= tabDirectoryWidth
	}
	ui.Text(c, name).TextColor(color).SingleLine().Ellipsis("…").MaxWidth(width)
	if dir != "" {
		ui.Text(c, dir).TextColor(c.Theme().TextMuted).FontSize(detailFontSize).
			SingleLine().Ellipsis("…").MaxWidth(tabDirectoryWidth)
	}
}

// 隐藏滚动条后，只有纵向滚轮的鼠标也能浏览标签。
func (a *App) scrollTabs(ev ui.InputEvent) bool {
	s := &a.tabScroll
	if ev.Kind != ui.InputScroll || ev.DY*ev.DY <= ev.DX*ev.DX {
		return false
	}
	x := min(max(s.X+ev.DY, 0), s.MaxX)
	if x == s.X {
		return false
	}
	s.X = x
	return true
}

// 已完整显示的标签无需滚动，避免切换时其余标签一起移动。
func (a *App) tabInClearArea(c *ui.Context, tab ui.Rect) bool {
	area := a.tabScroll.Bounds(c)
	fade := min(float32(tabFadeWidth), area.W/2)
	left := min(fade, a.tabScroll.X)
	right := min(fade, a.tabScroll.MaxX-a.tabScroll.X)
	return tab.W > 0 && area.W > 0 && tab.X >= area.X+left && tab.X+tab.W <= area.X+area.W-right
}

func tabFadeMask(c *ui.Context, s *ui.ScrollState) (ui.LinearGradient, bool) {
	width := s.Bounds(c).W
	left, right := s.X, s.MaxX-s.X
	if width <= 0 || left <= 0 && right <= 0 {
		return ui.LinearGradient{}, false
	}
	fade := min(float32(tabFadeWidth), width/2)
	opaque := c.Theme().Text
	// 随隐藏内容的长度收起遮罩，轻微溢出时也能看清边缘文字。
	return ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{
		{Color: opaque.Alpha(1 - min(left/fade, 1)), At: 0},
		{Color: opaque, At: fade / width},
		{Color: opaque, At: 1 - fade/width},
		{Color: opaque.Alpha(1 - min(right/fade, 1)), At: 1},
	}}, true
}

func (a *App) tabCloseButton(c *ui.Context, tab *fileTab, glyph *ui.SVG, statusColor ui.Color, showStatus, showClose bool) {
	t := c.Theme()
	label := "关闭 " + tab.id
	button := ui.ButtonBase(c.Key("close")).Label(label).Tooltip(label).
		Size(tabCloseSize, tabCloseSize).Margin(0, tabInset, 0, 0).Shrink(0).Radius(t.Radius).
		Justify(ui.Center).AlignItems(ui.Center).Disabled(a.busy).OnClick(func() { a.closeTab(tab) })
	color := t.TextMuted
	switch {
	case button.Pressed():
		button.Background(pressFill(t))
		color = t.Text
	case button.Hovered():
		button.Background(buttonFill(t))
		color = t.Text
	}
	button.Children(func() {
		focused := button.Focused()
		if showStatus && !focused {
			if glyph != nil {
				ui.Icon(c, glyph).FontSize(tabCloseIconSize).TextColor(statusColor)
			} else {
				ui.Box(c).Size(unsavedDotSize, unsavedDotSize).Radius(unsavedDotSize / 2.0).Background(statusColor)
			}
		} else if showClose || focused {
			ui.Icon(c, iconClose).FontSize(tabCloseIconSize).TextColor(color)
		}
	})
}

func tabStatus(t *ui.Theme, s *session) (string, *ui.SVG, ui.Color) {
	switch {
	case s == nil:
		return "", nil, ui.Transparent
	case s.conflict:
		return "文件有外部修改", iconWarning, t.Warning
	case s.err != "":
		return "保存失败", iconAlert, t.Danger
	case s.dirty() || s.saving:
		return "未保存", nil, t.TextMuted
	default:
		return "", nil, ui.Transparent
	}
}
