package gui

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"gotyp/internal/tinymist"
	"gotyp/internal/vault"
)

const (
	// detailLimit 是补全列表中 detail 的最大字符数，Typst 函数签名常有上千字符。
	detailLimit = 48
	// docLimit 是补全文档摘要的最大字符数，约三行，避免挤掉条目列表。
	docLimit = 160
	// lspIdle 是没有打开 Typst 笔记多久后结束 tinymist。它常驻数百 MB，
	// 重新启动只要几秒，短暂切到别的笔记再回来时不必重启。
	lspIdle = 5 * time.Minute
)

// startLSP 为仓库建立 tinymist 客户端。进程在第一次打开 Typst 笔记时才启动。
func (a *App) startLSP(root string) {
	a.stopLSP()
	client := tinymist.New(a.ctx, root)
	client.OnDiagnostics(func(path string, doc *tinymist.Document, diags []tinymist.Diagnostic) {
		a.publish(func() { a.setDiagnostics(client, path, doc, diags) })
	})
	client.OnShowDocument(func(path string, at tinymist.Position) {
		a.publish(func() { a.showDocument(client, path, at) })
	})
	if a.preview != nil {
		client.OnPreview(func(doc tinymist.Document, data []byte, err error) {
			a.publish(func() { a.receivePDF(client, doc, data, err) })
		})
	}
	a.lsp = client
	a.lspQueue = &serialQueue{}
}

// setDiagnostics 记录一个文件的诊断。被引入的文件也会报告，诊断面板一并列出。
func (a *App) setDiagnostics(client *tinymist.Client, path string, doc *tinymist.Document, diags []tinymist.Diagnostic) {
	s := a.current
	if a.lsp != client || s == nil || s.lspPath == "" {
		return
	}
	// 文本版本变化后，相同的行列也要重新定位；通知携带的快照不会随排队期间的新编辑改变。
	same := slices.Equal(a.diags[path], diags)
	if path == s.lspPath && doc != nil && (!same || s.diagSnapshot != doc) {
		a.showEditorDiagnostics(s, *doc, diags)
		s.diagSnapshot = doc
	}
	if same {
		return
	}
	if len(diags) == 0 {
		delete(a.diags, path)
	} else {
		if a.diags == nil {
			a.diags = map[string][]tinymist.Diagnostic{}
		}
		a.diags[path] = diags
	}
	a.diagReport = a.formatDiagnostics()
}

// stopLSP 排在已提交的打开和关闭之后结束进程。
func (a *App) stopLSP() {
	if a.lsp != nil {
		a.lspQueue.run(a.lsp.Close)
		a.lsp, a.lspQueue = nil, nil
	}
}

// serialQueue 在一个后台 goroutine 中按提交顺序执行任务，不阻塞主线程。
// 打开和关闭文档必须保持顺序，否则快速切走再切回同一篇笔记时，关闭可能排在打开之后。
type serialQueue struct {
	mu      sync.Mutex
	jobs    []func()
	running bool
}

func (q *serialQueue) run(job func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, job)
	if !q.running {
		q.running = true
		go q.drain()
	}
}

func (q *serialQueue) drain() {
	for {
		q.mu.Lock()
		if len(q.jobs) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		job := q.jobs[0]
		q.jobs = q.jobs[1:]
		q.mu.Unlock()
		job()
	}
}

// attachLSP 让 Typst 笔记的编辑器向 tinymist 请求补全和悬停，并提前同步文档，让诊断和首次编译尽早开始。
func (a *App) attachLSP(s *session) {
	client := a.lsp
	if client == nil || a.vault == nil || s.kind != vault.KindTypst {
		return
	}
	// 仓库根路径已解析过符号链接，这里只拼接路径，不访问文件系统。
	s.lspPath = filepath.Join(a.vault.Root(), filepath.FromSlash(s.id))
	s.ed.OnCompletion = func(r editor.CompletionRequest) { a.requestCompletion(s, r) }
	s.ed.OnHover = func(r editor.HoverRequest) { a.requestHover(s, r) }
	s.ed.OnDefinition = func(offset int) { a.requestDefinition(s, offset) }
	s.ed.ContextMenu = a.typstContextMenu(s)
	a.lspSeq++
	s.lspID = a.lspSeq
	s.lspContext, s.lspCancel = context.WithCancel(a.ctx)
	a.lspIdleGen++
	a.syncLSP(s)
}

