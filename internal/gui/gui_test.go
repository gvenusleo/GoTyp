package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

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

// 启动期间切换了笔记，迟到的预览任务不能加载到新笔记上，进行中的新启动也不能被它结束。
func TestFinishPreviewDropsStaleTask(t *testing.T) {
	a := newApp()
	a.lsp = tinymist.New(a.ctx, t.TempDir())
	t.Cleanup(a.lsp.Close)
	s := newSession("note.typ", "= Title", vault.FileStat{})
	a.current = s
	s.previewGen, s.previewStarting = 2, true
	p := tinymist.Preview{ID: "gotyp-1", URL: "http://127.0.0.1:1/"}
	if a.finishPreview(s, a.lsp, 1, p, nil) {
		t.Fatal("过期的预览任务被加载")
	}
	if s.previewURL != "" || s.previewTask != "" || !s.previewStarting {
		t.Fatalf("过期结果改动了预览状态：%+v", s)
	}
	other := newSession("other.typ", "", vault.FileStat{})
	a.current = other
	if a.finishPreview(s, a.lsp, 2, p, nil) {
		t.Fatal("已切走的笔记加载了预览")
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

// 外部重载后，即使错误的行列和内容没变，也必须按新版本恢复诊断位置。
func TestDiagnosticsRefreshAfterReload(t *testing.T) {
	a := newApp()
	t.Cleanup(a.cancel)
	client := tinymist.New(a.ctx, t.TempDir())
	t.Cleanup(client.Close)
	a.lsp = client
	s := newSession("a.typ", "#foo\nold", vault.FileStat{})
	s.lspPath = "/v/a.typ"
	a.current = s
	diags := []tinymist.Diagnostic{{Start: tinymist.Position{Character: 1}, End: tinymist.Position{Character: 4}, Error: true, Message: "unknown variable: foo"}}
	doc := s.lspDocument()
	a.setDiagnostics(client, s.lspPath, &doc, diags)
	s.ed.Buffer.Reset("#foo\nnew")
	s.ed.Select(0, 0)
	fresh := s.lspDocument()
	a.setDiagnostics(client, s.lspPath, &fresh, diags)
	if got := s.ed.Diagnostics(); len(got) != 1 || got[0].Start != 1 || got[0].End != 4 {
		t.Fatalf("重载后诊断没有恢复：%+v", got)
	}
}

// 补全先同步了新文本时，诊断不能用界面防抖同步前的旧快照换算。
func TestDiagnosticsBeforeDebouncedSync(t *testing.T) {
	a := newApp()
	t.Cleanup(a.cancel)
	client := tinymist.New(a.ctx, t.TempDir())
	t.Cleanup(client.Close)
	a.lsp = client
	s := newSession("a.typ", "#foo\n", vault.FileStat{})
	s.lspPath = "/v/a.typ"
	a.current = s
	s.syncedVersion = s.ed.Buffer.Version()
	s.ed.Buffer.Edit(editor.Edit{Text: "\n"})
	doc := s.lspDocument()
	diags := []tinymist.Diagnostic{{Start: tinymist.Position{Line: 1, Character: 1}, End: tinymist.Position{Line: 1, Character: 4}, Error: true, Message: "unknown variable: foo"}}
	a.setDiagnostics(client, s.lspPath, &doc, diags)
	if got := s.ed.Diagnostics(); len(got) != 1 || got[0].Start != 2 || got[0].End != 5 {
		t.Fatalf("提前同步后的诊断位置错误：%+v", got)
	}
}

// 切换笔记清空诊断后，被引入文件报告的同一条错误必须重新显示，不能被当作重复丢掉；
// 已显示的诊断再次到达时不重排。
func TestDiagnosticsShownAgainAfterSwitch(t *testing.T) {
	a := newApp()
	client := tinymist.New(a.ctx, t.TempDir())
	t.Cleanup(client.Close)
	a.lsp = client
	s := newSession("a.typ", "= A", vault.FileStat{})
	s.lspPath = "/v/a.typ"
	a.current = s
	lib := []tinymist.Diagnostic{{Error: true, Message: "unclosed delimiter"}}
	a.setDiagnostics(client, "/v/lib.typ", nil, lib)
	if a.diagReport.errors != 1 {
		t.Fatalf("errors = %d，期望 1", a.diagReport.errors)
	}
	a.diags, a.diagReport = nil, diagReport{}
	a.setDiagnostics(client, "/v/lib.typ", nil, lib)
	if a.diagReport.errors != 1 {
		t.Fatalf("切换后 errors = %d，期望 1", a.diagReport.errors)
	}
}
