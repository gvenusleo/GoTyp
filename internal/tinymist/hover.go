package tinymist

import (
	"context"
	"encoding/json"
)

// Hover 是悬停文档。Start 和 End 是它说明的字节范围，服务端未给出范围时为 -1。
type Hover struct {
	Markdown   string
	Start, End int
}

// Hover 请求 doc 在 offset 处的悬停文档。没有内容时 ok 为 false。
func (cl *Client) Hover(ctx context.Context, doc Document, offset int) (h Hover, ok bool, err error) {
	var res *struct {
		Contents json.RawMessage `json:"contents"`
		Range    *lspRange       `json:"range"`
	}
	if err := cl.request(ctx, doc, "textDocument/hover", offset, &res); err != nil || res == nil {
		return Hover{}, false, err
	}
	h = Hover{Markdown: markupText(res.Contents), Start: -1, End: -1}
	if h.Markdown == "" {
		return Hover{}, false, nil
	}
	if res.Range != nil {
		start, end, err := toRange(doc.Text, *res.Range)
		if err != nil {
			return Hover{}, false, err
		}
		h.Start, h.End = start, end
	}
	return h, true, nil
}
