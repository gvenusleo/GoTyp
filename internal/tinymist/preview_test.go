package tinymist

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// 切换主文件时，内存更新可能晚于入口编译；磁盘产物不能冒充当前快照或进入复用缓存。
func TestPreviewSwitchUsesMemoryResult(t *testing.T) {
	cl, f := startFake(t)
	results := make(chan string, 3)
	cl.OnPreview(func(_ Document, data []byte, err error) {
		if err == nil {
			results <- string(data)
		}
	})
	var firstConn *conn
	for i := range 2 {
		if i > 0 {
			fresh, server := startFake(t)
			server.got = f.got
			cl.launch = fresh.launch
		}
		doc := Document{Path: "/v/a.typ", Text: "memory", Version: 1, Session: uint64(i + 1)}
		done := make(chan error, 1)
		go func() { done <- cl.OpenContext(t.Context(), doc) }()
		output := previewOutput(t, f)
		f.next(t, "textDocument/didOpen")
		data := doc.Text
		if i > 0 && cl.conn == firstConn {
			// 旧进程会先导出磁盘版本，新进程没有尚未安装的上一会话内存事件。
			data = "disk"
		}
		if err := os.WriteFile(output, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if got := <-results; got != doc.Text {
			t.Errorf("预览 = %q，期望内存快照 %q", got, doc.Text)
		}
		if i > 0 {
			if cl.starts != 1 {
				t.Fatalf("主动切换会话消耗了崩溃重启次数：%d", cl.starts)
			}
			// 旧关闭任务即使排在新会话的补全同步之后，也不能关闭同一路径的新文档。
			cl.CloseDoc(doc.Path, doc.Session-1)
			if value, ok := cl.docs.Load(doc.Path); !ok || value.(*docState).doc != doc {
				t.Fatal("旧会话的关闭任务删除了新会话快照")
			}
			doc.Version++
			if err := cl.OpenContext(t.Context(), doc); err != nil {
				t.Fatal(err)
			}
			if got := <-results; got != doc.Text {
				t.Errorf("复用缓存 = %q，期望内存快照 %q", got, doc.Text)
			}
		}
		firstConn = cl.conn
		cl.CloseDoc(doc.Path, doc.Session)
	}
}

// 外部变化只失效磁盘缓存，不把大文件正文复制进 JSON-RPC，也不因文件较大重启编译器。
func TestRefreshInvalidatesWithoutCopyingFiles(t *testing.T) {
	cl, f := startFake(t)
	cl.OnPreview(func(Document, []byte, error) {})
	doc := Document{Path: "/v/a.typ", Text: "= A", Version: 1}
	done := make(chan error, 1)
	go func() { done <- cl.OpenContext(t.Context(), doc) }()
	output := previewOutput(t, f)
	f.next(t, "textDocument/didOpen")
	if err := os.WriteFile(output, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "large.bin")
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(32 << 20)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { done <- cl.Refresh(ctx, doc, []string{name, name}) }()
	req := <-f.got
	if req.Method != "tinymist/fsChange" {
		cancel()
		<-done
		t.Fatalf("文件变化触发了 %s，期望缓存失效", req.Method)
	}
	var params struct {
		Inserts []any    `json:"inserts"`
		Removes []string `json:"removes"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatal(err)
	}
	if len(params.Inserts) != 0 || !slices.Equal(params.Removes, []string{fileURI(name)}) {
		t.Fatalf("文件失效请求包含正文或重复路径：%+v", params)
	}
	f.reply(req.ID, "null")
	output = previewOutput(t, f)
	f.next(t, "textDocument/didChange")
	if err := os.WriteFile(output, []byte("refreshed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// 输入后立即删除可能只改变 buffer 版本；相同源码必须复用上一份 PDF，而不是等到超时。
func TestPreviewReusesUnchangedText(t *testing.T) {
	for _, compileError := range []bool{false, true} {
		t.Run(map[bool]string{false: "成功", true: "编译失败"}[compileError], func(t *testing.T) {
			cl, f := startFake(t)
			type result struct {
				doc  Document
				data string
				err  error
			}
			results := make(chan result, 2)
			cl.OnPreview(func(doc Document, data []byte, err error) { results <- result{doc, string(data), err} })
			doc := Document{Path: "/v/a.typ", Text: "= A", Version: 1}
			done := make(chan error, 1)
			go func() { done <- cl.OpenContext(t.Context(), doc) }()
			output := previewOutput(t, f)
			f.next(t, "textDocument/didOpen")
			if compileError {
				f.notify("tinymist/compileStatus", `{"path":"/v/a.typ","status":"compileError"}`)
			} else if err := os.WriteFile(output, []byte("pdf"), 0o600); err != nil {
				t.Fatal(err)
			}
			first := <-results
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			doc.Version = 3
			go func() { done <- cl.OpenContext(t.Context(), doc) }()
			select {
			case next := <-results:
				if next.doc != doc || next.data != first.data || next.err != first.err {
					t.Errorf("未复用编译结果：%+v", next)
				}
			case m := <-f.got:
				t.Errorf("相同源码重新等待输出：%s", m.Method)
				cl.cancel()
			}
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

// 每次调用使用自己的取消信号，第一次打开文档的 context 不能覆盖后来的调用。
func TestPreviewUsesCurrentCallContext(t *testing.T) {
	cl, f := startFake(t)
	cl.OnPreview(func(Document, []byte, error) {})
	doc := Document{Path: "/v/a.typ", Text: "first", Version: 1}
	done := make(chan error, 1)
	go func() { done <- cl.OpenContext(t.Context(), doc) }()
	output := previewOutput(t, f)
	f.next(t, "textDocument/didOpen")
	if err := os.WriteFile(output, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	doc.Version, doc.Text = 2, "second"
	go func() { done <- cl.OpenContext(ctx, doc) }()
	previewOutput(t, f)
	f.next(t, "textDocument/didChange")
	cancel()
	if p := cl.pdfWait.Load(); p != nil && p.ctx.Err() == nil {
		t.Error("本次调用取消后仍等待第一次打开文档时的 context")
		cl.cancel()
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("取消结果 = %v", err)
	}
}

// 补全抢在打开队列之前同步时，也必须继承请求的取消，而不是客户端生命周期。
func TestPreviewCompletionBeforeOpenCanCancel(t *testing.T) {
	cl, f := startFake(t)
	cl.OnPreview(func(Document, []byte, error) {})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := cl.Complete(ctx, Document{Path: "/v/a.typ", Text: "#te", Version: 1, Session: 1}, 3)
		done <- err
	}()
	previewOutput(t, f)
	f.next(t, "textDocument/didOpen")
	cancel()
	if p := cl.pdfWait.Load(); p != nil && p.ctx.Err() == nil {
		t.Error("补全提前同步时未使用请求的 context")
		cl.cancel()
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("取消结果 = %v", err)
	}
}
