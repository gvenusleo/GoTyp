package gui

import (
	"path"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *App) tabsView(c *ui.Context, narrowing bool) {
	strip := ui.ScrollHorizontal(c.Key("file-tabs-scroll")).Height(tabHeight).Grow(1).MinWidth(0).
		TrackScroll(&a.tabScroll).Scrollbars(false).Label("打开的文件").HandleInput(a.scrollTabs)
	if fade, ok := tabFadeMask(c, &a.tabScroll); ok {
		strip.MaskLinearGradient(fade)
	}
	direction := float32(1)
	if c.Direction() == ui.RightToLeft {
		direction = -1
	}
	scrollX := a.tabScroll.X
	dragged, x, dropped := a.tabDrag(c, strip)
	rows, closeTabs := a.tabRows(c, strip, narrowing, dragged != nil)
	if from := slices.Index(a.tabs, dragged); from >= 0 {
		bounds := make([]ui.Rect, len(rows))
		for i, row := range rows {
			bounds[i] = row.LayoutBounds()
		}
		x += direction * (a.tabScroll.X - scrollX)
		gap := tabInsertionGap(bounds, x, direction)
		if dropped {
			a.moveTab(dragged, gap)
		} else {
			shiftTabRows(rows, bounds, from, gap, direction)
		}
	}
	if closeTabs != nil {
		closeTabs()
	}
}

func (a *App) tabRows(c *ui.Context, strip ui.Element, narrowing, collectRows bool) ([]ui.Element, func()) {
	directories := make(map[string][]string, len(a.tabs))
	for _, tab := range a.tabs {
		name := path.Base(tab.id)
		directories[name] = append(directories[name], path.Dir(tab.id))
	}
	var rows []ui.Element
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
				if collectRows {
					rows = append(rows, row)
				}
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
	return rows, closeTabs
}

func (a *App) tabView(c *ui.Context, tabs ui.TabsParts, i int, tab *fileTab, name, dir string) ui.Element {
	t := c.Theme()
	active := tab == a.activeTab
	row := ui.Row(c.Key(tab.id)).Height(tabHeight).AlignItems(ui.Center).Shrink(0).Radius(t.Radius)
	hovered := row.Hovered()
	return row.Children(func() {
		part := tabs.Tab(i).Height(tabHeight).AlignItems(ui.Center).Radius(t.Radius).
			Disabled(a.busy).Label(tab.id).Tooltip(tab.id)
		if !a.busy && len(a.tabs) > 1 {
			part.Drag(tab)
		}
		dragging := part.Dragging()
		switch {
		case dragging:
			// MyGo 在指针下画出完整副本，原位只留下占位。
			part.Background(t.SurfacePressed).Opacity(0)
			row.Background(rowFill(t))
		case active && len(a.tabs) > 1:
			part.Background(fileSelectionFill(t))
		}
		status, glyph, statusColor := tabStatus(t, tab.session)
		if status != "" {
			part.Description(status)
		}
		if a.activeTab == nil {
			part.OnClick(func() { a.openNote(tab.id) })
		}
		color := t.TextMuted
		if active || hovered || dragging {
			color = t.Text
		}
		part.Children(func() {
			tabTitle(c, name, dir, color)
			tabActionSlot(ui.Box(c)).MarginEnd(tabInset).Children(func() {
				if dragging && status != "" {
					tabStatusIcon(c, glyph, statusColor)
				}
			})
		})
		button := a.tabCloseButton(c, tab, glyph, statusColor, status != "" && !hovered, active || hovered || row.FocusWithin())
		if dragging {
			button.Opacity(0)
		}
	})
}

func shiftTabRows(rows []ui.Element, bounds []ui.Rect, from, gap int, direction float32) {
	source := bounds[from]
	shift := direction * (source.W + tabGap)
	switch {
	case gap < from:
		target := bounds[gap]
		for _, row := range rows[gap:from] {
			row.Translate(shift, 0)
		}
		rows[from].Translate(tabLeadingEdge(target, direction)-tabLeadingEdge(source, direction), 0)
	case gap > from+1:
		target := bounds[gap-1]
		for _, row := range rows[from+1 : gap] {
			row.Translate(-shift, 0)
		}
		rows[from].Translate(tabLeadingEdge(target, -direction)-tabLeadingEdge(source, -direction), 0)
	}
}

