package gui

import (
	"testing"

	"github.com/gvenusleo/mygo-pdf"
)

func TestPDFStateChangedCompletesOnlyCurrentRequest(t *testing.T) {
	a := newApp()
	t.Cleanup(a.cancel)
	var completed []pdf.State
	a.previewRequest = 3
	a.previewLoaded = func(state pdf.State) { completed = append(completed, state) }
	a.pdfStateChanged(pdf.State{RequestGeneration: 2, DisplayedGeneration: 2})
	a.pdfStateChanged(pdf.State{RequestGeneration: 3, Loading: true})
	if len(completed) != 0 {
		t.Fatal("过期或未完成的任务进入完成回调")
	}
	state := pdf.State{RequestGeneration: 3, DisplayedGeneration: 3, PageCount: 2}
	a.pdfStateChanged(state)
	a.pdfStateChanged(state)
	if len(completed) != 1 || completed[0] != state || a.previewLoaded != nil {
		t.Fatalf("完成回调状态不符：%+v", completed)
	}
}
