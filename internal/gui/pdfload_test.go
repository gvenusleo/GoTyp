package gui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gvenusleo/mygo-pdf/pdf"

	"gotyp/internal/tinymist"
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

func TestPDFRefreshRetiresPendingOutlineRead(t *testing.T) {
	a := newApp()
	t.Cleanup(a.cancel)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	a.pdfOutline = pdfOutlineState{
		displayed: 1, load: 1, request: 2, cancel: cancel,
		result: pdf.OutlineResult{DisplayedGeneration: 1},
	}
	// 原图仍在显示时，新加载会取消组件中的大纲读取。
	a.pdfStateChanged(pdf.State{RequestGeneration: 2, DisplayedGeneration: 1, Loading: true})
	if ctx.Err() == nil || a.pdfOutline.request != 0 {
		t.Fatal("刷新后仍在等待已取消的大纲请求")
	}
	// 刷新失败时旧 PDF 留在屏幕上，仍可使用同一代大纲。
	a.pdfStateChanged(pdf.State{RequestGeneration: 2, DisplayedGeneration: 1, Err: errors.New("刷新失败")})
	if a.pdfOutline.displayed != 1 || a.pdfOutline.result.DisplayedGeneration != 1 {
		t.Fatal("刷新失败后丢失了正在显示的大纲")
	}
}

// 窗口关闭后，排队中的回调可能无法执行，仍需释放预览文件。
func TestPreviewFileReleasedWhenWindowCloses(t *testing.T) {
	a := newApp()
	t.Cleanup(a.cancel)
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, "preview.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	var queued func()
	a.publishFn = func(fn func()) { queued = fn }
	a.publishPreview(a.ctx, &tinymist.PreviewFile{File: file}, func() {
		t.Fatal("窗口关闭后执行了预览回调")
	})
	a.cancel()
	a.previewFiles.Wait()
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("窗口关闭后预览目录仍然存在：%v", err)
	}
	queued()
}
