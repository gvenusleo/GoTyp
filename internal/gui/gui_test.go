package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/tinymist"
	"gotyp/internal/vault"
)

func newTestApp(t *testing.T, source string) (*App, *session) {
	t.Helper()
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"note.md", "other.md"} {
		st, err := v.Create(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Write(id, st, strings.NewReader(source)); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := v.Scan()
	if err != nil {
		t.Fatal(err)
	}
	src, st, err := v.Read("note.md")
	if err != nil {
		t.Fatal(err)
	}
	a := newApp()
	a.vault, a.tree = v, tree
	a.publishFn = func(func()) {}
	s := newSession("note.md", src, st)
	a.showSession(s)
	return a, s
}

// 保存旧快照完成时，保存期间产生的新编辑必须仍是未保存状态。
func TestFinishSaveKeepsNewerEditDirty(t *testing.T) {
	a, s := newTestApp(t, "old")
	snap := s.ed.Buffer.Snapshot()
	s.saving = true
	s.ed.Buffer.Set("new")
	a.finishSave(s, a.vault, snap, s.stat, "note", nil)
	if !s.dirty() || s.saving {
		t.Fatalf("dirty = %v, saving = %v", s.dirty(), s.saving)
	}
}

// 新编辑之后才返回的旧编译结果不能替换预览，也不能吞掉待编译的新版本。
func TestFinishPreviewDropsStaleResult(t *testing.T) {
	a := newApp()
	s := newSession("note.typ", "= Title", vault.FileStat{})
	a.current = s
	s.renderGeneration = 2
	s.activeRender = 1
	s.previewPending = true
	a.finishPreview(s, previewResult{id: 1, version: 2, pages: []*ui.Bitmap{nil}})
	if s.activeRender != 0 || s.renderAttempted || !s.previewPending || len(s.previewPages) != 0 {
		t.Fatalf("stale result applied: %+v", s)
	}
}

// 切换笔记时编辑器按笔记 ID 区分，新笔记不能继承旧笔记的内容和撤销历史。
func TestSwitchingNotesKeepsEditorsApart(t *testing.T) {
	a, s := newTestApp(t, "same")
	tt := ui.NewTester(a.view, 1200, 800)
	if err := tt.Click("笔记源码"); err != nil {
		t.Fatal(err)
	}
	tt.Type("改动")
	if !s.dirty() {
		t.Fatal("edit did not mark session dirty")
	}
	other := newSession("other.md", "same", s.stat)
	a.showSession(other)
	tt.Frame()
	if other.dirty() || other.ed.Buffer.String() != "same" || other.ed.Buffer.CanUndo() {
		t.Fatalf("new session inherited edits: %q", other.ed.Buffer.String())
	}
}

// 常驻编译进程返回的旧版本结果不能替换预览，也不能结束等待中的新版本。
func TestFinishWatchDropsStaleResult(t *testing.T) {
	a := newApp()
	s := newSession("note.typ", "= Title", vault.FileStat{})
	a.current = s
	h := &watchHandle{}
	s.watch, s.watchWaiting = h, true
	s.ed.Buffer.Set("= Newer")
	a.finishWatch(s, h, previewResult{version: s.ed.Buffer.Version() - 1, pages: []*ui.Bitmap{nil}})
	if len(s.previewPages) != 0 || !s.watchWaiting || s.renderAttempted {
		t.Fatalf("stale watch result applied: %+v", s)
	}
}

// 文件被删除后，用户选择重新创建时用当前编辑写回原路径，并解除冲突。
func TestOverwriteRecreatesDeletedNote(t *testing.T) {
	a, s := newTestApp(t, "old")
	results := make(chan func(), 1)
	a.publishFn = func(fn func()) { results <- fn }
	s.ed.Buffer.Set("mine")
	name := filepath.Join(a.vault.Root(), "note.md")
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	s.conflict, s.deleted = true, true
	a.overwriteCurrent()
	(<-results)()
	data, err := os.ReadFile(name)
	if err != nil || string(data) != "mine" || s.conflict || s.dirty() {
		t.Fatalf("data = %q, err = %v, conflict = %v, dirty = %v", data, err, s.conflict, s.dirty())
	}
}

// 新编辑之后才返回的补全和悬停结果不能显示，切换笔记后旧笔记的结果也不能显示。
func TestFinishCompletionDropsStaleResult(t *testing.T) {
	a := newApp()
	s := newSession("note.typ", "#te", vault.FileStat{})
	s.lspPath = "/v/note.typ"
	a.current = s
	version := s.ed.Buffer.Version()
	s.ed.Buffer.Set("#tex")
	if a.finishCompletion(s, version, 3, 1, nil) {
		t.Fatal("旧版本的补全被显示")
	}
	if a.finishHover(s, version, tinymist.Hover{Markdown: "text", Start: 1, End: 3}) {
		t.Fatal("旧版本的悬停被显示")
	}
	other := newSession("other.typ", "", vault.FileStat{})
	a.current = other
	if a.finishCompletion(s, s.ed.Buffer.Version(), 4, 1, nil) {
		t.Fatal("已切走的笔记显示了补全")
	}
}
