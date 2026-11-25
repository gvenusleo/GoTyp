package gui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
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
	t.Cleanup(a.cancel)
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
	a.showSession(newSession("other.md", "other", s.stat))
	a.finishSave(s, a.vault, snap, s.stat, "note", nil)
	if !s.dirty() || s.saving {
		t.Fatalf("dirty = %v, saving = %v", s.dirty(), s.saving)
	}
}

// 编辑、切换或重新打开同一路径后，旧 PDF 结果不能改动新会话的预览状态。
func TestPreviewDropsStaleSnapshots(t *testing.T) {
	a := newApp()
	t.Cleanup(a.cancel)
	a.lsp = tinymist.New(a.ctx, t.TempDir())
	t.Cleanup(a.lsp.Close)
	s := newSession("note.typ", "= Title", vault.FileStat{})
	s.lspPath, s.lspID, s.previewStarting = "/v/note.typ", 1, true
	a.current = s
	doc := s.lspDocument()
	s.ed.Buffer.Set("= Changed")
	if a.receivePDF(a.lsp, doc, nil, tinymist.ErrCompile) || !s.previewStarting || s.previewErr != "" {
		t.Fatal("旧版本改动了当前预览状态")
	}
	doc = s.lspDocument()
	s.lspID++
	if a.receivePDF(a.lsp, doc, nil, tinymist.ErrCompile) || !s.previewStarting || s.previewErr != "" {
		t.Fatal("切回同一个标签后接受了早先激活的预览")
	}
	pending, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	s.previewCancel, s.previewReady = cancel, true
	a.receivePDF(a.lsp, s.lspDocument(), nil, tinymist.ErrCompile)
	if pending.Err() == nil || !s.previewReady || s.previewErr == "" {
		t.Fatal("当前编译失败后应取消待显示的 PDF 并保留已有画面")
	}
}

func TestPDFFileChangeCancelsPendingPreview(t *testing.T) {
	a, _ := newTestApp(t, "source")
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	vw := &viewer{id: "document.pdf", pdfCancel: cancel, pdfReady: true}
	a.showViewer(vw)
	// 等当前文件操作结束后再重读，先使已排队的 PDF 失效。
	a.busy = true
	a.readingPDF = newPDFRead("other.pdf")
	a.filesChanged(a.vault, []mygo.FileEvent{
		{Op: mygo.FileWrite, Path: vw.id},
		{Op: mygo.FileWrite, Path: "other.pdf"},
	})
	if ctx.Err() == nil || !a.activeTab.changed || !a.readingPDF.changed("other.pdf", nil) || !vw.pdfReady {
		t.Fatal("文件变化应使在途读取和渲染失效，并保留已有画面")
	}
}

// 切换笔记时编辑器按笔记 ID 区分，新笔记不能继承旧笔记的内容和撤销历史。
func TestSwitchingNotesKeepsEditorsApart(t *testing.T) {
	a, s := newTestApp(t, "same")
	a.state.DisableAutosave = true
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
		t.Fatalf("另一标签继承了编辑：%q", other.ed.Buffer.String())
	}
	want := s.ed.Buffer.String()
	s.ed.Select(0, len(want))
	if err := tt.Click("note.md"); err != nil {
		t.Fatal(err)
	}
	if a.current != s || !s.dirty() || s.ed.Buffer.String() != want || !s.ed.Buffer.CanUndo() || len(a.tabs) != 2 {
		t.Fatal("切回标签未保留编辑会话")
	}
	selection := s.ed.Selection()
	if selection.Start() != 0 || selection.End() != len(want) {
		t.Fatal("切回标签未保留选区")
	}
	tt.Key(ui.Ctrl, ui.KeyTab)
	if a.current != other {
		t.Fatal("快捷键没有切到右侧标签")
	}
	if err := tt.Click("关闭 other.md"); err != nil {
		t.Fatal(err)
	}
	if a.current != s || len(a.tabs) != 1 {
		t.Fatal("关闭标签后未激活邻居")
	}
}

func publishedResult(t *testing.T, results <-chan func()) {
	t.Helper()
	select {
	case apply := <-results:
		apply()
	case <-time.After(5 * time.Second):
		t.Fatal("后台任务未返回")
	}
}

func TestBackgroundTabAutosaves(t *testing.T) {
	a, s := newTestApp(t, "old")
	results := make(chan func(), 4)
	a.publishFn = func(fn func()) { results <- fn }
	tt := ui.NewTester(a.view, 1200, 800)
	s.ed.Buffer.Set("edited")
	tt.Frame()
	other := newSession("other.md", "old", s.stat)
	a.showSession(other)
	tt.Frame()
	tt.Advance(autosaveDelay)
	publishedResult(t, results)
	got, _, err := a.vault.Read(s.id)
	if err != nil || got != "edited" || s.dirty() || s.saving || a.current != other {
		t.Fatalf("后台保存状态异常：内容 %q，错误 %v，未保存 %v", got, err, s.dirty())
	}
}

