package tinymist

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ErrCompile 表示当前快照编译失败，调用方应保留上次成功的预览。
var ErrCompile = errors.New("Typst 编译失败")

// ErrDisconnected 表示编译进程意外断开，下一次同步可以重新启动它。
var ErrDisconnected = errClosed

// pdfWaiter 只对应一份快照，通知接收不需要获取客户端的同步锁。
type pdfWaiter struct {
	ctx    context.Context
	cancel context.CancelFunc
	stop   func() bool
	conn   *conn
	path   string
	dir    string
	file   string
	failed chan struct{}
}

// OnPreview 接收自动生成的 PDF 或编译错误，doc 是产生它的确切快照。
// fn 在后台调用，必须在首次使用客户端之前设置。
func (cl *Client) OnPreview(fn func(doc Document, data []byte, err error)) {
	cl.onPDF = fn
}

func (cl *Client) beginPDF(ctx context.Context, c *conn, doc Document, entry bool) (*pdfWaiter, error) {
	if cl.onPDF == nil {
		return nil, nil
	}
	dir, err := os.MkdirTemp(cl.outDir, "preview-")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	p := &pdfWaiter{
		ctx: ctx, cancel: cancel, stop: context.AfterFunc(cl.ctx, cancel),
		conn: c, path: doc.Path, dir: dir, file: filepath.Join(dir, "preview.pdf"),
		failed: make(chan struct{}, 1),
	}
	cl.pdfWait.Store(p)
	when := "onType"
	if entry {
		// 首次打开和外部资源刷新没有新的源码内容，使用主文件更新事件触发。
		when = "script"
	}
	err = c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{
		"exportPdf": when, "outputPath": filepath.Join(dir, "preview"),
		"compileStatus": "enable", "semanticTokens": "disable", "formatterMode": "typstyle",
	}})
	if err != nil {
		cl.pdfWait.Store(nil)
		p.close()
		return nil, err
	}
	return p, nil
}

// waitPDF 只在有编译进行时检查输出；原子替换保证读到完整的一份 PDF。
func (cl *Client) waitPDF(p *pdfWaiter) ([]byte, error) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return nil, p.ctx.Err()
		case <-p.conn.done:
			return nil, ErrDisconnected
		case <-p.failed:
			return nil, ErrCompile
		case <-ticker.C:
			data, err := os.ReadFile(p.file)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return data, err
		}
	}
}

func (cl *Client) finishPDF(c *conn, doc Document, p *pdfWaiter) error {
	data, err := cl.waitPDF(p)
	cl.pdfWait.Store(nil)
	if err == nil || errors.Is(err, ErrCompile) {
		value, _ := cl.docs.Load(doc.Path)
		next := *value.(*docState)
		next.pdf, next.pdfErr = data, err
		cl.docs.Store(doc.Path, &next)
		cl.onPDF(doc, data, err)
		return nil
	}
	cl.onPDF(doc, nil, err)
	// 中断的编译没有版本化完成通知，关闭旧连接才能隔离迟到的输出。
	c.w.Close()
	cl.conn = nil
	cl.docs.Clear()
	if !errors.Is(err, ErrDisconnected) {
		cl.starts--
	}
	return err
}

func (p *pdfWaiter) close() {
	p.stop()
	p.cancel()
	os.RemoveAll(p.dir)
}

func (cl *Client) compileStatus(c *conn, params []byte) {
	p := cl.pdfWait.Load()
	if p == nil || p.conn != c {
		return
	}
	var status struct {
		Path   string `json:"path"`
		Status string `json:"status"`
	}
	if json.Unmarshal(params, &status) != nil || status.Status != "compileError" {
		return
	}
	rel, err := filepath.Rel(cl.root, p.path)
	if status.Path != p.path && (err != nil || filepath.ToSlash(status.Path) != "/"+filepath.ToSlash(rel)) {
		return
	}
	select {
	case p.failed <- struct{}{}:
	default:
	}
}

// Refresh 在外部资源变化后更新服务端的文件快照，并重新安排当前文档的自动预览。
func (cl *Client) Refresh(ctx context.Context, doc Document, paths []string) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := cl.connect()
	if err != nil {
		return err
	}
	changes, reset := previewFileChanges(paths)
	if reset {
		// 目录变化无法枚举受影响的缓存，重建文件环境后按需读取。
		shutdown(ctx, c)
		cl.conn, cl.starts = nil, 0
		cl.docs.Clear()
	} else if err := c.call(ctx, "tinymist/fsChange", changes, nil); err != nil {
		return err
	}
	_, err = cl.syncLocked(ctx, doc, true)
	return err
}

func previewFileChanges(paths []string) (map[string]any, bool) {
	removes := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		st, err := os.Stat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) || err == nil && st.IsDir() {
			return nil, true
		}
		// removes 清除 NotifyAccessModel 的缓存；下次编译只从磁盘读取实际依赖。
		removes = append(removes, fileURI(path))
	}
	return map[string]any{"inserts": []any{}, "removes": removes, "isSync": false}, false
}
