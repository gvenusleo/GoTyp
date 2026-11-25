package gui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestMarkdownLinkToUnindexedPDF(t *testing.T) {
	a, s := newTestApp(t, "source")
	const id = ".attachments/report.pdf"
	name := filepath.Join(a.vault.Root(), filepath.FromSlash(id))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	results := make(chan func(), 1)
	a.publishFn = func(apply func()) { results <- apply }
	a.openMarkdownLink(s, id)
	if a.toast != "" {
		t.Fatal(a.toast)
	}
	select {
	case <-results:
		// 默认应用由主线程回调打开，测试只等待路径读取完成。
	case <-time.After(5 * time.Second):
		t.Fatal("未安排在默认应用中打开")
	}
}

func TestPDFReloadFollowsSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("创建符号链接可能需要额外权限")
	}
	a, _ := newTestApp(t, "source")
	const id, target = "link.pdf", "report.pdf"
	name := filepath.Join(a.vault.Root(), target)
	if err := os.WriteFile(name, []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(a.vault.Root(), id)); err != nil {
		t.Fatal(err)
	}
	results := make(chan func(), 4)
	a.publishFn = func(apply func()) { results <- apply }
	a.loadSession(id)
	var firstApply func()
	select {
	case firstApply = <-results:
	case <-time.After(5 * time.Second):
		t.Fatal("首次读取未完成")
	}
	if err := os.WriteFile(name, []byte("新内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.filesChanged(a.vault, []mygo.FileEvent{{Op: mygo.FileWrite, Path: target}})
	firstApply()
	if a.viewing != nil || !a.busy {
		t.Fatal("目标于首次读取期间发生变化，应丢弃已排队的结果")
	}
	publishedResult(t, results)
	if a.viewing == nil || string(a.viewing.pdf) != "新内容" {
		t.Fatal("未通过链接读取目标文件的最新内容")
	}
	if err := os.WriteFile(name, []byte("更新内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.filesChanged(a.vault, []mygo.FileEvent{{Op: mygo.FileWrite, Path: target}})
	if !a.activeTab.checking {
		t.Fatal("目标文件发生变化后没有安排重读")
	}
	publishedResult(t, results)
	if a.viewing.id != id || string(a.viewing.pdf) != "更新内容" {
		t.Fatal("标签未显示链接目标的最新内容")
	}
}

func TestPDFReadFailureEndsLoading(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("创建符号链接可能需要额外权限")
	}
	a, _ := newTestApp(t, "source")
	const id = "report.pdf"
	target := filepath.Join(t.TempDir(), id)
	if err := os.WriteFile(target, []byte("PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	vw := &viewer{id: id, pdfCancel: cancel}
	a.showViewer(vw)
	if err := os.Symlink(target, filepath.Join(a.vault.Root(), id)); err != nil {
		t.Fatal(err)
	}
	results := make(chan func(), 4)
	a.publishFn = func(apply func()) { results <- apply }
	a.filesChanged(a.vault, []mygo.FileEvent{{Op: mygo.FileWrite, Path: id}})
	publishedResult(t, results)
	view := ui.NewTester(func(c *ui.Context) { a.pdfFileView(c, a.viewing) }, 400, 400)
	view.Frame()
	if _, spinning := view.Find("正在打开 PDF"); spinning || ctx.Err() == nil || a.viewing.note == "" || a.viewing.pdfCancel != nil {
		t.Fatal("取消加载后未显示重读失败的状态")
	}
}
