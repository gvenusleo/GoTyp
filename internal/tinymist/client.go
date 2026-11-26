// Package tinymist 通过 LSP 向 tinymist 同步 Typst 文本，请求补全、悬停和预览，并接收诊断。
// 它不依赖 GUI，所有方法都可以在后台 goroutine 中调用。
package tinymist

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrUnavailable 表示找不到 tinymist，或它反复崩溃后已停用。
var ErrUnavailable = errors.New("tinymist 不可用")

// errStale 表示请求版本比已同步版本旧。旧请求不能把服务端的文本退回去。
var errStale = errors.New("文档版本已过期")

const (
	initTimeout     = 10 * time.Second
	shutdownTimeout = 2 * time.Second
	// maxStarts 是一个仓库内最多启动进程的次数：首次启动加一次崩溃后的重启。
	maxStarts = 2
)

// Document 是一篇笔记在某个版本的快照。Path 是绝对路径。
type Document struct {
	Path    string
	Text    string
	Version uint64
	Session uint64 // 区分同一路径关闭后重新打开的编辑会话
}

// docState 是一篇已打开文档的不可变同步状态；lspVersion 是发给服务端的递增版本号。
type docState struct {
	conn       *conn
	doc        Document
	lspVersion int
	source     *sourceRead
}

type sourceRead struct {
	done chan struct{}
	err  error
}

// Client 管理一个仓库的 tinymist 进程。进程在第一次使用时启动。
type Client struct {
	ctx       context.Context
	cancel    context.CancelFunc
	root      string
	launch    func(ctx context.Context, root string) (*conn, error)
	onDiag    func(session uint64, path string, doc *Document, diags []Diagnostic)
	onShow    func(session uint64, path string, at Position)
	previewMu sync.Mutex

	mu       sync.Mutex
	conn     *conn
	starts   int
	disabled bool
	session  uint64
	// 诊断接收不能等待发送时持有的 mu，否则大文档可能堵住 stdio 管道的两端。
	docs sync.Map // string -> *docState，只替换，不改动已存入的快照
}

// New 返回仓库 root 的客户端，不启动进程。ctx 结束时进程随之结束。
func New(ctx context.Context, root string) *Client {
	ctx, cancel := context.WithCancel(ctx)
	return &Client{ctx: ctx, cancel: cancel, root: root, launch: launch}
}

// launch 启动 tinymist 并完成初始化握手。tinymist 在标准输入关闭时退出，
// 所以 GoTyp 崩溃时不会留下进程。
func launch(ctx context.Context, root string) (*conn, error) {
	if _, err := exec.LookPath("tinymist"); err != nil {
		return nil, ErrUnavailable
	}
	cmd := exec.CommandContext(ctx, "tinymist", "lsp")
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := newConn(stdout, stdin)
	exited := make(chan struct{})
	c.stopProcess = func() {
		_ = cmd.Process.Kill()
		<-exited
	}
	go func() {
		<-c.done
		cmd.Wait()
		close(exited)
	}()
	if err := initialize(ctx, c, root); err != nil {
		c.stop()
		return nil, err
	}
	return c, nil
}

func initialize(ctx context.Context, c *conn, root string) error {
	ctx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, c.stop)
	defer stop()
	uri := fileURI(root)
	params := map[string]any{
		"processId":        os.Getpid(),
		"rootUri":          uri,
		"workspaceFolders": []any{map[string]any{"uri": uri, "name": filepath.Base(root)}},
		"capabilities": map[string]any{
			"general": map[string]any{"positionEncodings": []string{"utf-16"}},
			"textDocument": map[string]any{
				"completion": map[string]any{"completionItem": map[string]any{
					"snippetSupport": true, "documentationFormat": []string{"markdown", "plaintext"},
				}},
				"hover":      map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
				"formatting": map[string]any{"dynamicRegistration": true},
			},
			"window": map[string]any{"showDocument": map[string]any{"support": true}},
		},
		// 预览按快照设置独立输出路径。
		"initializationOptions": map[string]any{
			"exportPdf":         "never",
			"projectResolution": "singleFile",
			"semanticTokens":    "disable", "formatterMode": "typstyle",
		},
	}
	if err := c.call(ctx, "initialize", params, nil); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

