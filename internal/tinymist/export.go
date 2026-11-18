package tinymist

import (
	"context"
	"errors"
	"strings"
)

// ExportPDF 独立请求当前快照的 PDF 字节，不读取、覆盖或删除自动预览文件。
func (cl *Client) ExportPDF(ctx context.Context, doc Document) ([]byte, error) {
	cl.exportMu.Lock()
	defer cl.exportMu.Unlock()
	var res struct {
		Data []byte `json:"data"`
	}
	params := map[string]any{"command": "tinymist.exportPdf", "arguments": []any{
		doc.Path, map[string]any{}, map[string]any{"write": false},
	}}
	if err := cl.syncCall(ctx, doc, "workspace/executeCommand", params, &res); err != nil {
		if rpc, ok := errors.AsType[*rpcError](err); ok {
			return nil, errors.New(firstError(rpc.Message))
		}
		return nil, err
	}
	if len(res.Data) == 0 {
		return nil, errors.New("tinymist 没有返回 PDF 数据")
	}
	return res.Data, nil
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
