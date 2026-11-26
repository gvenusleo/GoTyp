package tinymist

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrCompile 表示当前快照编译失败，调用方应保留上次成功的预览。
var ErrCompile = errors.New("Typst 编译失败")

// ErrDisconnected 表示编译进程意外断开，下一次同步可以重新启动它。
var ErrDisconnected = errClosed

// PreviewFile 是已完成的本地 PDF。调用方读取完毕后负责关闭它。
type PreviewFile struct {
	File *os.File
	Size int64
}

// Close 释放 PDF 的文件句柄和临时目录。
func (p *PreviewFile) Close() {
	_ = p.File.Close()
	_ = os.RemoveAll(filepath.Dir(p.File.Name()))
}

// Preview 显式导出快照。只串行化导出，等待回复期间仍可同步文本和发出编辑请求。
func (cl *Client) Preview(ctx context.Context, doc Document) (*PreviewFile, error) {
	cl.previewMu.Lock()
	defer cl.previewMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 文件可能比客户端活得更久，不能放在 Client.Close 删除的目录中。
	dir, err := makePreviewTemp()
	if err != nil {
		return nil, err
	}
	file, err := cl.exportPreview(ctx, doc, filepath.Join(dir, "preview"))
	if err != nil {
		_ = os.RemoveAll(dir)
	}
	return file, err
}

func (cl *Client) exportPreview(ctx context.Context, doc Document, output string) (*PreviewFile, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := cl.sendPreview(ctx, doc, output)
	if err != nil {
		return nil, err
	}
	var result struct {
		Path string `json:"path"`
	}
	if err := req.conn.receive(ctx, req.reply, &result); err != nil {
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, errClosed):
			req.conn.drop(req.id)
			cl.stopExport(req.conn, !errors.Is(err, errClosed))
		case strings.Contains(err.Error(), "document is not available for export"):
			return nil, ErrCompile
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := output + ".pdf"
	if filepath.Clean(result.Path) != filepath.Clean(name) {
		return nil, errors.New("tinymist 返回了错误的预览路径")
	}
	return openPreview(name)
}

type pendingRequest struct {
	conn  *conn
	id    int64
	reply chan *message
}

func (cl *Client) sendPreview(ctx context.Context, doc Document, output string) (*pendingRequest, error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	c, err := cl.syncLocked(ctx, doc)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 管道满时写入也可能挂起。关闭输入可让发送退出并回收该连接。
	stopWrite := context.AfterFunc(ctx, func() { _ = c.w.Close() })
	defer stopWrite()
	settings := map[string]any{"settings": map[string]any{"outputPath": output}}
	if err := c.notify("workspace/didChangeConfiguration", settings); err != nil {
		return nil, cl.previewSendError(c, ctx, err)
	}
	id, reply, err := c.send("workspace/executeCommand", map[string]any{
		"command":   "tinymist.exportPdf",
		"arguments": []any{doc.Path, map[string]any{}, map[string]bool{"write": true, "open": false}},
	})
	if err != nil {
		return nil, cl.previewSendError(c, ctx, err)
	}
	return &pendingRequest{c, id, reply}, nil
}

func (cl *Client) previewSendError(c *conn, ctx context.Context, err error) error {
	if canceled := ctx.Err(); canceled != nil {
		c.stopping.Store(true)
		cl.disconnectLocked(c, err)
		return canceled
	}
	return cl.disconnectLocked(c, err)
}

func (cl *Client) stopExport(c *conn, planned bool) {
	if planned {
		c.stopping.Store(true)
	}
	// 先结束旧进程，让正在写管道的调用释放同步锁，再清理文件。
	c.stop()
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.conn == c {
		cl.conn = nil
		cl.docs.Clear()
		if c.stopping.Load() {
			cl.starts--
		}
	}
}

func openPreview(name string) (*PreviewFile, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		file.Close()
		return nil, errors.New("预览 PDF 为空或不是普通文件")
	}
	return &PreviewFile{File: file, Size: info.Size()}, nil
}

// Refresh 重建编译环境，使新的导出从磁盘读取依赖。
func (cl *Client) Refresh(ctx context.Context, doc Document) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if doc.Session < cl.session {
		return errStale
	}
	if c := cl.conn; c != nil && doc.Session == cl.session {
		if value, ok := cl.docs.Load(doc.Path); ok {
			if current := value.(*docState).doc; current.Version > doc.Version {
				doc = current
			}
		}
		// 旧的编译快照可能在失效请求后继续登记依赖缓存。
		// 停掉旧进程，也让在途导出及时结束。
		cl.conn = nil
		cl.docs.Clear()
		if !c.closed() || c.stopping.Load() {
			cl.starts--
		}
		c.stop()
	}
	_, err := cl.syncLocked(ctx, doc)
	return err
}
