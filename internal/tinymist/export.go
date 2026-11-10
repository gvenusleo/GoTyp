package tinymist

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ExportPDF 把 doc 的文本导出为 PDF。tinymist 把产物写进客户端的临时目录，
// 这里读回后立即删除；产物不在临时目录中时拒绝读取，绝不删除仓库里的文件。
func (cl *Client) ExportPDF(ctx context.Context, doc Document) ([]byte, error) {
	// 产物按文件名放在同一个临时目录里，不同目录下的同名笔记同时导出会互相覆盖或删除，所以逐个导出。
	cl.exportMu.Lock()
	defer cl.exportMu.Unlock()
	var res struct {
		Path string `json:"path"`
	}
	params := map[string]any{"command": "tinymist.exportPdf", "arguments": []any{doc.Path}}
	if err := cl.syncCall(ctx, doc, "workspace/executeCommand", params, &res); err != nil {
		var rpc *rpcError
		if errors.As(err, &rpc) {
			return nil, errors.New(firstError(rpc.Message))
		}
		return nil, err
	}
	cl.mu.Lock()
	dir := cl.outDir
	cl.mu.Unlock()
	if res.Path == "" || dir == "" || !inside(dir, res.Path) {
		return nil, errors.New("tinymist 没有在临时目录中生成 PDF")
	}
	defer os.Remove(res.Path)
	return os.ReadFile(res.Path)
}

func inside(dir, name string) bool {
	rel, err := filepath.Rel(dir, name)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// firstError 从 tinymist 的导出错误中取出第一条 Typst 错误，其余的诊断面板里都有。
func firstError(msg string) string {
	i := strings.Index(msg, "error: ")
	if i < 0 {
		return msg
	}
	msg = msg[i+len("error: "):]
	if j := strings.IndexAny(msg, "\n\\"); j >= 0 {
		msg = msg[:j]
	}
	return strings.TrimSpace(msg)
}
