package gui

import (
	"context"

	"github.com/gvenusleo/mygo-pdf/pdf"

	"gotyp/internal/vault"
)

type pdfPosition struct {
	page     int
	zoom     float32
	fit      pdf.Fit
	rotation int
}

// pdfPreview 随标签保留，后台缓存满时只释放查看器和原生文档。
type pdfPreview struct {
	*pdf.Viewer
	request           uint64
	loaded            func(pdf.State)
	outline           pdfOutlineState
	used              uint64
	sourceBytes       int64
	pendingBytes      int64
	position          pdfPosition
	file, pendingFile *typstPDF
	restoring         bool
}

func (a *App) activePDF() *pdfPreview {
	if a.activeTab != nil {
		return a.activeTab.pdf
	}
	return nil
}

func (a *App) activatePDF(tab *fileTab) {
	if a.win == nil {
		return
	}
	if tab == nil {
		a.trimPDFCache(nil)
		return
	}
	file := tab.viewer != nil && isPDF(tab.id)
	if !file && (tab.session == nil || tab.session.kind != vault.KindTypst) {
		a.closeTabPDF(tab)
		a.trimPDFCache(nil)
		return
	}
	if tab.pdf == nil {
		tab.pdf = &pdfPreview{}
	}
	p := tab.pdf
	a.pdfUse++
	p.used = a.pdfUse
	a.trimPDFCache(tab)
	if p.Viewer == nil {
		options := pdf.Options{
			Labels: pdf.ChineseLabels(),
			OnLink: func(address string) { a.services.OpenURL(address) },
		}
		if file {
			options.OpenExternal = func() {
				if a.activeTab == tab {
					a.openVaultFile(tab.id)
				}
			}
		}
		p.Viewer = pdf.NewViewer(a.win, options)
		p.OnChange(func(state pdf.State) { p.stateChanged(a.ctx, state) })
		if p.file != nil {
			a.restoreTypstPDF(tab)
		}
	}
	if file {
		a.loadPDF(tab)
	}
}

func (p *pdfPreview) loadBytes(ctx context.Context, data []byte, finished func(pdf.State)) {
	if request := p.LoadBytes(ctx, data, pdf.RefreshDocument); request != 0 {
		p.request, p.loaded = request, finished
		p.pendingBytes = int64(cap(data))
	}
}

func (p *pdfPreview) stateChanged(ctx context.Context, state pdf.State) {
	if p.position.page != 0 && state.DisplayedGeneration != 0 && state.DisplayedGeneration == p.request {
		position := p.position
		p.position = pdfPosition{}
		p.SetRotation(position.rotation)
		p.SetFit(position.fit)
		p.SetZoom(position.zoom)
		p.GoToPage(min(position.page, state.PageCount))
		state = p.State()
	}
	p.updateOutline(ctx, state)
	if state.Loading || p.loaded == nil || state.RequestGeneration != p.request {
		return
	}
	finished := p.loaded
	p.request, p.loaded = 0, nil
	// 渲染细化错误可能来自已显示页。文档提交成功时，宿主只更新文档状态。
	if state.DisplayedGeneration == state.RequestGeneration {
		p.sourceBytes = p.pendingBytes
		state.Err = nil
	}
	p.pendingBytes = 0
	finished(state)
}

func (a *App) releasePDFViewer(p *pdfPreview) {
	a.discardTypstPDF(p.pendingFile)
	p.pendingFile = nil
	p.sourceBytes, p.pendingBytes = 0, 0
	p.request, p.loaded, p.restoring = 0, nil, false
	p.outline.cancelRead()
	if viewer := p.Viewer; viewer != nil {
		p.Viewer = nil
		viewer.Close()
		a.previewFiles.Go(func() { <-viewer.Done() })
	}
}

func (a *App) closeTabPDF(tab *fileTab) {
	tab.viewer.cancelPDF()
	if p := tab.pdf; p != nil {
		tab.pdf = nil
		a.releasePDFViewer(p)
		a.discardTypstPDF(p.file)
	}
}

func (a *App) closePDFPreviews() {
	for _, tab := range a.tabs {
		a.closeTabPDF(tab)
	}
}
