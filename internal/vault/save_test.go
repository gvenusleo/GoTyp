package vault

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func openTestVault(t *testing.T) *Vault {
	t.Helper()
	v, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func createTestNote(t *testing.T, v *Vault, id, source string) FileStat {
	t.Helper()
	if dir := filepath.Dir(filepath.Join(v.Root(), filepath.FromSlash(id))); dir != v.Root() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st, err := v.Create(id)
	if err != nil {
		t.Fatal(err)
	}
	st, err = v.Write(id, st, strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestWritePreservesContentAndPermissions(t *testing.T) {
	v := openTestVault(t)
	st := createTestNote(t, v, "notes/test.md", "first")
	name := filepath.Join(v.Root(), "notes", "test.md")
	if runtime.GOOS != "windows" {
		if err := os.Chmod(name, 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		st = newFileStat(info)
	}
	st, err := v.Write("notes/test.md", st, strings.NewReader("second"))
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := v.Read("notes/test.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != "second" {
		t.Fatalf("content = %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %o", info.Mode().Perm())
		}
		if !st.same(info) {
			t.Fatal("returned stat does not describe saved file")
		}
	}
}

func TestWriteRejectsExternalChange(t *testing.T) {
	v := openTestVault(t)
	st := createTestNote(t, v, "note.md", "mine")
	name := filepath.Join(v.Root(), "note.md")
	if err := os.WriteFile(name, []byte("external edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := v.Write("note.md", st, strings.NewReader("replacement"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v", err)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "external edit" {
		t.Fatalf("external file was overwritten: %q", data)
	}
}

func TestWriteRejectsExternalDeletion(t *testing.T) {
	v := openTestVault(t)
	st := createTestNote(t, v, "note.md", "mine")
	if err := os.Remove(filepath.Join(v.Root(), "note.md")); err != nil {
		t.Fatal(err)
	}
	_, err := v.Write("note.md", st, strings.NewReader("replacement"))
	if !errors.Is(err, ErrConflict) || !errors.Is(err, ErrDeleted) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(v.Root(), "note.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted note was recreated: %v", err)
	}
}

func TestCreateDoesNotTruncate(t *testing.T) {
	v := openTestVault(t)
	createTestNote(t, v, "note.md", "keep")
	_, err := v.Create("note.md")
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("error = %v", err)
	}
	got, _, err := v.Read("note.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != "keep" {
		t.Fatalf("content = %q", got)
	}
}

// 不是 UTF-8 文本的文件不能以文本读出：进了编辑器再保存，无效字节会被替换，原文件就被改坏了。
func TestReadRejectsNonText(t *testing.T) {
	v := openTestVault(t)
	for name, data := range map[string][]byte{
		"latin1.txt": {'c', 'a', 'f', 0xe9},
		"nul.md":     []byte("a\x00b"),
	} {
		if err := os.WriteFile(filepath.Join(v.Root(), name), data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := v.Read(name); !errors.Is(err, ErrNotText) {
			t.Errorf("Read(%q) error = %v", name, err)
		}
	}
}
