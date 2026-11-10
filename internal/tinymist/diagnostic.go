package tinymist

import (
	"encoding/json"
	"net/url"
	"path/filepath"
)

// Diagnostic 是 tinymist 报告的一条错误或警告。Start 和 End 是 LSP 坐标，调用方用 Offset
// 按诊断对应版本的文本换算为字节偏移。
type Diagnostic struct {
	Start, End Position
	Error      bool // 否则是警告
	Message    string
}

// OnDiagnostics 设置诊断的接收者：path 是绝对路径，diags 是这个文件当前的全部诊断，为空表示已清除。
// fn 在后台 goroutine 中按服务端发送的顺序调用。必须在第一次使用客户端之前设置。
func (cl *Client) OnDiagnostics(fn func(path string, diags []Diagnostic)) {
	cl.onDiag = fn
}

// OnShowDocument 设置预览跳回源码的接收者：path 是绝对路径，at 是目标位置。
// fn 在后台 goroutine 中调用。必须在第一次使用客户端之前设置。
func (cl *Client) OnShowDocument(fn func(path string, at Position)) {
	cl.onShow = fn
}

func (cl *Client) notification(method string, params json.RawMessage) {
	switch method {
	case "textDocument/publishDiagnostics":
		cl.diagnostics(params)
	case "window/showDocument":
		var p struct {
			URI       string    `json:"uri"`
			Selection *lspRange `json:"selection"`
		}
		if json.Unmarshal(params, &p) != nil || cl.onShow == nil {
			return
		}
		var at Position
		if p.Selection != nil {
			at = p.Selection.Start
		}
		if path := uriPath(p.URI); path != "" {
			cl.onShow(path, at)
		}
	}
}

func (cl *Client) diagnostics(params json.RawMessage) {
	if cl.onDiag == nil {
		return
	}
	var p struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Range    lspRange `json:"range"`
			Severity int      `json:"severity"`
			Message  string   `json:"message"`
		} `json:"diagnostics"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	path := uriPath(p.URI)
	if path == "" {
		return
	}
	var diags []Diagnostic
	seen := map[Diagnostic]bool{}
	for _, d := range p.Diagnostics {
		// LSP 的严重程度：1 错误，2 警告，3 信息，4 提示。后两种 Typst 不用于编译结果，不显示。
		if d.Severity != 1 && d.Severity != 2 {
			continue
		}
		diag := Diagnostic{Start: d.Range.Start, End: d.Range.End, Error: d.Severity == 1, Message: d.Message}
		// 预览任务的编译实例与主实例会对同一个文件各报一遍，合并后同一处错误出现两次。
		if !seen[diag] {
			seen[diag] = true
			diags = append(diags, diag)
		}
	}
	// tinymist 每次编译都会重发诊断。是否与上次相同由调用方判断：只有它知道界面何时清空过，
	// 在这里去重会在切换笔记后吞掉被引入文件的诊断。
	cl.onDiag(path, diags)
}

// uriPath 把 file URI 换回绝对路径，与 fileURI 相反。
func uriPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p := u.Path
	if len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:] // Windows 盘符路径
	}
	return filepath.FromSlash(p)
}
