package tinymist

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeServer 自动回应握手和主文件选择，其余消息交给测试。
type fakeServer struct {
	in         *bufio.Reader
	out        io.Writer
	got        chan message
	holdSource bool
}

func startFake(t *testing.T) (*Client, *fakeServer) {
	t.Helper()
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	f := &fakeServer{in: bufio.NewReader(serverR), out: serverW, got: make(chan message, 16)}
	go f.serve()
	cl := New(context.Background(), t.TempDir())
	cl.launch = func(ctx context.Context, root string) (*conn, error) {
		c := newConn(clientR, clientW)
		c.stopProcess = func() { _ = serverW.Close() }
		return c, initialize(ctx, c, root)
	}
	t.Cleanup(func() {
		clientW.Close()
		serverW.Close()
		cl.cancel()
	})
	return cl, f
}

func (f *fakeServer) serve() {
	tp := textproto.NewReader(f.in)
	for {
		h, err := tp.ReadMIMEHeader()
		if err != nil {
			return
		}
		n, _ := strconv.Atoi(h.Get("Content-Length"))
		body := make([]byte, n)
		if _, err := io.ReadFull(f.in, body); err != nil {
			return
		}
		var m message
		json.Unmarshal(body, &m)
		if m.Method == "initialize" {
			f.reply(m.ID, `{"capabilities":{}}`)
			continue
		}
		if m.Method == "shutdown" {
			f.reply(m.ID, "null")
			continue
		}
		if m.Method == "exit" {
			if closer, ok := f.out.(io.Closer); ok {
				closer.Close()
			}
			return
		}
		// 打开文档时附带的焦点切换与各测试关心的消息无关，直接回应。
		if m.Method == "workspace/executeCommand" && strings.Contains(string(m.Params), "tinymist.focusMain") {
			f.reply(m.ID, "null")
			continue
		}
		if m.Method == "textDocument/documentColor" && !f.holdSource {
			f.reply(m.ID, `[]`)
			continue
		}
		f.got <- m
	}
}

func (f *fakeServer) reply(id json.RawMessage, result string) {
	body := `{"jsonrpc":"2.0","id":` + string(id) + `,"result":` + result + `}`
	io.WriteString(f.out, "Content-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body)
}

func (f *fakeServer) notify(method, params string) {
	body := `{"jsonrpc":"2.0","method":"` + method + `","params":` + params + `}`
	io.WriteString(f.out, "Content-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body)
}

func (f *fakeServer) next(t *testing.T, method string) message {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case m := <-f.got:
			if m.Method == method {
				return m
			}
		case <-timer.C:
			t.Fatalf("没有收到 %s", method)
			return message{}
		}
	}
}

// 取消的请求要通知服务端，并且不能再返回结果。
func TestCompleteCancel(t *testing.T) {
	cl, f := startFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	doc := Document{Path: "/v/a.typ", Text: "#te", Version: 1}
	go func() {
		_, err := cl.Complete(ctx, doc, 3)
		done <- err
	}()
	f.next(t, "textDocument/didOpen")
	req := f.next(t, "textDocument/completion")
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("err = %v，期望 context.Canceled", err)
	}
	cancelMsg := f.next(t, "$/cancelRequest")
	var p struct {
		ID json.RawMessage `json:"id"`
	}
	json.Unmarshal(cancelMsg.Params, &p)
	if string(p.ID) != string(req.ID) {
		t.Fatalf("取消的 ID = %s，期望 %s", p.ID, req.ID)
	}
	// 迟到的响应没有人等待，不能阻塞读取循环。
	f.reply(req.ID, `[{"label":"text"}]`)
}

// 首次源码读取被扣住时，较新请求可以取消，且不能提前发送 didChange。
func TestCompleteWaitsForFirstSourceRead(t *testing.T) {
	cl, f := startFake(t)
	f.holdSource = true
	doc := Document{Path: "/v/a.typ", Text: "first", Version: 1}
	opened := make(chan error, 1)
	go func() { opened <- cl.OpenContext(t.Context(), doc) }()
	read := f.next(t, "textDocument/documentColor")
	ctx, cancel := context.WithCancel(t.Context())
	fresh := Document{Path: doc.Path, Text: "second", Version: 2}
	finished := make(chan error, 1)
	go func() { _, err := cl.Complete(ctx, fresh, 1); finished <- err }()
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("补全无法在首次源码读取期间取消")
	}
	select {
	case m := <-f.got:
		t.Fatalf("首次源码读取完成前发送了 %s", m.Method)
	default:
	}
	f.reply(read.ID, `[]`)
	if err := <-opened; err != nil {
		t.Fatal(err)
	}
}

// 版本变化时先发送全量文本，再发出请求，保证服务端按新文本计算。
func TestCompleteSyncsBeforeRequest(t *testing.T) {
	cl, f := startFake(t)
	if err := cl.Open(Document{Path: "/v/a.typ", Text: "#te", Version: 1}); err != nil {
		t.Fatal(err)
	}
	f.next(t, "textDocument/didOpen")
	go cl.Complete(context.Background(), Document{Path: "/v/a.typ", Text: "#tex", Version: 2}, 4)
	m := <-f.got
	if m.Method != "textDocument/didChange" {
		t.Fatalf("先收到 %s，期望 didChange", m.Method)
	}
	if m := <-f.got; m.Method != "textDocument/completion" {
		t.Fatalf("收到 %s，期望 completion", m.Method)
	}
}

