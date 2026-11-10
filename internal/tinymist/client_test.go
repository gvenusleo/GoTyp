package tinymist

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/textproto"
	"strconv"
	"testing"
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
	cl.launch = func(ctx context.Context, root string) (*conn, error) {
		c := newConn(clientR, clientW)
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
		f.got <- m
	}
}

func (f *fakeServer) reply(id json.RawMessage, result string) {
	body := `{"jsonrpc":"2.0","id":` + string(id) + `,"result":` + result + `}`
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
