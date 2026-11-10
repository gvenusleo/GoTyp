package typst

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// typst watch 的 stderr 不是稳定接口，这里固定 0.15.1 的输出格式，升级后格式变化会在这里暴露。
func TestWatchParsesStatusAndPages(t *testing.T) {
	out := t.TempDir()
	w := &Watch{out: out, lines: make(chan string, 16), stamps: map[string]time.Time{}}
	feed := func(lines ...string) {
		for _, line := range lines {
			w.lines <- line
		}
	}
	feed("watching sub/note.typ", "writing to /tmp/out/page-{p}-of-{t}.png", "",
		"[13:35:10] compiled with errors", "", "sub/note.typ:2:9: error: unclosed delimiter")
	r, err := w.Next(context.Background())
	if err != nil || r.Err == nil || !strings.Contains(r.Err.Error(), "sub/note.typ:2:9") {
		t.Fatalf("error result = %+v, %v", r, err)
	}

	old := time.Now().Add(-time.Minute)
	for name, stamp := range map[string]time.Time{"page-1-of-3.png": old, "page-1-of-2.png": time.Now(), "page-2-of-2.png": time.Now()} {
		name := filepath.Join(out, name)
		if err := os.WriteFile(name, []byte(filepath.Base(name)), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(name, stamp, stamp)
	}
	feed("[13:35:11] compiled with warnings in 6.68 ms", "", "sub/note.typ:1:16: warning: unknown font family")
	r, err = w.Next(context.Background())
	if err != nil || r.Err != nil || len(r.Pages) != 2 || string(r.Pages[1]) != "page-2-of-2.png" {
		t.Fatalf("pages = %d, err = %v, %v", len(r.Pages), r.Err, err)
	}
	if !strings.Contains(r.Diagnostics, "warning") || r.Duration != 6680*time.Microsecond {
		t.Fatalf("diagnostics = %q", r.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(out, "page-1-of-3.png")); !os.IsNotExist(err) {
		t.Fatal("页数不符的旧页面没有删除")
	}

	feed("[13:35:12] compiled successfully in 5 ms")
	r, _ = w.Next(context.Background())
	if len(r.Pages) != 2 || r.Pages[0] != nil || r.Pages[1] != nil {
		t.Fatal("未变化的页面不应重新读取")
	}
}