// detachLSP 在笔记不再显示时取消它的请求，并通知 tinymist 关闭文档。
func (a *App) detachLSP(s *session) {
	if s == nil || s.lspPath == "" {
		return
	}
	s.cancelLSP()
	s.ed.OnCompletion, s.ed.OnHover, s.ed.OnDefinition, s.ed.ContextMenu = nil, nil, nil, nil
	s.ed.SetDiagnostics(s.ed.Buffer.Version(), nil)
	s.diagSnapshot = nil
	a.diags, a.diagReport = nil, diagReport{}
	if client := a.lsp; client != nil {
		path := s.lspPath
		a.lspQueue.run(func() { client.CloseDoc(path) })
		a.scheduleLSPSuspend(client)
	}
	s.lspPath = ""
}

// scheduleLSPSuspend 在离开 Typst 笔记后安排结束 tinymist；期间打开了 Typst 笔记则作废。
func (a *App) scheduleLSPSuspend(client *tinymist.Client) {
	a.lspIdleGen++
	gen := a.lspIdleGen
	time.AfterFunc(lspIdle, func() {
		a.publish(func() {
			if a.lsp == client && a.lspIdleGen == gen && (a.current == nil || a.current.lspPath == "") {
				a.lspQueue.run(client.Suspend)
			}
		})
	})
}

func (s *session) lspDocument() tinymist.Document {
	return tinymist.Document{Path: s.lspPath, Text: s.text(), Version: s.ed.Buffer.Version(), Session: s.lspID}
}

func (s *session) cancelLSP() {
	for _, cancel := range []context.CancelFunc{s.completeCancel, s.hoverCancel, s.lspCancel} {
		if cancel != nil {
			cancel()
		}
	}
	s.completeCancel, s.hoverCancel, s.lspCancel = nil, nil, nil
}

func (a *App) requestCompletion(s *session, r editor.CompletionRequest) {
	client := a.lsp
	if client == nil || a.current != s || s.lspPath == "" || r.Version != s.ed.Buffer.Version() {
		return
	}
	// 中文正文的每次输入都会触发请求，前缀含非 ASCII 字符时只响应 Ctrl+Space，不必每次把全文发给 tinymist。
	if !r.Explicit && !isASCII(r.Prefix) {
		return
	}
	if s.completeCancel != nil {
		s.completeCancel()
	}
	ctx, cancel := context.WithCancel(s.lspContext)
	s.completeCancel = cancel
	doc := s.lspDocument()
	start := r.Offset - len(r.Prefix)
	go func() {
		items, err := client.Complete(ctx, doc, r.Offset)
		if err != nil {
			return
		}
		list := completions(items, doc.Text, start, r.Offset)
		a.publish(func() { a.finishCompletion(s, r.Version, r.Offset, start, list) })
	}()
}

// finishCompletion 只在笔记、buffer 版本和光标位置都没变时显示结果，返回是否显示。
// 光标移走后再弹出列表会打断用户，VS Code 和 Zed 也会丢弃这种结果。
func (a *App) finishCompletion(s *session, version uint64, offset, start int, list []editor.Completion) bool {
	if a.current != s || s.lspPath == "" || s.ed.Buffer.Version() != version || s.ed.Cursor() != offset {
		return false
	}
	s.ed.ShowCompletions(start, list)
	return true
}

