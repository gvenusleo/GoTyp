package tinymist

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeServer 是受控的 JSON-RPC 对端：只回应 initialize，其余消息原样交给测试。
type fakeServer struct {
	in  *bufio.Reader
	out io.Writer
	got chan message
}

func startFake(t *testing.T) (*Client, *fakeServer) {
	t.Helper()
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	f := &fakeServer{in: bufio.NewReader(serverR), out: serverW, got: make(chan message, 16)}
	go f.serve()
	cl := New(context.Background(), t.TempDir())
	cl.launch = func(ctx context.Context, root, outDir string) (*conn, error) {
		c := newConn(clientR, clientW)
		return c, initialize(ctx, c, root, outDir)
	}
	t.Cleanup(func() {
		clientW.Close()
		serverW.Close()
		cl.cancel()
		if cl.outDir != "" {
			os.RemoveAll(cl.outDir)
		}
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
		// 打开文档时附带的焦点切换与各测试关心的消息无关，直接回应。
		if m.Method == "workspace/executeCommand" && strings.Contains(string(m.Params), "tinymist.focusMain") {
			f.reply(m.ID, "null")
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
	for m := range f.got {
		if m.Method == method {
			return m
		}
	}
	t.Fatalf("没有收到 %s", method)
	return message{}
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

// 等待自动 PDF 时取消必须结束旧连接，迟到的输出不能进入下一次同步。
func TestPreviewCancelClosesConnection(t *testing.T) {
	cl, f := startFake(t)
	cl.OnPreview(func(Document, []byte, error) {})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- cl.OpenContext(ctx, Document{Path: "/v/a.typ", Text: "= A", Version: 1}) }()
	f.next(t, "textDocument/didOpen")
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("err = %v，期望 context.Canceled", err)
	}
	if cl.conn != nil {
		t.Fatal("取消后仍保留旧连接")
	}
}

// 后一版源码必须等前一版 PDF 完成后才发送，输出的快照版本不能错配。
func TestPreviewSerializesSnapshots(t *testing.T) {
	cl, f := startFake(t)
	got := make(chan Document, 2)
	cl.OnPreview(func(doc Document, data []byte, err error) {
		if err == nil && string(data) == doc.Text {
			got <- doc
		}
	})
	first := Document{Path: "/v/a.typ", Text: "first", Version: 1}
	second := Document{Path: first.Path, Text: "second", Version: 2}
	done := make(chan error, 2)
	go func() { done <- cl.OpenContext(t.Context(), first) }()
	output := previewOutput(t, f)
	f.next(t, "textDocument/didOpen")
	go func() { done <- cl.OpenContext(t.Context(), second) }()
	if err := os.WriteFile(output, []byte(first.Text), 0o600); err != nil {
		t.Fatal(err)
	}
	if doc := <-got; doc != first {
		t.Fatalf("第一份快照 = %+v", doc)
	}
	output = previewOutput(t, f)
	f.next(t, "textDocument/didChange")
	if err := os.WriteFile(output, []byte(second.Text), 0o600); err != nil {
		t.Fatal(err)
	}
	if doc := <-got; doc != second {
		t.Fatalf("第二份快照 = %+v", doc)
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func previewOutput(t *testing.T, f *fakeServer) string {
	t.Helper()
	m := f.next(t, "workspace/didChangeConfiguration")
	var p struct {
		Settings struct {
			Output string `json:"outputPath"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		t.Fatal(err)
	}
	return p.Settings.Output + ".pdf"
}

// 编译错误应结束本次等待并允许后续编辑继续，不能等待不存在的 PDF 直到超时。
func TestPreviewCompileErrorKeepsConnection(t *testing.T) {
	cl, f := startFake(t)
	got := make(chan error, 1)
	cl.OnPreview(func(_ Document, _ []byte, err error) { got <- err })
	done := make(chan error, 1)
	go func() { done <- cl.OpenContext(t.Context(), Document{Path: "/v/a.typ", Text: "#(", Version: 1}) }()
	f.next(t, "textDocument/didOpen")
	f.notify("tinymist/compileStatus", `{"path":"/v/a.typ","status":"compileError"}`)
	if err := <-got; err != ErrCompile {
		t.Fatalf("编译结果 = %v", err)
	}
	if err := <-done; err != nil || cl.conn == nil {
		t.Fatalf("编译失败破坏了连接：%v", err)
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

// tinymist 报告的导出路径不在临时目录中时，既不读取也不删除，避免误删仓库里的文件。
func TestExportRefusesPathOutsideTempDir(t *testing.T) {
	cl, f := startFake(t)
	victim := filepath.Join(t.TempDir(), "note.pdf")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := cl.ExportPDF(context.Background(), Document{Path: "/v/a.typ", Text: "= A", Version: 1})
		done <- err
	}()
	req := f.next(t, "workspace/executeCommand")
	f.reply(req.ID, `{"path":`+strconv.Quote(victim)+`}`)
	if err := <-done; err == nil {
		t.Fatal("临时目录之外的导出结果被接受")
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatalf("临时目录之外的文件被改动：%q, %v", data, err)
	}
}

// 补全等请求提前同步的版本必须随诊断送达；已送达的快照不能被后续同步改写。
func TestDiagnosticsCarrySyncedSnapshot(t *testing.T) {
	cl, f := startFake(t)
	got := make(chan *Document, 4)
	cl.OnDiagnostics(func(_ string, doc *Document, _ []Diagnostic) { got <- doc })
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
	cl.CloseDoc(doc.Path)
	f.notify("textDocument/publishDiagnostics", note)
	if stale := <-got; stale != nil {
		t.Fatalf("关闭文档后仍携带旧快照：%+v", stale)
	}
}

// 相同的诊断重复到达时每次都交给调用方。是否与界面上的相同只有调用方知道：
// 切换笔记清空界面后，被引入文件的同一条错误必须再次显示。
func TestRepeatedDiagnosticsAreDelivered(t *testing.T) {
	cl, f := startFake(t)
	got := make(chan int, 4)
	cl.OnDiagnostics(func(path string, doc *Document, diags []Diagnostic) { got <- len(diags) })
	if err := cl.Open(Document{Path: "/v/a.typ", Text: "= A", Version: 1}); err != nil {
		t.Fatal(err)
	}
	f.next(t, "textDocument/didOpen")
	note := `{"uri":"file:///v/lib.typ","diagnostics":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"severity":1,"message":"unclosed delimiter"}]}`
	f.notify("textDocument/publishDiagnostics", note)
	f.notify("textDocument/publishDiagnostics", note)
	for i := range 2 {
		select {
		case n := <-got:
			if n != 1 {
				t.Fatalf("诊断条数 = %d，期望 1", n)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("第 %d 次诊断没有送达", i+1)
		}
	}
}