func tabLeadingEdge(bounds ui.Rect, direction float32) float32 {
	if direction < 0 {
		return bounds.X + bounds.W
	}
	return bounds.X
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
	ui.Row(c).Height(tabHeight).Padding(0, tabGap, 0, tabLabelInset).
		Gap(c.Theme().Space(1)).AlignItems(ui.Center).Children(func() {
		width := float32(tabTextWidth)
		if dir != "" {
			width -= tabDirectoryWidth
		}
		ui.Text(c, name).TextColor(color).SingleLine().Ellipsis("…").MaxWidth(width)
		if dir != "" {
			ui.Text(c, dir).TextColor(c.Theme().TextMuted).FontSize(detailFontSize).
				SingleLine().Ellipsis("…").MaxWidth(tabDirectoryWidth)
		}
	})
}

func (a *App) tabDrag(c *ui.Context, strip ui.Element) (*fileTab, float32, bool) {
	if a.busy || len(a.tabs) < 2 {
		return nil, 0, false
	}
	// 保存松手坐标，以免下一帧构建前的指针移动改变落点。
	releaseX := ui.Local(strip, "tab-release-x", func() float32 { return 0 })
	strip.ObserveInput(func(ev ui.InputEvent) {
		if ev.Kind == ui.InputPointerUp && ev.Button == 0 && !ev.Canceled {
			*releaseX = ev.X
		}
	})
	area := strip.Bounds()
	nextScroll := ui.Local(strip, "tab-drag-scroll", func() time.Time { return time.Time{} })
	if tab, ok := ui.Drop[*fileTab](strip); ok {
		*nextScroll = time.Time{}
		return tab, area.X + *releaseX, true
	}
	if tab, over := ui.DragOver[*fileTab](strip); over && slices.Contains(a.tabs, tab) {
		x, _, inside := strip.PointerPosition()
		if inside {
			distance := x
			if c.Direction() == ui.RightToLeft {
				distance = area.W - x
			}
			a.scrollTabsOnDrag(c, distance, area.W, nextScroll)
			return tab, area.X + x, false
		}
	}
	*nextScroll = time.Time{}
	return nil, 0, false
}

func (a *App) scrollTabsOnDrag(c *ui.Context, x, width float32, next *time.Time) {
	edge := min(float32(tabFadeWidth), width/2)
	if edge <= 0 {
		return
	}
	s := &a.tabScroll
	innerX := min(max(x, edge), width-edge)
	offset := min(max(s.X+(x-innerX)*tabDragStep/edge, 0), s.MaxX)
	if offset == s.X {
		*next = time.Time{}
		return
	}
	now := c.Now()
	if !now.Before(*next) {
		s.X = offset
		*next = now.Add(time.Second / 60)
	}
	c.After(next.Sub(now))
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

func (a *App) tabCloseButton(c *ui.Context, tab *fileTab, glyph *ui.SVG, statusColor ui.Color, showStatus, showClose bool) ui.Element {
	t := c.Theme()
	label := "关闭 " + tab.id
	button := tabActionSlot(ui.ButtonBase(c.Key("close"))).Absolute().End(tabInset).Top((tabHeight - tabCloseSize) / 2).
		Label(label).Tooltip(label).Radius(t.Radius).Disabled(a.busy).OnClick(func() { a.closeTab(tab) })
	color := t.TextMuted
	switch {
	case button.Pressed():
		button.Background(pressFill(t))
		color = t.Text
	case button.Hovered():
		button.Background(buttonFill(t))
		color = t.Text
	}
	return button.Children(func() {
		focused := button.Focused()
		if showStatus && !focused {
			tabStatusIcon(c, glyph, statusColor)
		} else if showClose || focused {
			ui.Icon(c, iconClose).FontSize(tabCloseIconSize).TextColor(color)
		}
	})
}

func tabActionSlot(e ui.Element) ui.Element {
	return e.Size(tabCloseSize, tabCloseSize).Shrink(0).Justify(ui.Center).AlignItems(ui.Center)
}

func tabStatusIcon(c *ui.Context, glyph *ui.SVG, color ui.Color) {
	if glyph != nil {
		ui.Icon(c, glyph).FontSize(tabCloseIconSize).TextColor(color)
	} else {
		ui.Box(c).Size(unsavedDotSize, unsavedDotSize).Radius(unsavedDotSize / 2.0).Background(color)
	}
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