func (a *App) requestHover(s *session, r editor.HoverRequest) {
	client := a.lsp
	if client == nil || a.current != s || s.lspPath == "" || r.Version != s.ed.Buffer.Version() {
		return
	}
	if s.hoverCancel != nil {
		s.hoverCancel()
	}
	ctx, cancel := context.WithCancel(s.lspContext)
	s.hoverCancel = cancel
	doc := s.lspDocument()
	go func() {
		h, ok, err := client.Hover(ctx, doc, r.Offset)
		if err != nil || !ok {
			return
		}
		if h.Start < 0 {
			h.Start, h.End = r.Offset, r.Offset
		}
		a.publish(func() { a.finishHover(s, r.Version, h) })
	}()
}

func (a *App) finishHover(s *session, version uint64, h tinymist.Hover) bool {
	if a.current != s || s.lspPath == "" || s.ed.Buffer.Version() != version {
		return false
	}
	md := &markdown.Options{Theme: a.themes.md}
	s.ed.ShowHover(h.Start, h.End, "", func(c *ui.Context) { hoverView(c, h.Markdown, md) })
	return true
}

// completions 把 tinymist 的补全转换为编辑器的补全。编辑器把整个列表插入到 start 与光标之间，
// 替换范围与之对不上的条目被丢弃，避免插入后留下或吞掉源码。
func completions(items []tinymist.Item, text string, start, offset int) []editor.Completion {
	list := make([]editor.Completion, 0, len(items))
	for _, it := range items {
		insert, ok := alignInsert(it, text, start, offset)
		if !ok {
			continue
		}
		cc := editor.Completion{
			Label: it.Label, Detail: shortDetail(it.Detail), Documentation: docSummary(it.Doc),
			Kind: completionKind(it.Kind), Insert: insert, FilterText: it.Filter,
		}
		if it.Snippet {
			if sn, err := editor.ParseSnippet(insert); err == nil {
				cc.Snippet = sn
			} else {
				cc.Insert = it.Label
			}
		}
		list = append(list, cc)
	}
	return list
}

// alignInsert 把条目的替换范围对齐到 [start, offset)。
func alignInsert(it tinymist.Item, text string, start, offset int) (string, bool) {
	switch {
	case it.Start < 0:
		return it.Text, true
	case it.End != offset:
		return "", false
	case it.Start == start:
		return it.Text, true
	case it.Start < start:
		// tinymist 的词包含编辑器不算作词的字符（如 `-`），去掉重复的这一段。
		extra := text[it.Start:start]
		if strings.HasPrefix(it.Text, extra) {
			return it.Text[len(extra):], true
		}
		return "", false
	default:
		// 编辑器的词比 tinymist 的范围长，保留范围之前的部分。
		return text[start:it.Start] + it.Text, true
	}
}

// shortDetail 只在列表中显示 detail 的第一行开头。
func shortDetail(detail string) string {
	if i := strings.IndexByte(detail, '\n'); i >= 0 {
		detail = detail[:i]
	}
	return truncate(detail, detailLimit)
}

var markdownLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// docSummary 取文档第一段的纯文本。MyGo 的补全面板按纯文本显示文档，且不限制它的高度，
// 文档过长会把条目列表挤没，完整文档留给悬停查看。
func docSummary(doc string) string {
	doc = strings.TrimSpace(doc)
	if i := strings.Index(doc, "\n\n"); i >= 0 {
		doc = doc[:i]
	}
	doc = markdownLink.ReplaceAllString(doc, "$1")
	doc = strings.NewReplacer("`", "", "\n", " ").Replace(doc)
	return truncate(doc, docLimit)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func truncate(s string, limit int) string {
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

// completionKind 映射 LSP 的 CompletionItemKind。
func completionKind(kind int) editor.CompletionKind {
	switch kind {
	case 2:
		return editor.CompletionMethod
	case 3, 4:
		return editor.CompletionFunction
	case 5, 10:
		return editor.CompletionField
	case 6:
		return editor.CompletionVariable
	case 7, 8, 13, 22:
		return editor.CompletionType
	case 9:
		return editor.CompletionModule
	case 12, 20, 21:
		return editor.CompletionConstant
	case 14:
		return editor.CompletionKeyword
	case 15:
		return editor.CompletionSnippet
	}
	return editor.CompletionText
}
