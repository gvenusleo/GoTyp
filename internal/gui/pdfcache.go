package gui

import "github.com/gvenusleo/mygo-pdf/pdf"

// 查看器各有渲染缓存，独立 PDF 的原始字节也会一直固定在内存中。
const (
	pdfViewerLimit = 2
	pdfByteBudget  = 96 << 20
)

func (a *App) trimPDFCache(active *fileTab) {
	for {
		var oldest *fileTab
		var bytes int64
		count := 0
		for _, tab := range a.tabs {
			p := tab.pdf
			if p == nil || (p.Viewer == nil && tab != active) {
				continue
			}
			count++
			pending := p.pendingBytes
			if tab.viewer != nil {
				pending = max(pending, int64(cap(tab.viewer.pdf)))
			}
			bytes += p.sourceBytes + pending
			if tab != active && (oldest == nil || p.used < oldest.pdf.used) {
				oldest = tab
			}
		}
		if oldest == nil || (count <= pdfViewerLimit && bytes <= pdfByteBudget) {
			return
		}
		a.evictPDF(oldest)
	}
}

func (a *App) evictPDF(tab *fileTab) {
	p := tab.pdf
	if state := p.State(); p.position.page == 0 && state.Page > 0 {
		p.position = pdfPosition{state.Page, state.Zoom, state.Fit, state.Rotation}
	}
	a.releasePDFViewer(p)
	p.outline.displayed, p.outline.load = 0, 0
	p.outline.result = pdf.OutlineResult{}
	for i := range p.outline.rows {
		// 保留行身份以便重读时匹配，去掉书签对旧查看器的引用。
		entry := &p.outline.rows[i].entry
		*entry = pdf.OutlineEntry{Title: entry.Title, Page: entry.Page}
	}
	if vw := tab.viewer; vw != nil {
		vw.cancelPDF()
		vw.pdfReady = false
		tab.changed = true
	}
	if s := tab.session; s != nil {
		s.previewReady, s.previewStarting = false, false
	}
}
