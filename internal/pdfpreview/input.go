package pdfpreview

import (
	"math"
	"slices"

	"github.com/egoist/mygo/ui"
)

type pointerDrag struct {
	down  bool
	moved bool
	x, y  float32
	start textPoint
	link  int
}

func (v *Viewer) zoomCentered(zoom float32) { v.setZoom(zoom, v.width/2, v.height/2) }

func (v *Viewer) viewportInput(services ui.Services) func(ui.InputEvent) bool {
	return func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputZoom:
			return v.pinch(ev, ev.X, ev.Y)
		case ui.InputScroll:
			if ev.Mods&(ui.Cmd|ui.Ctrl) != 0 && ev.DY != 0 {
				v.setZoom(v.zoom*float32(math.Exp(-float64(ev.DY)/160)), ev.X, ev.Y)
				return true
			}
		}
		return v.keyInput(ev, services)
	}
}

func (v *Viewer) pinch(ev ui.InputEvent, x, y float32) bool {
	if ev.Scale <= 0 {
		return false
	}
	v.setZoom(v.zoom*ev.Scale, x, y)
	return true
}

func (v *Viewer) keyInput(ev ui.InputEvent, services ui.Services) bool {
	if ev.Kind == ui.InputCommand && ev.Text == "copy" || ev.Kind == ui.InputKeyDown && ev.Mods&(ui.Cmd|ui.Ctrl) != 0 && ev.Mods&^(ui.Cmd|ui.Ctrl) == 0 && ev.Key == ui.KeyC {
		v.copySelection(services)
		return true
	}
	if ev.Kind != ui.InputKeyDown || ev.Mods&^(ui.Cmd|ui.Ctrl|ui.Shift) != 0 {
		return false
	}
	switch ev.Key {
	case ui.KeyMinus:
		v.zoomCentered(v.zoom / 1.25)
	case ui.KeyEqual:
		v.zoomCentered(v.zoom * 1.25)
	case ui.Key0:
		v.zoomCentered(1)
	default:
		return false
	}
	return true
}

func (v *Viewer) copySelection(services ui.Services) {
	if v.hasSelection {
		v.worker.submitCopy(copyJob{ctx: v.docContext, document: v.document, selected: v.selection, apply: services.WriteClipboard})
	}
}

func (v *Viewer) pageInput(page int, frame *pageFrame, bounds ui.Rect, services ui.Services) func(ui.InputEvent) bool {
	return func(ev ui.InputEvent) bool {
		if frame.key.document != v.document {
			return false
		}
		switch ev.Kind {
		case ui.InputZoom:
			return v.pinch(ev, bounds.X+ev.X-v.scroll.X, bounds.Y+ev.Y-v.scroll.Y)
		case ui.InputPointerDown:
			if ev.Button != 0 {
				return false
			}
			v.hasSelection = false
			index := characterAt(frame, ev.X/bounds.W, ev.Y/bounds.H, false)
			v.pointer = pointerDrag{down: true, x: ev.X, y: ev.Y, start: textPoint{page, index}, link: linkAt(frame, ev.X/bounds.W, ev.Y/bounds.H)}
			return true
		case ui.InputPointerMove:
			if v.pointer.down && ev.Button == 0 {
				v.extendSelection(ev, bounds)
				return true
			}
		case ui.InputPointerUp:
			if !v.pointer.down || ev.Button != 0 {
				return false
			}
			if !ev.Canceled {
				v.extendSelection(ev, bounds)
				if link := v.pointer.link; !v.pointer.moved && link >= 0 && link == linkAt(frame, ev.X/bounds.W, ev.Y/bounds.H) {
					v.openLink(frame.links[link], services)
				}
			}
			v.pointer = pointerDrag{}
			return true
		}
		return false
	}
}

func (v *Viewer) extendSelection(ev ui.InputEvent, bounds ui.Rect) {
	drag := &v.pointer
	if !drag.moved && math.Hypot(float64(ev.X-drag.x), float64(ev.Y-drag.y)) < 3 {
		return
	}
	drag.moved = true
	if drag.start.index < 0 {
		return
	}
	x, y := bounds.X+ev.X, bounds.Y+ev.Y
	page := v.layout.firstPage(y)
	frame := v.cache[page].frame
	if frame == nil || frame.key.document != v.document {
		return
	}
	r := v.layout.pages[page]
	if index := characterAt(frame, (x-r.X)/r.W, (y-r.Y)/r.H, true); index >= 0 {
		first, last := drag.start, textPoint{page, index}
		if first.page > last.page || first.page == last.page && first.index > last.index {
			first, last = last, first
		}
		v.selection, v.hasSelection = textRange{first, last}, true
	}
}

func (v *Viewer) openLink(link pageLink, services ui.Services) {
	if link.url != "" {
		services.OpenURL(link.url)
	} else if link.page >= 0 && link.page < len(v.pages) {
		page := v.layout.pages[link.page]
		v.scroll.X = max(0, min(page.X+link.target.X*page.W, v.layout.width-v.width))
		v.scroll.Y = max(0, min(page.Y+link.target.Y*page.H, v.layout.height-v.height))
	}
}

func linkAt(frame *pageFrame, x, y float32) int {
	return slices.IndexFunc(frame.links, func(link pageLink) bool { return link.bounds.Contains(x, y) })
}

func characterAt(frame *pageFrame, x, y float32, nearest bool) int {
	index, distance := -1, float32(math.MaxFloat32)
	for i, box := range frame.chars {
		if box.W == 0 || box.H == 0 {
			continue
		}
		if box.Contains(x, y) {
			return i
		}
		if nearest {
			dx := max(box.X-x, x-box.X-box.W, 0)
			dy := max(box.Y-y, y-box.Y-box.H, 0)
			if d := dx*dx + dy*dy; d < distance {
				index, distance = i, d
			}
		}
	}
	return index
}
