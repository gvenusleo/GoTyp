package pdfpreview

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// View 构建滚动预览。父布局可能变动时，传入新的可比较 layoutKey。
func (v *Viewer) View(c *ui.Context, layoutKey any, openExternal func()) {
	t := c.Theme()
	scroll := ui.ScrollBoth(c).Grow(1).FillWidth().MinWidth(0).MinHeight(0).
		TrackScroll(&v.scroll).Label("PDF 页面")
	bounds := v.scroll.Handle.Bounds(c)
	w, h := c.Size()
	layoutChanged := v.windowWidth != w || v.windowHeight != h || v.layoutKey != layoutKey
	v.windowWidth, v.windowHeight, v.layoutKey = w, h, layoutKey
	unmeasured := bounds.W <= 0 || bounds.H <= 0
	firstMeasurement := unmeasured && !v.waitingBounds
	v.waitingBounds = unmeasured
	if v.measure(bounds.W, bounds.H, t.Space(3)) || layoutChanged || firstMeasurement {
		// MyGo 先构建后布局，首次出现和尺寸变化时在下一帧读取新尺寸。
		c.AnimationFrame()
	}
	if len(v.pages) == 0 {
		return
	}
	services := c.Services()
	doc := v.document
	scroll.Focusable().HandleInput(v.viewportInput(services)).ContextMenu(func(m *ui.Menu) {
		if v.document == doc {
			v.contextMenu(m, services, openExternal)
		}
	})
	first, last := v.visiblePages()
	v.requestVisible(first, last)
	scroll.Children(func() {
		ui.Box(c).Size(v.layout.width, v.layout.height).Shrink(0).Children(func() {
			for i := first; i <= last; i++ {
				v.pageView(c, i)
			}
		})
	})
}

func (v *Viewer) contextMenu(m *ui.Menu, services ui.Services, openExternal func()) {
	if m.Item("复制").Shortcut(ui.Cmd, ui.KeyC).Disabled(!v.hasSelection).Chosen() {
		v.copySelection(services)
	}
	m.Separator()
	if m.Item("放大").Disabled(v.zoom >= 4).Chosen() {
		v.zoomCentered(v.zoom * 1.25)
	}
	if m.Item("缩小").Disabled(v.zoom <= 0.5).Chosen() {
		v.zoomCentered(v.zoom / 1.25)
	}
	if m.Item("适应宽度").Disabled(v.zoom == 1).Chosen() {
		v.zoomCentered(1)
	}
	if openExternal != nil {
		m.Separator()
		if m.Item("在默认应用中打开").Chosen() {
			openExternal()
		}
	}
}

func (v *Viewer) pageView(c *ui.Context, index int) {
	t := c.Theme()
	r := v.layout.pages[index]
	entry := v.cache[index]
	label := fmt.Sprintf("第 %d 页", index+1)
	// 文档代次变更时取消旧页面的鼠标捕获。
	key := struct {
		document uint64
		page     int
	}{v.document, index}
	page := ui.Box(c.Key(key)).Absolute().Left(r.X).Top(r.Y).Size(r.W, r.H).Clip().Label(label)
	frame := entry.frame
	if frame == nil && entry.err != nil {
		page.Background(t.Background)
		page.Center().Children(func() { ui.Text(c, entry.err.Error()).TextColor(t.TextMuted) })
	}
	if frame != nil && frame.key.document == v.document {
		services := c.Services()
		page.HandleInput(v.pageInput(index, frame, r, services))
		if x, y, over := page.PointerPosition(); over {
			switch {
			case linkAt(frame, x/r.W, y/r.H) >= 0:
				page.Cursor(ui.CursorPointer)
			case characterAt(frame, x/r.W, y/r.H, false) >= 0:
				page.Cursor(ui.CursorText)
			}
		}
	}
	page.Children(func() {
		if frame != nil {
			ui.Image(c, frame.bitmap).Absolute().Size(r.W, r.H).AccessHidden(true)
		}
		v.tileViews(c, index, r)
		if frame != nil {
			v.highlight(c, index, frame, r)
		}
	})
}

func (v *Viewer) tileViews(c *ui.Context, page int, bounds ui.Rect) {
	for _, key := range v.tileKeys {
		if key.page != page || v.tiles[key].frame == nil {
			continue
		}
		x, y := bounds.W/float32(key.size.X), bounds.H/float32(key.size.Y)
		left, top := float32(key.tile.Min.X)*x, float32(key.tile.Min.Y)*y
		width, height := float32(key.tile.Max.X)*x-left, float32(key.tile.Max.Y)*y-top
		ui.Image(c.Key(key.tile), v.tiles[key].frame.bitmap).Absolute().Left(left).Top(top).
			Size(width, height).PassThrough().AccessHidden(true)
	}
}

func (v *Viewer) highlight(c *ui.Context, page int, frame *pageFrame, bounds ui.Rect) {
	if !v.hasSelection || frame.key.document != v.document || page < v.selection.first.page || page > v.selection.last.page {
		return
	}
	start, end := 0, len(frame.chars)
	if page == v.selection.first.page {
		start = min(v.selection.first.index, end)
	}
	if page == v.selection.last.page {
		end = min(end, v.selection.last.index+1)
	}
	color := c.Theme().Selection
	// 合并同一行相邻字形，缩短长选区对应的视图树。
	var line ui.Rect
	for _, rect := range frame.chars[min(start, end):end] {
		if rect.W == 0 || rect.H == 0 {
			continue
		}
		if line.W > 0 && rect.Y < line.Y+line.H*0.75 && line.Y < rect.Y+rect.H*0.75 &&
			rect.X <= line.X+line.W+0.02 && line.X <= rect.X+rect.W+0.02 {
			x, y := min(line.X, rect.X), min(line.Y, rect.Y)
			line = ui.Rect{X: x, Y: y, W: max(line.X+line.W, rect.X+rect.W) - x, H: max(line.Y+line.H, rect.Y+rect.H) - y}
			continue
		}
		highlightRect(c, line, bounds, color)
		line = rect
	}
	highlightRect(c, line, bounds, color)
}

func highlightRect(c *ui.Context, r, bounds ui.Rect, color ui.Color) {
	if r.W > 0 {
		ui.Box(c).Absolute().Left(r.X*bounds.W).Top(r.Y*bounds.H).Size(r.W*bounds.W, r.H*bounds.H).
			Background(color).PassThrough().AccessHidden(true)
	}
}
