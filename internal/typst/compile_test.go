package typst

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func testVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "note.typ"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCompileReportsSyntaxError(t *testing.T) {
	if _, err := exec.LookPath("typst"); err != nil {
		t.Skip("未安装 Typst CLI")
	}
	root := testVault(t)
	_, err := Preview(context.Background(), Request{Root: root, LogicalPath: "sub/note.typ", Source: `#include "missing.typ"`})
	if err == nil {
		t.Fatal("missing include compiled successfully")
	}
}

func TestCompileHonorsCancellation(t *testing.T) {
	root := testVault(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Preview(ctx, Request{Root: root, LogicalPath: "sub/note.typ", Source: "text"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
