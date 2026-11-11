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

// errStale 表示请求的版本比已同步的旧。后台请求的先后不确定，旧请求不能把服务端的文本退回去。
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
}

// docState 是一篇已打开文档的不可变同步状态；lspVersion 是发给服务端的递增版本号。
type docState struct {
	conn       *conn
	doc        Document
	lspVersion int
}

// Client 管理一个仓库的 tinymist 进程。进程在第一次使用时启动。
type Client struct {
	ctx    context.Context
	cancel context.CancelFunc
	root   string
	launch func(ctx context.Context, root, outDir string) (*conn, error)
	onDiag func(path string, doc *Document, diags []Diagnostic)
	onShow func(path string, at Position)

	mu       sync.Mutex
	conn     *conn
	starts   int
	disabled bool
	// 诊断接收不能等待发送时持有的 mu，否则大文档可能堵住 stdio 管道的两端。
	docs sync.Map // string -> *docState，只替换，不改动已存入的快照
	// outDir 是 tinymist 导出文件的临时目录，第一次启动进程时创建，Close 时删除。
	outDir   string
	exportMu sync.Mutex
}

// New 返回仓库 root 的客户端，不启动进程。ctx 结束时进程随之结束。
func New(ctx context.Context, root string) *Client {
	ctx, cancel := context.WithCancel(ctx)
	return &Client{ctx: ctx, cancel: cancel, root: root, launch: launch}
}

// launch 启动 tinymist 并完成初始化握手。tinymist 在标准输入关闭时退出，
// 所以 GoTyp 崩溃时不会留下进程。
func launch(ctx context.Context, root, outDir string) (*conn, error) {
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
	go func() {
		<-c.done
		cmd.Wait()
	}()
	if err := initialize(ctx, c, root, outDir); err != nil {
		stdin.Close()
		return nil, err
	}
	return c, nil
}

func initialize(ctx context.Context, c *conn, root, outDir string) error {
	ctx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
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
		// GoTyp 不向仓库写入文件：tinymist 不自动导出，手动导出的产物写进临时目录。
		// 语义高亮由 GoTyp 自己的分词器负责；格式化用 tinymist 内置的 typstyle。
		"initializationOptions": map[string]any{
			"exportPdf": "never", "outputPath": filepath.Join(outDir, "$name"),
			"semanticTokens": "disable", "formatterMode": "typstyle",
		},
	}
	if err := c.call(ctx, "initialize", params, nil); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

// connect 返回可用的连接，必要时启动或重启进程。调用方持有 mu。
func (cl *Client) connect() (*conn, error) {
	if cl.conn != nil && !cl.conn.closed() {
		return cl.conn, nil
	}
	cl.conn = nil
	if cl.disabled || cl.ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	if cl.starts >= maxStarts {
		cl.disabled = true
		return nil, ErrUnavailable
	}
	if cl.outDir == "" {
		dir, err := makeTemp("tinymist")
		if err != nil {
			return nil, err
		}
		cl.outDir = dir
	}
	cl.starts++
	c, err := cl.launch(cl.ctx, cl.root, cl.outDir)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			cl.disabled = true
		}
		return nil, err
	}
	// 诊断在 didOpen 之后才会到达，这时接收者已经就位。
	c.handleNotes(func(method string, params json.RawMessage) { cl.notification(c, method, params) })
	cl.conn = c
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
	cl.mu.Lock()
	c, err := cl.syncLocked(doc)
	if err != nil {
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

// Open 同步文档，必要时启动进程。打开笔记时和编辑停顿后调用，让诊断和预览跟上最新文本。
func (cl *Client) Open(doc Document) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	_, err := cl.syncLocked(doc)
	return err
}

// syncLocked 在文档未打开时发送 didOpen，版本变化时发送全量 didChange。
func (cl *Client) syncLocked(doc Document) (*conn, error) {
	c, err := cl.connect()
	if err != nil {
		return nil, err
	}
	value, _ := cl.docs.Load(doc.Path)
	st, _ := value.(*docState)
	uri := fileURI(doc.Path)
	switch {
	case st == nil || st.conn != c:
		st = &docState{conn: c, doc: doc, lspVersion: 1}
		cl.docs.Store(doc.Path, st)
		err = c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": "typst", "version": st.lspVersion, "text": doc.Text,
		}})
		if err == nil {
			err = focus(c, doc.Path)
		}
	case doc.Version < st.doc.Version:
		return nil, errStale
	case st.doc.Version != doc.Version:
		st = &docState{conn: c, doc: doc, lspVersion: st.lspVersion + 1}
		cl.docs.Store(doc.Path, st)
		err = c.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": st.lspVersion},
			"contentChanges": []any{map[string]any{"text": doc.Text}},
		})
	}
	if err != nil {
		cl.docs.Delete(doc.Path)
		return nil, err
	}
	return c, nil
}

// focus 把主实例的主文件切到 path。tinymist 只在第一次 didOpen 时自动跟随，之后切换的笔记
// 不显式切换就收不到诊断。只发出请求，不等待结果，避免在持锁时阻塞。
func focus(c *conn, path string) error {
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

// CloseDoc 通知服务端不再编辑 path。进程未启动或已退出时什么也不做。
func (cl *Client) CloseDoc(path string) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	value, _ := cl.docs.LoadAndDelete(path)
	st, _ := value.(*docState)
	if st == nil || st.conn.closed() {
		return
	}
	st.conn.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": fileURI(path)}})
}

// Close 按 LSP 约定结束进程，超时后强制结束。之后的请求都返回 ErrUnavailable。
func (cl *Client) Close() {
	cl.mu.Lock()
	c, dir := cl.conn, cl.outDir
	cl.conn = nil
	cl.disabled = true
	cl.mu.Unlock()
	defer cl.cancel()
	if dir != "" {
		// 进程结束后才删，避免删除时它还在写导出文件。
		defer os.RemoveAll(dir)
	}
	if c == nil || c.closed() {
		return
	}
	ctx, cancel := context.WithTimeout(cl.ctx, shutdownTimeout)
	defer cancel()
	if c.call(ctx, "shutdown", nil, nil) == nil {
		c.notify("exit", nil)
	}
	c.w.Close()
	select {
	case <-c.done:
	case <-ctx.Done():
	}
}

func fileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows 盘符路径
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
