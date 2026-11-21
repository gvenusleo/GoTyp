package gui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestCloseTabsSavesUndoDuringWrite(t *testing.T) {
	a, s := newTestApp(t, "old\r\nsecond\n")
	a.state.DisableAutosave = true
	results := make(chan func(), 1)
	a.publishFn = func(fn func()) { results <- fn }
	s.ed.Buffer.Set("intermediate")
	a.saveSession(s)
	if !s.ed.Buffer.Undo() || s.dirty() {
		t.Fatal("撤销未还原到上次保存的内容")
	}
	a.showSession(newSession("other.md", "other", s.stat))
	a.closeTabs(func(*fileTab) bool { return true })
	publishedResult(t, results)
	if !a.busy || !s.dirty() || len(a.tabs) != 2 {
		t.Fatal("仍有编辑需要保存时关闭了标签")
	}
	publishedResult(t, results)
	got, _, err := a.vault.Read(s.id)
	if err != nil || got != "old\r\nsecond\n" || len(a.tabs) != 0 {
		t.Fatalf("关闭后的文件是 %q，标签数 %d，错误 %v", got, len(a.tabs), err)
	}
}

func TestRenameBackgroundTabKeepsExternalChange(t *testing.T) {
	a, s := newTestApp(t, "old")
	results := make(chan func(), 4)
	a.publishFn = func(fn func()) { results <- fn }
	a.showSession(newSession("other.md", "old", s.stat))
	if _, err := a.vault.Write(s.id, s.stat, strings.NewReader("external")); err != nil {
		t.Fatal(err)
	}
	a.filesChanged(a.vault, []mygo.FileEvent{{Op: mygo.FileWrite, Path: s.id}})
	publishedResult(t, results)
	a.startRename(s.id)
	a.rename.name = "renamed.md"
	a.commitRename()
	publishedResult(t, results)
	a.openNote("renamed.md")
	if a.current != s || !a.activeTab.checking {
		t.Fatal("改名后的标签没有继续刷新外部修改")
	}
	for range 2 {
		publishedResult(t, results)
	}
	if got := s.text(); got != "external" || s.dirty() {
		t.Fatalf("改名后显示 %q，未保存状态 %v", got, s.dirty())
	}
}

func TestRevealTabsAfterHeaderResize(t *testing.T) {
	a, first := newTestApp(t, "# 内容")
	a.showSession(newSession("readme.txt", "text", first.stat))
	for _, id := range []string{"一段很长的第三个标签的文件名称一.md", "一段很长的第四个标签的文件名称二.md"} {
		a.showSession(newSession(id, "# 内容", first.stat))
	}
	a.openNote("readme.txt")
	tt := ui.NewTester(a.view, 720, 480)
	tt.Key(ui.Ctrl, ui.KeyTab)
	area, _ := tt.Find("打开的文件")
	button, ok := tt.Find("关闭 " + a.tabs[2].id)
	if a.activeTab != a.tabs[2] || !ok || button.W != tabCloseSize || button.X+button.W > area.X+area.W-tabFadeWidth {
		t.Fatalf("活动标签未进入清晰滚动区：标签 %v，滚动区 %v", button, area)
	}
}