func TestBackgroundConflictDoesNotBlockTabs(t *testing.T) {
	a, s := newTestApp(t, "old")
	a.state.DisableAutosave = true
	results := make(chan func(), 4)
	a.publishFn = func(fn func()) { results <- fn }
	s.ed.Buffer.Set("mine")
	other := newSession("other.md", "old", s.stat)
	a.showSession(other)
	if _, err := a.vault.Write(s.id, s.stat, strings.NewReader("external")); err != nil {
		t.Fatal(err)
	}
	a.filesChanged(a.vault, []mygo.FileEvent{{Path: s.id, Op: mygo.FileWrite}})
	for range 2 {
		publishedResult(t, results)
	}
	if !s.conflict || !s.dirty() || s.text() != "mine" || a.current != other {
		t.Fatal("后台文件冲突状态或编辑内容丢失")
	}
	a.openNote(s.id)
	a.openNote(other.id)
	if a.current != other || a.busy {
		t.Fatal("后台冲突阻止了标签切换")
	}
	called := false
	a.afterSaving(a.sessionsUnder(""), false, func() { called = true })
	if called || a.busy || !s.dirty() {
		t.Fatal("保存冲突时执行了会释放会话的操作")
	}
}

func TestCloseTabMenuSavesOnlyClosingTabs(t *testing.T) {
	a, first := newTestApp(t, "old")
	a.state.DisableAutosave = true
	results := make(chan func(), 1)
	a.publishFn = func(fn func()) { results <- fn }
	_, st, err := a.vault.Read("other.md")
	if err != nil {
		t.Fatal(err)
	}
	a.showSession(newSession("dir/other.md", "old", st))
	a.openNote(first.id)
	tt := ui.NewTester(a.view, 1200, 800)
	if err := tt.RightClick("dir/other.md"); err != nil {
		t.Fatal(err)
	}
	if a.current != first {
		t.Fatalf("打开后台标签的菜单时激活了 %s", a.openID())
	}
	if err := tt.ChooseMenuItem("关闭"); err != nil {
		t.Fatal(err)
	}
	if len(a.tabs) != 1 || a.current != first {
		t.Fatal("关闭单个后台标签影响了当前文件")
	}
	other := newSession("other.md", "old", st)
	a.showSession(other)
	a.openNote(first.id)
	first.ed.Buffer.Set("save first")
	other.ed.Buffer.Set("keep editing")
	tt.Frame()
	rightClick := func() {
		close, ok := tt.Find("关闭 " + other.id)
		if !ok {
			t.Fatal("找不到标签的关闭按钮")
		}
		tt.RightClickAt(close.X+close.W/2, close.Y+close.H/2)
	}
	rightClick()
	if a.current != first {
		t.Fatal("打开后台标签的菜单时切换了文件")
	}
	if err := tt.ChooseMenuItem("关闭其他标签页"); err != nil {
		t.Fatal(err)
	}
	publishedResult(t, results)
	tt.Frame()
	if len(a.tabs) != 1 || a.current != other || first.dirty() || !other.dirty() {
		t.Fatal("关闭其他标签页未能保存目标文件并保留右键标签的编辑")
	}
	a.showSession(first)
	first.ed.Buffer.Set("save again")
	tt.Frame()
	rightClick()
	if err := tt.ChooseMenuItem("关闭全部标签页"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		publishedResult(t, results)
	}
	tt.Frame()
	if len(a.tabs) != 0 || a.current != nil || first.dirty() || other.dirty() {
		t.Fatal("关闭全部标签页未能保存两个文件并清空标签栏")
	}
}

func TestWorkspaceRestoresTabsAndExplicitEmpty(t *testing.T) {
	a, s := newTestApp(t, "old")
	a.showSession(newSession("other.md", "old", s.stat))
	root := a.vault.Root()
	name := filepath.Join(t.TempDir(), "state.json")
	if err := vault.SaveState(name, a.state.Clone()); err != nil {
		t.Fatal(err)
	}
	state, err := vault.LoadState(name)
	if err != nil {
		t.Fatal(err)
	}
	b := newApp()
	t.Cleanup(b.cancel)
	b.vault, b.tree = a.vault, a.tree
	results := make(chan func(), 1)
	b.publishFn = func(fn func()) { results <- fn }
	b.restoreTabs(state.Workspaces[root])
	publishedResult(t, results)
	if len(b.tabs) != 2 || b.tabs[0].id != "note.md" || b.tabs[0].session != nil || b.openID() != "other.md" {
		t.Fatal("未按顺序恢复标签，或提前加载了后台文件")
	}
	a.removeTabs(func(*fileTab) bool { return true })
	if err := vault.SaveState(name, a.state.Clone()); err != nil {
		t.Fatal(err)
	}
	state, err = vault.LoadState(name)
	if err != nil {
		t.Fatal(err)
	}
	if w := state.Workspaces[root]; w.Tabs == nil || len(w.Tabs) != 0 || w.Selected != "" {
		t.Fatalf("空标签列表未保留：%+v", w)
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