// 晚到的旧请求不能把服务端的文本退回旧版本。
func TestStaleRequestDoesNotRewindText(t *testing.T) {
	cl, f := startFake(t)
	if err := cl.Open(Document{Path: "/v/a.typ", Text: "#tex", Version: 2}); err != nil {
		t.Fatal(err)
	}
	f.next(t, "textDocument/didOpen")
	if _, err := cl.Complete(context.Background(), Document{Path: "/v/a.typ", Text: "#te", Version: 1}, 3); err != errStale {
		t.Fatalf("err = %v，期望 errStale", err)
	}
	select {
	case m := <-f.got:
		t.Fatalf("旧请求发出了 %s", m.Method)
	default:
	}
}

// 重命名的修改只要涉及其他文件就整体拒绝，GoTyp 不在用户不知情时改写其他笔记。
func TestRenameRejectsOtherFiles(t *testing.T) {
	cl, f := startFake(t)
	doc := Document{Path: "/v/a.typ", Text: "#let x = 1\n#x", Version: 1}
	done := make(chan error, 1)
	go func() {
		_, err := cl.Rename(context.Background(), doc, 5, "y")
		done <- err
	}()
	req := f.next(t, "textDocument/rename")
	f.reply(req.ID, `{"changes":{"file:///v/a.typ":[{"range":{"start":{"line":0,"character":5},"end":{"line":0,"character":6}},"newText":"y"}],"file:///v/b.typ":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"y"}]}}`)
	if err := <-done; err != ErrOtherFiles {
		t.Fatalf("err = %v，期望 ErrOtherFiles", err)
	}
}

// 导出从磁盘解析相对依赖；成功和失败都不能覆盖仓库中的同名 PDF。
func TestExportFromDisk(t *testing.T) {
	if _, err := exec.LookPath("tinymist"); err != nil {
		t.Skip("未安装 tinymist")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(root, "note.typ")
	if err := os.WriteFile(note, []byte(`#include "part.typ"`), 0o600); err != nil {
		t.Fatal(err)
	}
	pdf := filepath.Join(root, "note.pdf")
	if err := os.WriteFile(pdf, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportPDF(t.Context(), root, note); err == nil {
		t.Fatal("缺失依赖时导出应失败")
	}
	if err := os.WriteFile(filepath.Join(root, "part.typ"), []byte("Saved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := ExportPDF(t.Context(), root, note); err != nil || !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Fatalf("导出失败：%v", err)
	}
	if data, err := os.ReadFile(pdf); err != nil || string(data) != "keep" {
		t.Fatalf("仓库中的 PDF 被改动：%q, %v", data, err)
	}
}

// 补全等请求提前同步的版本必须随诊断送达；已送达的快照不能被后续同步改写。
func TestDiagnosticsCarrySyncedSnapshot(t *testing.T) {
	cl, f := startFake(t)
	got := make(chan *Document, 4)
	cl.OnDiagnostics(func(_ uint64, _ string, doc *Document, _ []Diagnostic) { got <- doc })
	doc := Document{Path: "/v/a.typ", Text: "#foo\n", Version: 1}
	if err := cl.Open(doc); err != nil {
		t.Fatal(err)
	}
	f.next(t, "textDocument/didOpen")
	note := `{"uri":"file:///v/a.typ","diagnostics":[]}`
	f.notify("textDocument/publishDiagnostics", note)
	first := <-got
	if first == nil || *first != doc {
		t.Fatalf("初始诊断快照 = %+v，期望 %+v", first, doc)
	}
	fresh := Document{Path: doc.Path, Text: "\n#foo\n", Version: 2}
	done := make(chan error, 1)
	go func() {
		_, err := cl.Complete(t.Context(), fresh, 0)
		done <- err
	}()
	req := f.next(t, "textDocument/completion")
	f.notify("textDocument/publishDiagnostics", note)
	second := <-got
	if second == nil || *second != fresh || *first != doc {
		t.Fatalf("快照未更新或旧快照被改写：first=%+v，second=%+v", first, second)
	}
	f.reply(req.ID, `[]`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	cl.CloseDoc(doc.Path, doc.Session)
	f.notify("textDocument/publishDiagnostics", note)
	if stale := <-got; stale != nil {
		t.Fatalf("关闭文档后仍携带旧快照：%+v", stale)
	}
}

// 相同的诊断重复到达时每次都交给调用方。是否与界面上的相同只有调用方知道：
// 切换笔记清空界面后，被引入文件的同一条错误必须再次显示。
func TestRepeatedDiagnosticsAreDelivered(t *testing.T) {
	cl, f := startFake(t)
	got := make(chan uint64, 4)
	cl.OnDiagnostics(func(activation uint64, _ string, _ *Document, diags []Diagnostic) {
		if len(diags) == 1 {
			got <- activation
		}
	})
	const activation = 7
	if err := cl.Open(Document{Path: "/v/a.typ", Text: "= A", Version: 1, Session: activation}); err != nil {
		t.Fatal(err)
	}
	f.next(t, "textDocument/didOpen")
	note := `{"uri":"file:///v/lib.typ","diagnostics":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"severity":1,"message":"unclosed delimiter"}]}`
	f.notify("textDocument/publishDiagnostics", note)
	f.notify("textDocument/publishDiagnostics", note)
	for i := range 2 {
		select {
		case got := <-got:
			if got != activation {
				t.Fatalf("诊断会话 = %d，期望 %d", got, activation)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("第 %d 次诊断没有送达", i+1)
		}
	}
}
