package pdfpreview

import (
	"context"
	"image"
	"net/url"
	"strings"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"
)

const (
	maxPageChars = 50_000
	maxPageLinks = 1024
	maxURL       = 8192
)

type pdfRect struct{ left, top, right, bottom float32 }

type pageLink struct {
	bounds ui.Rect
	page   int
	target ui.Point
	url    string
}

func (link pageLink) bytes() int { return 48 + len(link.url) }

type pageFrame struct {
	key    rasterKey
	bitmap *ui.Bitmap
	pixels *image.RGBA
	chars  []ui.Rect
	links  []pageLink
}

func (e *engine) frame(ctx context.Context, doc *document, page uintptr, key rasterKey, img *image.RGBA) (*pageFrame, error) {
	f := &pageFrame{key: key, pixels: img}
	f.links = e.annotations(ctx, doc, page)
	if text := e.loadText(page); text != 0 {
		defer e.closeText(text)
		if doc.copy {
			f.chars = e.characterBoxes(ctx, page, text, key.metadata)
		}
		f.links = e.webLinks(ctx, page, text, f.links)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	remaining := key.metadata - 16*len(f.chars)
	for i, link := range f.links {
		remaining -= link.bytes()
		if remaining < 0 {
			clear(f.links[i:])
			f.links = f.links[:i]
			break
		}
	}
	return f, nil
}

func (e *engine) characterBoxes(ctx context.Context, page, text uintptr, budget int) []ui.Rect {
	boxes := make([]ui.Rect, max(0, min(int(e.charCount(text)), maxPageChars, budget/32)))
	for i := range boxes {
		if i%128 == 0 && ctx.Err() != nil {
			return nil
		}
		var left, right, bottom, top float64
		if e.charBox(text, int32(i), &left, &right, &bottom, &top) != 0 {
			boxes[i] = e.normalizedRect(page, left, top, right, bottom)
		}
	}
	return boxes
}

func (e *engine) normalizedRect(page uintptr, left, top, right, bottom float64) ui.Rect {
	// 归一化坐标独立于光栅尺寸，裁切和固有旋转交给 PDFium。
	const extent = 1 << 16
	var x1, y1, x2, y2 int32
	if e.pageToDevice(page, 0, 0, extent, extent, 0, left, top, &x1, &y1) == 0 ||
		e.pageToDevice(page, 0, 0, extent, extent, 0, right, bottom, &x2, &y2) == 0 {
		return ui.Rect{}
	}
	x, y := max(0, min(x1, x2)), max(0, min(y1, y2))
	rightEdge, bottomEdge := min(extent, max(x1, x2)), min(extent, max(y1, y2))
	return ui.Rect{X: float32(x) / extent, Y: float32(y) / extent,
		W: float32(max(0, rightEdge-x)) / extent, H: float32(max(0, bottomEdge-y)) / extent}
}

func (e *engine) annotations(ctx context.Context, doc *document, page uintptr) []pageLink {
	var links []pageLink
	var cursor int32
	for range maxPageLinks {
		var annot uintptr
		if ctx.Err() != nil || e.nextLink(page, &cursor, &annot) == 0 {
			break
		}
		var rect pdfRect
		if e.linkRect(annot, &rect) == 0 {
			continue
		}
		link := e.annotationTarget(doc, annot)
		if link.page < 0 && link.url == "" {
			continue
		}
		link.bounds = e.normalizedRect(page, float64(rect.left), float64(rect.top), float64(rect.right), float64(rect.bottom))
		links = append(links, link)
	}
	return links
}

func (e *engine) annotationTarget(doc *document, annot uintptr) pageLink {
	const gotoPage = 1
	const uri = 3
	link := pageLink{page: -1}
	dest := e.linkDest(doc.handle, annot)
	if dest == 0 {
		if action := e.linkAction(annot); action != 0 {
			switch e.actionType(action) {
			case gotoPage:
				dest = e.actionDest(doc.handle, action)
			case uri:
				if n := e.actionURI(doc.handle, action, nil, 0); n > 1 && n <= maxURL {
					buf := make([]byte, n)
					e.actionURI(doc.handle, action, &buf[0], n)
					link.url = allowedURL(trimCString(buf))
				}
			}
		}
	}
	if dest != 0 {
		if page := int(e.destPage(doc.handle, dest)); page >= 0 && page < len(doc.pages) {
			link.page = page
			link.target = e.destinationPoint(doc.handle, int32(page), dest)
		}
	}
	return link
}

func (e *engine) destinationPoint(doc uintptr, index int32, dest uintptr) ui.Point {
	const (
		fitHorizontal = 3
		fitVertical   = 4
		fitRectangle  = 5
		fitBoundsH    = 7
		fitBoundsV    = 8
	)
	var count cLong
	var params [4]float32
	view := e.destView(dest, &count, &params[0])
	var x, y, zoom float32
	var hasX, hasY, hasZoom int32
	if e.destLocation(dest, &hasX, &hasY, &hasZoom, &x, &y, &zoom) == 0 {
		switch {
		case count >= 1 && (view == fitHorizontal || view == fitBoundsH):
			y, hasY = params[0], 1
		case count >= 1 && (view == fitVertical || view == fitBoundsV):
			x, hasX = params[0], 1
		case count >= 4 && view == fitRectangle:
			x, y, hasX, hasY = params[0], params[3], 1, 1
		default:
			return ui.Point{}
		}
	}
	if hasX == 0 && hasY == 0 {
		return ui.Point{}
	}
	page := e.loadPage(doc, index)
	if page == 0 {
		return ui.Point{}
	}
	defer e.closePage(page)
	var bounds pdfRect
	if e.pageBounds(page, &bounds) == 0 {
		return ui.Point{}
	}
	if hasX == 0 {
		x = bounds.left
	}
	if hasY == 0 {
		y = bounds.top
	}
	const extent = 1 << 16
	var dx, dy int32
	if e.pageToDevice(page, 0, 0, extent, extent, 0, float64(x), float64(y), &dx, &dy) == 0 {
		return ui.Point{}
	}
	return ui.Point{X: float32(max(0, min(extent, dx))) / extent, Y: float32(max(0, min(extent, dy))) / extent}
}

func (e *engine) webLinks(ctx context.Context, page, text uintptr, links []pageLink) []pageLink {
	web := e.loadWebLinks(text)
	if web == 0 {
		return links
	}
	defer e.closeWebLinks(web)
	for i := range min(e.webLinkCount(web), maxPageLinks) {
		if ctx.Err() != nil || len(links) >= maxPageLinks {
			break
		}
		n := e.webLinkURL(web, i, nil, 0)
		if n <= 1 || n > maxURL {
			continue
		}
		buf := make([]uint16, n)
		e.webLinkURL(web, i, &buf[0], n)
		address := allowedURL(utf16Text(buf))
		if address == "" {
			continue
		}
		for j := range min(e.webRectCount(web, i), int32(maxPageLinks-len(links))) {
			var left, top, right, bottom float64
			if e.webRect(web, i, j, &left, &top, &right, &bottom) != 0 {
				bounds := e.normalizedRect(page, left, top, right, bottom)
				links = append(links, pageLink{bounds: bounds, page: -1, url: address})
			}
		}
	}
	return links
}

func allowedURL(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.User != nil || !ui.SafeToOpen(address) {
		return ""
	}
	if !strings.EqualFold(u.Scheme, "mailto") && u.Hostname() == "" {
		return ""
	}
	return address
}

func utf16Text(units []uint16) string {
	runes := utf16.Decode(units)
	return strings.TrimRight(string(runes), "\x00")
}

type textPoint struct{ page, index int }
type textRange struct{ first, last textPoint }

func (e *engine) copyText(ctx context.Context, doc *document, selection textRange) string {
	if !doc.copy {
		return ""
	}
	var result strings.Builder
	for i := selection.first.page; i <= selection.last.page && i < len(doc.pages) && result.Len() < 1<<20; i++ {
		if ctx.Err() != nil {
			return ""
		}
		page := e.loadPage(doc.handle, int32(i))
		if page == 0 {
			continue
		}
		text := e.pageSelection(page, i, selection)
		e.closePage(page)
		if text != "" {
			if result.Len() != 0 {
				result.WriteByte('\n')
			}
			result.WriteString(text)
		}
	}
	return result.String()
}

func (e *engine) pageSelection(page uintptr, index int, selection textRange) string {
	text := e.loadText(page)
	if text == 0 {
		return ""
	}
	defer e.closeText(text)
	start, end := 0, min(int(e.charCount(text)), maxPageChars)
	if index == selection.first.page {
		start = selection.first.index
	}
	if index == selection.last.page {
		end = min(end, selection.last.index+1)
	}
	if end <= start {
		return ""
	}
	buf := make([]uint16, end-start+1)
	n := e.getText(text, int32(start), int32(end-start), &buf[0])
	return utf16Text(buf[:max(0, min(int(n), len(buf)))])
}
