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
	var completed []pdf.State
	p := pdfPreview{request: 3, loaded: func(state pdf.State) { completed = append(completed, state) }}
	p.stateChanged(t.Context(), pdf.State{RequestGeneration: 2, DisplayedGeneration: 2})
	p.stateChanged(t.Context(), pdf.State{RequestGeneration: 3, Loading: true})
	if len(completed) != 0 {
		t.Fatal("过期或未完成的任务进入完成回调")
	}
	state := pdf.State{RequestGeneration: 3, DisplayedGeneration: 3, PageCount: 2}
	p.stateChanged(t.Context(), state)
	p.stateChanged(t.Context(), state)
	if len(completed) != 1 || completed[0] != state || p.loaded != nil {
		t.Fatalf("完成回调状态不符：%+v", completed)
	}
}

func TestPDFRefreshRetiresPendingOutlineRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	p := pdfPreview{outline: pdfOutlineState{
		displayed: 1, load: 1, request: 2, cancel: cancel,
		result: pdf.OutlineResult{DisplayedGeneration: 1},
	}}
	// 原图仍在显示时，新加载会取消组件中的大纲读取。
	p.stateChanged(t.Context(), pdf.State{RequestGeneration: 2, DisplayedGeneration: 1, Loading: true})
	if ctx.Err() == nil || p.outline.request != 0 {
		t.Fatal("刷新后仍在等待已取消的大纲请求")
	}
	// 刷新失败时旧 PDF 留在屏幕上，仍可使用同一代大纲。
	p.stateChanged(t.Context(), pdf.State{RequestGeneration: 2, DisplayedGeneration: 1, Err: errors.New("刷新失败")})
	if p.outline.displayed != 1 || p.outline.result.DisplayedGeneration != 1 {
		t.Fatal("刷新失败后丢失了正在显示的大纲")
	}
}

func TestPDFEvictionReleasesPendingTypstArtifact(t *testing.T) {
	a := newApp()
	t.Cleanup(a.cancel)
	makeArtifact := func() *typstPDF {
		t.Helper()
		name := filepath.Join(t.TempDir(), "preview.pdf")
		if err := os.WriteFile(name, []byte("PDF"), 0o600); err != nil {
			t.Fatal(err)
		}
		return &typstPDF{path: name}
	}
	displayed, pending := makeArtifact(), makeArtifact()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	p := &pdfPreview{
		file: displayed, pendingFile: pending, request: 3,
		loaded:  func(pdf.State) { t.Fatal("仍有过期加载回调") },
		outline: pdfOutlineState{request: 2, cancel: cancel},
	}
	a.releasePDFViewer(p)
	a.previewFiles.Wait()
	if ctx.Err() == nil || p.pendingFile != nil || p.request != 0 || p.loaded != nil {
		t.Fatal("回收查看器后未作废仍在等待的任务")
	}
	if _, err := os.Stat(pending.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("回收查看器后待显示的 PDF 仍然存在：%v", err)
	}
	if _, err := os.Stat(displayed.path); err != nil {
		t.Fatalf("回收查看器后无法复用已显示的 PDF：%v", err)
	}
	a.closeTabPDF(&fileTab{pdf: p})
	a.previewFiles.Wait()
	if _, err := os.Stat(displayed.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("关闭标签后仍保留已显示的 PDF：%v", err)
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
