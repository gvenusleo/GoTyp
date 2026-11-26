package gui

import (
	"context"

	"github.com/gvenusleo/mygo-pdf/pdf"
)

func (a *App) loadPreview(ctx context.Context, data []byte, finished func(pdf.State)) {
	// 切换文件时宿主已清空查看器，同一文件刷新时保留画面。
	if request := a.preview.LoadBytes(ctx, data, pdf.RefreshDocument); request != 0 {
		a.previewRequest, a.previewLoaded = request, finished
	}
}

func (a *App) pdfStateChanged(state pdf.State) {
	if state.Loading || a.previewLoaded == nil || state.RequestGeneration != a.previewRequest {
		return
	}
	finished := a.previewLoaded
	a.previewRequest, a.previewLoaded = 0, nil
	// 渲染细化错误可能来自已显示页。文档提交成功时，宿主只更新文档状态。
	if state.DisplayedGeneration == state.RequestGeneration {
		state.Err = nil
	}
	finished(state)
}

func (a *App) clearPreview() {
	a.previewRequest, a.previewLoaded = 0, nil
	a.preview.Clear()
}
