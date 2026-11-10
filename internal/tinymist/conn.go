package tinymist

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"sync"
)

// errClosed 表示连接已经断开，可能是进程退出，也可能是主动关闭。
var errClosed = errors.New("tinymist 连接已断开")

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("tinymist 错误 %d：%s", e.Code, e.Message) }

// message 同时承载请求、通知和响应，按字段是否存在区分。
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// conn 是一条 LSP 的 JSON-RPC 连接。写入串行进行，响应按请求 ID 分发。
type conn struct {
	w   io.WriteCloser
	wmu sync.Mutex

	mu      sync.Mutex
	next    int64
	pending map[int64]chan *message

	done chan struct{}
}

func newConn(r io.Reader, w io.WriteCloser) *conn {
	c := &conn{w: w, pending: map[int64]chan *message{}, done: make(chan struct{})}
	go c.readLoop(bufio.NewReader(r))
	return c
}

func (c *conn) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *conn) write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

func (c *conn) notify(method string, params any) error {
	return c.write(envelope(nil, method, params))
}

// envelope 组装请求或通知。没有参数时省略 params，JSON-RPC 不允许它为 null。
func envelope(id any, method string, params any) map[string]any {
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	return m
}

// send 发出请求并返回等待响应的通道。调用方据此把“发送”和“等待”分开，
// 让文本同步与请求在同一把锁内按顺序写出。
func (c *conn) send(method string, params any) (int64, chan *message, error) {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan *message, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	err := c.write(envelope(id, method, params))
	if err != nil {
		c.drop(id)
		return 0, nil, err
	}
	return id, ch, nil
}

func (c *conn) drop(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// wait 等待响应。ctx 先结束时通知服务端取消，并放弃这次结果。
func (c *conn) wait(ctx context.Context, id int64, ch chan *message, out any) error {
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out == nil || len(m.Result) == 0 {
			return nil
		}
		return json.Unmarshal(m.Result, out)
	case <-c.done:
		return errClosed
	case <-ctx.Done():
		c.drop(id)
		c.notify("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	}
}

func (c *conn) call(ctx context.Context, method string, params, out any) error {
	id, ch, err := c.send(method, params)
	if err != nil {
		return err
	}
	return c.wait(ctx, id, ch, out)
}

func (c *conn) readLoop(r *bufio.Reader) {
	defer close(c.done)
	tp := textproto.NewReader(r)
	for {
		header, err := tp.ReadMIMEHeader()
		if err != nil {
			return
		}
		n, err := strconv.Atoi(header.Get("Content-Length"))
		if err != nil || n < 0 {
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var m message
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		c.dispatch(&m)
	}
}

func (c *conn) dispatch(m *message) {
	switch {
	case m.Method != "" && len(m.ID) > 0:
		c.answer(m)
	case m.Method != "":
		// 通知（如诊断）暂不使用。
	default:
		id, err := strconv.ParseInt(string(m.ID), 10, 64)
		if err != nil {
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

// answer 回应服务端发来的请求。GoTyp 不提供任何客户端能力，一律返回空结果，避免服务端等待。
func (c *conn) answer(m *message) {
	result := json.RawMessage("null")
	if m.Method == "workspace/configuration" {
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		json.Unmarshal(m.Params, &p)
		nulls := make([]json.RawMessage, len(p.Items))
		for i := range nulls {
			nulls[i] = json.RawMessage("null")
		}
		result, _ = json.Marshal(nulls)
	}
	c.write(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{"2.0", m.ID, result})
}
