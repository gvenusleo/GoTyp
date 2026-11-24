package gui

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMoveFolderSavesOpenNotes(t *testing.T) {
	a, first := newTestApp(t, "原文")
	folder, err := a.vault.CreateFolder("")
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.vault.CreateFolder("")
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.vault.Move("other.md", folder)
	if err != nil {
		t.Fatal(err)
	}
	_, st, err := a.vault.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	if a.tree, err = a.vault.Scan(); err != nil {
		t.Fatal(err)
	}
	other := newSession(id, "原文", st)
	a.showSession(other)
	a.openNote(first.id)
	other.ed.Buffer.Set("未保存的改动")
	results := make(chan func(), 4)
	a.publishFn = func(fn func()) { results <- fn }
	a.moveItem(folder, target)
	publishedResult(t, results)
	publishedResult(t, results)
	movedID := target + "/" + id
	got, _, err := a.vault.Read(movedID)
	if err != nil || got != "未保存的改动" {
		t.Fatalf("移动后的文件是 %q，错误 %v", got, err)
	}
	if a.current != first || other.id != movedID || a.findTab(movedID).session != other || other.dirty() {
		t.Fatal("后台标签没有保留会话并更新路径")
	}
	if !a.outline.Open.Has(target+"/"+folder) || !a.outline.Open.Has(target) {
		t.Fatal("目标目录未展开，或移动的目录丢失了展开状态")
	}
	if _, _, err := a.vault.Read(id); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("移动后原路径仍存在：%v", err)
	}
	publishedResult(t, results)
}

func TestMoveStopsOnSaveConflict(t *testing.T) {
	a, s := newTestApp(t, "原文")
	target, err := a.vault.CreateFolder("")
	if err != nil {
		t.Fatal(err)
	}
	if a.tree, err = a.vault.Scan(); err != nil {
		t.Fatal(err)
	}
	s.ed.Buffer.Set("未保存的改动")
	if _, err := a.vault.Write(s.id, s.stat, strings.NewReader("外部修改")); err != nil {
		t.Fatal(err)
	}
	results := make(chan func(), 1)
	a.publishFn = func(fn func()) { results <- fn }
	a.moveItem(s.id, target)
	publishedResult(t, results)
	got, _, err := a.vault.Read("note.md")
	if err != nil || got != "外部修改" || a.busy || !s.conflict || s.text() != "未保存的改动" || a.openID() != "note.md" {
		t.Fatalf("保存冲突后原路径或编辑状态异常：磁盘 %q，错误 %v", got, err)
	}
}

func TestLongFileTreeKeepsSidebarControlsVisible(t *testing.T) {
	a, _ := newTestApp(t, "原文")
	folder, err := a.vault.CreateFolder("")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 90 {
		if _, err := a.vault.Create(fmt.Sprintf("%s/笔记-%02d.md", folder, i)); err != nil {
			t.Fatal(err)
		}
	}
	if a.tree, err = a.vault.Scan(); err != nil {
		t.Fatal(err)
	}
	a.outline.Open.Add(folder)
	tt := ui.NewTester(a.view, 840, 480)
	for _, label := range []string{"设置", "移到" + vaultRootLabel} {
		if box, ok := tt.Find(label); !ok || box.H == 0 || box.Y+box.H > 480 {
			t.Fatalf("%q未保留在窗口内：%+v", label, box)
		}
	}
	bounds, ok := tt.Find("笔记")
	if !ok {
		t.Fatal("文件树不可见")
	}
	tt.Scroll(bounds.X+bounds.W/2, bounds.Y+bounds.H/2, 0, 2400)
	first, last := a.outline.List.Visible()
	if first <= 0 || last != a.outline.Rows()-1 {
		t.Fatalf("未滚动到文件树末尾：%d 到 %d，共 %d 行", first, last, a.outline.Rows())
	}
}
