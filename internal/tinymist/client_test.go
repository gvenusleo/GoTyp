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

// 等待中的预览启动被取消时，服务端可能已经起了任务，要随即结束它，不留下占着端口的服务。
func TestStartPreviewCancelKillsTask(t *testing.T) {
	cl, f := startFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := cl.StartPreview(ctx, Document{Path: "/v/a.typ", Text: "= A", Version: 1}, "gotyp-1", false)
		done <- err
	}()
	f.next(t, "textDocument/didOpen")
	start := f.next(t, "workspace/executeCommand")
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("err = %v，期望 context.Canceled", err)
	}
	f.reply(start.ID, `{"staticServerPort":1}`)
	for {
		m := f.next(t, "workspace/executeCommand")
		var p struct {
			Command   string `json:"command"`
			Arguments []any  `json:"arguments"`
		}
		json.Unmarshal(m.Params, &p)
		if p.Command == "tinymist.doKillPreview" {
			if len(p.Arguments) != 1 || p.Arguments[0] != "gotyp-1" {
				t.Fatalf("结束的任务 = %v，期望 gotyp-1", p.Arguments)
			}
			return
		}
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

// 相同的诊断重复到达时每次都交给调用方。是否与界面上的相同只有调用方知道：
// 切换笔记清空界面后，被引入文件的同一条错误必须再次显示。
func TestRepeatedDiagnosticsAreDelivered(t *testing.T) {
	cl, f := startFake(t)
	got := make(chan int, 4)
	cl.OnDiagnostics(func(path string, diags []Diagnostic) { got <- len(diags) })
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