// connect 返回可用的连接，必要时启动或重启进程。调用方持有 mu。
func (cl *Client) connect() (*conn, error) {
	if c := cl.conn; c != nil {
		switch {
		case c.stopping.Load():
			c.stop()
			cl.starts--
		case !c.closed():
			return c, nil
		}
		cl.conn = nil
		cl.docs.Clear()
	}
	if cl.disabled || cl.ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	if cl.starts >= maxStarts {
		cl.disabled = true
		return nil, ErrUnavailable
	}
	cl.starts++
	c, err := cl.launch(cl.ctx, cl.root)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			cl.disabled = true
		}
		return nil, err
	}
	// 诊断在 didOpen 之后才会到达，这时接收者已经就位。
	session := cl.session
	c.handleNotes(func(method string, params json.RawMessage) { cl.notification(c, session, method, params) })
	cl.conn = c
	// 先接管主文件选择，避免首次 didOpen 的隐式聚焦与显式聚焦触发两次编译。
	if err := focus(c, nil); err != nil {
		cl.conn = nil
		c.stop()
		return nil, err
	}
	return c, nil
}

// request 请求 doc 在 offset 处的结果。
func (cl *Client) request(ctx context.Context, doc Document, method string, offset int, out any) error {
	params, err := positionParams(doc, offset)
	if err != nil {
		return err
	}
	return cl.syncCall(ctx, doc, method, params, out)
}

// positionParams 是指向 doc 中 offset 的 TextDocumentPositionParams。
func positionParams(doc Document, offset int) (map[string]any, error) {
	pos, err := toPosition(doc.Text, offset)
	if err != nil {
		return nil, err
	}
	return map[string]any{"textDocument": map[string]any{"uri": fileURI(doc.Path)}, "position": pos}, nil
}

// syncCall 先把文档同步到 doc 的版本，再在同一把锁内发出请求，保证服务端按这个版本的文本计算结果。
func (cl *Client) syncCall(ctx context.Context, doc Document, method string, params, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cl.mu.Lock()
	if err := ctx.Err(); err != nil {
		cl.mu.Unlock()
		return err
	}
	c, err := cl.syncLocked(ctx, doc)
	if err != nil {
		cl.mu.Unlock()
		return err
	}
	if err := ctx.Err(); err != nil {
		cl.mu.Unlock()
		return err
	}
	id, ch, err := c.send(method, params)
	cl.mu.Unlock()
	if err != nil {
		return err
	}
	return c.wait(ctx, id, ch, out)
}

// Open 同步文档，必要时启动进程。
func (cl *Client) Open(doc Document) error {
	return cl.OpenContext(cl.ctx, doc)
}

// OpenContext 同步快照，让编辑辅助和诊断跟上最新文本。
func (cl *Client) OpenContext(ctx context.Context, doc Document) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	_, err := cl.syncLocked(ctx, doc)
	return err
}

// syncLocked 同步快照，并在等待首次源码读取时释放 mu。
func (cl *Client) syncLocked(ctx context.Context, doc Document) (*conn, error) {
	for {
		c, err := cl.documentConnectionLocked(ctx, doc.Session)
		if err != nil {
			return nil, err
		}
		value, _ := cl.docs.Load(doc.Path)
		st, _ := value.(*docState)
		if st == nil || st.conn != c || st.doc.Session != doc.Session {
			if err := cl.openDocumentLocked(c, doc); err != nil {
				return nil, cl.disconnectLocked(c, err)
			}
			continue
		}
		if doc.Version < st.doc.Version {
			return nil, errStale
		}
		if waited, err := cl.waitForSourceLocked(ctx, st.source); err != nil || waited {
			if err != nil {
				return nil, err
			}
			continue
		}
		if st.source.err != nil {
			return nil, cl.disconnectLocked(c, st.source.err)
		}
		return cl.updateDocumentLocked(c, doc, st)
	}
}

func (cl *Client) documentConnectionLocked(ctx context.Context, session uint64) (*conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if session < cl.session {
		return nil, errStale
	}
	if session != cl.session && cl.conn != nil {
		// didClose 的文件缓存失效没有完成回执；新会话必须隔离尚未安装的旧内存事件。
		if c := cl.conn; c.stopping.Load() || !c.closed() {
			shutdown(ctx, c)
			cl.starts--
		}
		cl.conn = nil
		cl.docs.Clear()
	}
	cl.session = session
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cl.connect()
}

func (cl *Client) waitForSourceLocked(ctx context.Context, source *sourceRead) (bool, error) {
	select {
	case <-source.done:
		return false, nil
	default:
	}
	cl.mu.Unlock()
	select {
	case <-source.done:
	case <-ctx.Done():
	}
	cl.mu.Lock()
	return true, ctx.Err()
}

