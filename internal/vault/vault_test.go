package vault

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveRejectsEscape(t *testing.T) {
	v := openTestVault(t)
	for _, id := range []string{"", ".", "../outside.md", "/absolute.md", `..\outside.md`} {
		if _, err := v.Resolve(id); !errors.Is(err, ErrOutside) {
			t.Errorf("Resolve(%q) error = %v", id, err)
		}
	}
}

func TestResolveRejectsSymlinkOutside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("创建符号链接可能需要额外权限")
	}
	v := openTestVault(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(v.Root(), "link.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Resolve("link.md"); !errors.Is(err, ErrOutside) {
		t.Fatalf("error = %v", err)
	}
}

func TestRenameDoesNotOverwrite(t *testing.T) {
	v := openTestVault(t)
	createTestNote(t, v, "a.md", "A")
	createTestNote(t, v, "b.md", "B")
	if _, err := v.Rename("a.md", "b.md"); !errors.Is(err, ErrExists) {
		t.Fatalf("error = %v", err)
	}
	if _, err := v.Rename("a.md", "../c.md"); err == nil {
		t.Fatal("允许了跨目录改名")
	}
	if source, _, err := v.Read("b.md"); err != nil || source != "B" {
		t.Fatalf("b.md = %q, %v", source, err)
	}
}

func TestMoveRejectsInvalidTargets(t *testing.T) {
	v := openTestVault(t)
	createTestNote(t, v, "source/sub/note.md", "原文")
	createTestNote(t, v, "target/note.md", "目标原文")
	if err := os.Mkdir(filepath.Join(v.Root(), "target", "source"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		id, dir string
		want    error
	}{
		{"source/sub/note.md", "target", ErrExists},
		{"source", "target", ErrExists},
		{"source", "source", nil},
		{"source", "source/sub", nil},
		{"source/sub/note.md", "../outside", ErrOutside},
	} {
		_, err := v.Move(tt.id, tt.dir)
		if err == nil || tt.want != nil && !errors.Is(err, tt.want) {
			t.Errorf("Move(%q, %q) = %v，预期失败 %v", tt.id, tt.dir, err, tt.want)
		}
	}
	for id, want := range map[string]string{"source/sub/note.md": "原文", "target/note.md": "目标原文"} {
		if got, _, err := v.Read(id); err != nil || got != want {
			t.Fatalf("%s = %q，错误 %v", id, got, err)
		}
	}
}

func TestMoveRejectsSymlinkOutside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("创建符号链接可能需要额外权限")
	}
	v := openTestVault(t)
	createTestNote(t, v, "note.md", "原文")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(v.Root(), "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Move("note.md", "outside"); !errors.Is(err, ErrOutside) {
		t.Fatalf("Move 错误 = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "note.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("仓库外出现了移动结果：%v", err)
	}
}

// 一个无法读取的子目录不能让整个仓库打不开。
func TestScanSkipsUnreadableDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("需要 Unix 权限且不能以 root 运行")
	}
	v := openTestVault(t)
	createTestNote(t, v, "ok.md", "# 可读")
	createTestNote(t, v, "locked/hidden.md", "")
	locked := filepath.Join(v.Root(), "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	tree, err := v.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if tree.Titles["ok.md"] != "可读" {
		t.Fatalf("titles = %v", tree.Titles)
	}
}
