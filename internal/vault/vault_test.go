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
