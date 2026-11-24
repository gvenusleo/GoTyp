package gui

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
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
