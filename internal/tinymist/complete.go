package tinymist

import (
	"context"
	"encoding/json"
	"sort"
)

// Item 是一条补全。Start 和 End 是 Text 替换的字节范围，服务端未给出范围时为 -1。
type Item struct {
	Label  string
	Detail string
	Doc    string
	// Kind 是 LSP 的 CompletionItemKind，由调用方映射为界面图标。
	Kind    int
	Text    string
	Snippet bool
	Start   int
	End     int
	// Filter 是服务端给出的匹配文本，为空时按 Label 匹配。
	Filter string
}

type completionItem struct {
	Label            string          `json:"label"`
	Kind             int             `json:"kind"`
	Detail           string          `json:"detail"`
	Documentation    json.RawMessage `json:"documentation"`
	FilterText       string          `json:"filterText"`
	SortText         string          `json:"sortText"`
	InsertText       string          `json:"insertText"`
	InsertTextFormat int             `json:"insertTextFormat"`
	TextEdit         *struct {
		NewText string    `json:"newText"`
		Range   *lspRange `json:"range"`
		// InsertReplaceEdit 的插入范围。
		Insert *lspRange `json:"insert"`
	} `json:"textEdit"`
}

// Complete 请求 doc 在 offset 处的补全。范围换算失败的条目被丢弃。
func (cl *Client) Complete(ctx context.Context, doc Document, offset int) ([]Item, error) {
	var raw json.RawMessage
	if err := cl.request(ctx, doc, "textDocument/completion", offset, &raw); err != nil {
		return nil, err
	}
	var list struct {
		Items []completionItem `json:"items"`
	}
	if json.Unmarshal(raw, &list.Items) != nil {
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
	}
	// 编辑器按匹配分数稳定排序，同分时保持这里的顺序，所以先按服务端建议的顺序排列，与 VS Code 一致。
	sort.SliceStable(list.Items, func(i, j int) bool { return sortKey(list.Items[i]) < sortKey(list.Items[j]) })
	items := make([]Item, 0, len(list.Items))
	for _, ci := range list.Items {
		if it, ok := convertItem(doc.Text, ci); ok {
			items = append(items, it)
		}
	}
	return items, nil
}

// sortKey 按 LSP 约定，没有 sortText 时用 label 排序。
func sortKey(ci completionItem) string {
	if ci.SortText != "" {
		return ci.SortText
	}
	return ci.Label
}

func convertItem(text string, ci completionItem) (Item, bool) {
	it := Item{
		Label: ci.Label, Detail: ci.Detail, Doc: markupText(ci.Documentation), Kind: ci.Kind,
		Text: ci.InsertText, Snippet: ci.InsertTextFormat == 2, Start: -1, End: -1, Filter: ci.FilterText,
	}
	if it.Text == "" {
		it.Text = ci.Label
	}
	if e := ci.TextEdit; e != nil {
		r := e.Range
		if r == nil {
			r = e.Insert
		}
		if r == nil {
			return Item{}, false
		}
		start, end, err := toRange(text, *r)
		if err != nil {
			return Item{}, false
		}
		it.Text, it.Start, it.End = e.NewText, start, end
	}
	return it, true
}

// markupText 取出 string、MarkupContent、MarkedString 或它们的数组中的文本。
// MarkedString 的代码按 Markdown 代码块返回。
func markupText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		out := ""
		for _, part := range list {
			if t := markupText(part); t != "" {
				if out != "" {
					out += "\n\n"
				}
				out += t
			}
		}
		return out
	}
	var m struct {
		Language string `json:"language"`
		Value    string `json:"value"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	if m.Language != "" {
		return "```" + m.Language + "\n" + m.Value + "\n```"
	}
	return m.Value
}
