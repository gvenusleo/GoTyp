package typst

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMirrorSkipsSymlinkOutside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("创建符号链接可能需要额外权限")
	}
	root := testVault(t)
	outside := filepath.Join(t.TempDir(), "secret.typ")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside.typ")); err != nil {
		t.Fatal(err)
	}
	mirror := filepath.Join(t.TempDir(), "vault")
	if _, err := writeMirror(root, mirror, "sub/note.typ", "snapshot"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(mirror, "outside.typ")); !os.IsNotExist(err) {
		t.Fatalf("outside link exists or unexpected error: %v", err)
	}
}
