package tinymist

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrOtherFiles 表示重命名要改动当前文档以外的文件。GoTyp 只改正在编辑的笔记。
var ErrOtherFiles = errors.New("重命名会改动其他文件")

// Edit 把 doc 中 Start 到 End 的字节替换为 Text。偏移都相对请求时的文本。
type Edit struct {
	Start, End int
	Text       string
}

// Location 是一个文件中的位置。Path 是绝对路径；At 是 LSP 坐标，由调用方按目标文件的文本换算。
type Location struct {
	Path string
	At   Position
}

// Heading 是文档中的一个标题。Level 从 1 开始，Offset 是标题名的字节偏移。
type Heading struct {
	Name   string
	Level  int
	Offset int
}

// symbolHeading 是 tinymist 在 documentSymbol 中给标题用的 SymbolKind（Namespace）。
const symbolHeading = 3

// Offset 把 LSP 坐标换算为 text 中的字节偏移，坐标与文本对不上时 ok 为假。
func Offset(text string, p Position) (int, bool) {
	off, err := toOffset(text, p)
	return off, err == nil
}

// Definition 请求 doc 在 offset 处符号的定义，没有定义时 ok 为假。
func (cl *Client) Definition(ctx context.Context, doc Document, offset int) (loc Location, ok bool, err error) {
	var raw json.RawMessage
	if err := cl.request(ctx, doc, "textDocument/definition", offset, &raw); err != nil {
		return Location{}, false, err
	}
	// 结果可能是单个 Location、Location 数组或 LocationLink 数组，统一按数组读取。
	type target struct {
		URI         string    `json:"uri"`
		Range       *lspRange `json:"range"`
		TargetURI   string    `json:"targetUri"`
		TargetRange *lspRange `json:"targetSelectionRange"`
	}
	var list []target
	if json.Unmarshal(raw, &list) != nil {
		var one target
		if json.Unmarshal(raw, &one) != nil {
			return Location{}, false, nil
		}
		list = []target{one}
	}
	for _, t := range list {
		uri, r := t.URI, t.Range
		if t.TargetURI != "" {
			uri, r = t.TargetURI, t.TargetRange
		}
		if path := uriPath(uri); path != "" && r != nil {
			return Location{Path: path, At: r.Start}, true, nil
		}
	}
	return Location{}, false, nil
}

type textEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// Format 请求格式化整篇文档，返回相对 doc.Text 的修改。tabSize 是缩进宽度。
func (cl *Client) Format(ctx context.Context, doc Document, tabSize int) ([]Edit, error) {
	var edits []textEdit
	params := map[string]any{
		"textDocument": map[string]any{"uri": fileURI(doc.Path)},
		"options":      map[string]any{"tabSize": tabSize, "insertSpaces": true},
	}
	if err := cl.syncCall(ctx, doc, "textDocument/formatting", params, &edits); err != nil {
		return nil, err
	}
	return toEdits(doc.Text, edits)
}

// PrepareRename 确认 offset 处的符号可以改名，返回它的字节范围和建议的名字。
func (cl *Client) PrepareRename(ctx context.Context, doc Document, offset int) (start, end int, name string, ok bool, err error) {
	var raw json.RawMessage
	if err := cl.request(ctx, doc, "textDocument/prepareRename", offset, &raw); err != nil {
		return 0, 0, "", false, err
	}
	var res struct {
		lspRange
		Range       *lspRange `json:"range"`
		Placeholder string    `json:"placeholder"`
	}
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &res) != nil {
		return 0, 0, "", false, nil
	}
	r := res.lspRange
	if res.Range != nil {
		r = *res.Range
	}
	start, end, err = toRange(doc.Text, r)
	if err != nil {
		return 0, 0, "", false, err
	}
	if name = res.Placeholder; name == "" {
		name = doc.Text[start:end]
	}
	return start, end, name, true, nil
}

// Rename 请求把 offset 处的符号改名为 name。修改涉及其他文件时返回 ErrOtherFiles，一处也不应用。
func (cl *Client) Rename(ctx context.Context, doc Document, offset int, name string) ([]Edit, error) {
	var res struct {
		Changes         map[string][]textEdit `json:"changes"`
		DocumentChanges []struct {
			Kind         string `json:"kind"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Edits []textEdit `json:"edits"`
		} `json:"documentChanges"`
	}
	params, err := positionParams(doc, offset)
	if err != nil {
		return nil, err
	}
	params["newName"] = name
	if err := cl.syncCall(ctx, doc, "textDocument/rename", params, &res); err != nil {
		return nil, err
	}
	var edits []textEdit
	for uri, list := range res.Changes {
		if uriPath(uri) != doc.Path {
			return nil, ErrOtherFiles
		}
		edits = append(edits, list...)
	}
	for _, dc := range res.DocumentChanges {
		// 带 kind 的是新建、改名或删除文件。
		if dc.Kind != "" || uriPath(dc.TextDocument.URI) != doc.Path {
			return nil, ErrOtherFiles
		}
		edits = append(edits, dc.Edits...)
	}
	return toEdits(doc.Text, edits)
}

func toEdits(text string, list []textEdit) ([]Edit, error) {
	edits := make([]Edit, 0, len(list))
	for _, e := range list {
		start, end, err := toRange(text, e.Range)
		if err != nil {
			return nil, err
		}
		edits = append(edits, Edit{Start: start, End: end, Text: e.NewText})
	}
	return edits, nil
}

// Headings 返回文档中的标题，按出现顺序，层级由嵌套深度决定。
func (cl *Client) Headings(ctx context.Context, doc Document) ([]Heading, error) {
	var symbols []documentSymbol
	params := map[string]any{"textDocument": map[string]any{"uri": fileURI(doc.Path)}}
	if err := cl.syncCall(ctx, doc, "textDocument/documentSymbol", params, &symbols); err != nil {
		return nil, err
	}
	var out []Heading
	collectHeadings(doc.Text, symbols, 1, &out)
	return out, nil
}

type documentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	SelectionRange lspRange         `json:"selectionRange"`
	Children       []documentSymbol `json:"children"`
}

func collectHeadings(text string, symbols []documentSymbol, level int, out *[]Heading) {
	for _, s := range symbols {
		if s.Kind != symbolHeading {
			continue
		}
		if off, err := toOffset(text, s.SelectionRange.Start); err == nil {
			*out = append(*out, Heading{Name: s.Name, Level: level, Offset: off})
		}
		collectHeadings(text, s.Children, level+1, out)
	}
}

// ScrollPreview 让预览任务 id 滚到 doc 中 offset 对应的位置。只发出请求，不等结果。
func (cl *Client) ScrollPreview(id string, doc Document, offset int) {
	pos, err := toPosition(doc.Text, offset)
	if err != nil {
		return
	}
	cl.mu.Lock()
	c := cl.conn
	cl.mu.Unlock()
	if c == nil || c.closed() {
		return
	}
	ctx, cancel := context.WithTimeout(cl.ctx, shutdownTimeout)
	defer cancel()
	c.call(ctx, "workspace/executeCommand", map[string]any{
		"command": "tinymist.scrollPreview",
		"arguments": []any{id, map[string]any{
			"event": "panelScrollTo", "filepath": doc.Path, "line": pos.Line, "character": pos.Character,
		}},
	}, nil)
}