func (cl *Client) updateDocumentLocked(c *conn, doc Document, st *docState) (*conn, error) {
	if doc.Version == st.doc.Version {
		return c, nil
	}
	next := *st
	next.doc = doc
	if st.doc.Text == doc.Text {
		cl.docs.Store(doc.Path, &next)
		return c, nil
	}
	next.lspVersion++
	cl.docs.Store(doc.Path, &next)
	if err := c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": fileURI(doc.Path), "version": next.lspVersion},
		"contentChanges": []any{map[string]any{"text": doc.Text}},
	}); err != nil {
		return nil, cl.disconnectLocked(c, err)
	}
	return c, nil
}

func (cl *Client) disconnectLocked(c *conn, err error) error {
	cl.conn = nil
	cl.docs.Clear()
	if c.stopping.Load() {
		cl.starts--
	}
	c.stop()
	return errors.Join(ErrDisconnected, err)
}

func (cl *Client) openDocumentLocked(c *conn, doc Document) error {
	source := &sourceRead{done: make(chan struct{})}
	cl.docs.Store(doc.Path, &docState{conn: c, doc: doc, lspVersion: 1, source: source})
	uri := fileURI(doc.Path)
	if err := c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": uri, "languageId": "typst", "version": 1, "text": doc.Text,
	}}); err != nil {
		return err
	}
	if err := focus(c, doc.Path); err != nil {
		return err
	}
	// tinymist 0.15.12 首次读取源码后才登记缓存路径。提前发送 didChange
	// 可能使 exportPdf 复用首版快照。documentColor 会读取编译器中的源码，空数组也是有效回执。
	id, reply, err := c.send("textDocument/documentColor", map[string]any{"textDocument": map[string]string{"uri": uri}})
	if err != nil {
		return err
	}
	go cl.readSource(c, id, reply, source)
	return nil
}

func (cl *Client) readSource(c *conn, id int64, reply chan *message, source *sourceRead) {
	defer close(source.done)
	ctx, cancel := context.WithTimeout(cl.ctx, initTimeout)
	defer cancel()
	defer c.drop(id)
	var colors []json.RawMessage
	source.err = c.receive(ctx, reply, &colors)
	if source.err == nil && colors == nil {
		source.err = errors.New("tinymist 未能首次读取主文件")
	}
}

// focus 把主实例的主文件切到 path。tinymist 只在第一次 didOpen 时自动跟随，之后切换的笔记
// 不显式切换就收不到诊断。只发出请求，不等待结果，避免在持锁时阻塞。
func focus(c *conn, path any) error {
	id, ch, err := c.send("workspace/executeCommand", map[string]any{
		"command": "tinymist.focusMain", "arguments": []any{path},
	})
	if err != nil {
		return err
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
		defer cancel()
		c.wait(ctx, id, ch, nil)
	}()
	return nil
}

// CloseDoc 通知服务端关闭指定会话的 path，旧会话的关闭任务不能影响新会话。
func (cl *Client) CloseDoc(path string, session uint64) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	value, _ := cl.docs.Load(path)
	st, _ := value.(*docState)
	if st == nil || st.doc.Session != session {
		return
	}
	cl.docs.Delete(path)
	if st.conn.closed() {
		return
	}
	st.conn.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": fileURI(path)}})
}

// Close 按 LSP 约定结束进程，超时后强制结束。之后的请求都返回 ErrUnavailable。
func (cl *Client) Close() {
	cl.mu.Lock()
	c := cl.conn
	cl.conn = nil
	cl.disabled = true
	cl.mu.Unlock()
	defer cl.cancel()
	shutdown(cl.ctx, c)
}

// Suspend 结束空闲的进程，把字体和图片缓存占用的内存还给系统。客户端仍然可用，
// 下次请求时重新启动进程；主动结束不算崩溃，重启次数重新计算。
func (cl *Client) Suspend() {
	cl.mu.Lock()
	c := cl.conn
	cl.conn = nil
	if !cl.disabled {
		cl.starts = 0
	}
	// 旧连接上打开的文档在新进程中都要重新 didOpen。
	cl.docs.Clear()
	cl.mu.Unlock()
	shutdown(cl.ctx, c)
}

// shutdown 按 LSP 约定结束进程。超时后结束旧进程，即使此时尚有管道写入。
func shutdown(ctx context.Context, c *conn) {
	if c == nil {
		return
	}
	if c.closed() || c.stopping.Load() {
		c.stop()
		return
	}
	ctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, c.stop)
	defer stop()
	if c.call(ctx, "shutdown", nil, nil) == nil {
		c.notify("exit", nil)
	}
	c.w.Close()
	select {
	case <-c.done:
	case <-ctx.Done():
		c.stop()
	}
}

func fileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows 盘符路径
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
