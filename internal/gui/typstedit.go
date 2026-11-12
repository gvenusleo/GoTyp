package gui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gotyp/internal/tinymist"
)

const (
	// lspTimeout 是跳转、格式化和重命名等一次性请求的上限，tinymist 卡住时不让操作一直挂着。
	lspTimeout = 10 * time.Second
)

// noteJump 是打开 id 后要移动到的位置。
type noteJump struct {
	id string
	at tinymist.Position
}

// symbolRenameState 是重命名符号对话框的状态，记下发起时的笔记、版本和位置。
type symbolRenameState struct {
	open    bool
	name    string
	s       *session
	version uint64
	offset  int
}

// typstReady 报告当前笔记能否使用 tinymist 的编辑命令。
func (a *App) typstReady() bool {
	s := a.current
	return s != nil && s.lspPath != "" && a.lsp != nil && !s.previewMissing
}

// typstContextMenu 把 Typst 编辑命令加到编辑器的右键菜单末尾。
func (a *App) typstContextMenu(s *session) func(m *ui.Menu) {
	return func(m *ui.Menu) {
		if a.current != s {
			return
		}
		ready := a.typstReady()
		m.Separator()
		if m.Item("跳转到定义").Shortcut(0, ui.KeyF12).Disabled(!ready).Chosen() {
			a.requestDefinition(s, s.ed.Cursor())
		}
		if m.Item("重命名符号…").Shortcut(0, ui.KeyF2).Disabled(!ready).Chosen() {
			a.startSymbolRename()
		}
		if m.Item("格式化文档").Shortcut(ui.Shift|ui.Alt, ui.KeyF).Disabled(!ready).Chosen() {
			a.formatCurrent()
		}
		if m.Item("跳到标题…").Shortcut(ui.Cmd|ui.Shift, ui.KeyO).Disabled(!ready).Chosen() {
			a.openHeadings()
		}
	}
}

// showEditorDiagnostics 在编辑器中标出当前笔记的诊断。坐标按诊断对应的已同步文本换算，
// 编辑器再按版本把它们移到之后的编辑上。
func (a *App) showEditorDiagnostics(s *session, doc tinymist.Document, diags []tinymist.Diagnostic) {
	list := make([]editor.Diagnostic, 0, len(diags))
	for _, d := range diags {
		start, ok := tinymist.Offset(doc.Text, d.Start)
		end, ok2 := tinymist.Offset(doc.Text, d.End)
		if !ok || !ok2 || end < start {
			continue
		}
		severity := editor.SeverityWarning
		if d.Error {
			severity = editor.SeverityError
		}
		list = append(list, editor.Diagnostic{Start: start, End: end, Severity: severity, Message: d.Message, Source: "typst"})
	}
	s.ed.SetDiagnostics(doc.Version, list)
}

// definitionAtCursor 供菜单使用，跳到光标处符号的定义。
func (a *App) definitionAtCursor() {
	if s := a.current; a.typstReady() {
		a.requestDefinition(s, s.ed.Cursor())
	}
}

func (a *App) requestDefinition(s *session, offset int) {
	client := a.lsp
	if client == nil || a.current != s || s.lspPath == "" {
		return
	}
	doc := s.lspDocument()
	ctx, cancel := context.WithTimeout(s.lspContext, lspTimeout)
	go func() {
		defer cancel()
		loc, ok, err := client.Definition(ctx, doc, offset)
		a.publish(func() {
			if a.current != s || s.ed.Buffer.Version() != doc.Version {
				return
			}
			switch {
			case err != nil:
				a.notify("无法跳转到定义：" + err.Error())
			case !ok:
				a.notify("没有找到定义")
			default:
				a.jumpTo(s, loc)
			}
		})
	}()
}

// jumpTo 移到 loc：当前笔记内直接移动光标，仓库内其他文件先按切换规则保存再打开。
func (a *App) jumpTo(s *session, loc tinymist.Location) {
	if loc.Path == s.lspPath {
		a.moveCursor(s, loc.At)
		return
	}
	id, ok := a.vaultID(loc.Path)
	if !ok {
		a.notify("目标在笔记仓库之外，无法打开")
		return
	}
	if a.busy {
		return
	}
	a.pendingJump = &noteJump{id: id, at: loc.At}
	a.openNote(id)
}

// vaultID 把绝对路径换成仓库中的文件 ID，不在仓库内或不在文件树中时 ok 为假。
func (a *App) vaultID(name string) (string, bool) {
	if a.vault == nil || a.tree == nil {
		return "", false
	}
	rel, err := filepath.Rel(a.vault.Root(), name)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	id := filepath.ToSlash(rel)
	return id, a.tree.Files[id]
}

// moveCursor 把光标移到 LSP 坐标 at 并滚到可见，坐标与当前文本对不上时什么也不做。
func (a *App) moveCursor(s *session, at tinymist.Position) bool {
	off, ok := tinymist.Offset(s.text(), at)
	if !ok {
		return false
	}
	s.ed.Select(off, off)
	s.ed.GoTo(s.ed.Buffer.Pos(off))
	return true
}

// applyPendingJump 在新会话显示后执行打开前记下的跳转。
func (a *App) applyPendingJump(s *session) {
	j := a.pendingJump
	a.pendingJump = nil
	if j != nil && j.id == s.id {
		a.moveCursor(s, j.at)
	}
}

// showDocument 处理服务端发起的源码跳转。
func (a *App) showDocument(client *tinymist.Client, name string, at tinymist.Position) {
	s := a.current
	if a.lsp != client || s == nil || s.lspPath == "" {
		return
	}
	if name == s.lspPath {
		a.moveCursor(s, at)
		return
	}
	a.jumpTo(s, tinymist.Location{Path: name, At: at})
}

// formatCurrent 用 typstyle 格式化当前笔记，结果作为一步撤销应用。
func (a *App) formatCurrent() {
	s, client := a.current, a.lsp
	if !a.typstReady() || s.formatting {
		return
	}
	doc := s.lspDocument()
	tab := s.ed.TabSize
	if tab <= 0 {
		tab = 2
	}
	s.formatting = true
	ctx, cancel := context.WithTimeout(s.lspContext, lspTimeout)
	go func() {
		defer cancel()
		edits, err := client.Format(ctx, doc, tab)
		a.publish(func() {
			s.formatting = false
			if a.current != s {
				return
			}
			switch {
			case err != nil:
				a.notify("无法格式化：" + err.Error())
			case s.ed.Buffer.Version() != doc.Version:
				a.notify("笔记已改动，请重新格式化")
			default:
				applyEdits(s, edits)
			}
		})
	}()
}

func applyEdits(s *session, edits []tinymist.Edit) {
	if len(edits) == 0 {
		return
	}
	list := make([]editor.Edit, len(edits))
	for i, e := range edits {
		list[i] = editor.Edit{Start: e.Start, End: e.End, Text: e.Text}
	}
	s.ed.Buffer.Edit(list...)
}

// startSymbolRename 先确认光标处可以改名，再打开对话框。
func (a *App) startSymbolRename() {
	s, client := a.current, a.lsp
	if !a.typstReady() || a.symbolRename.open {
		return
	}
	doc, offset := s.lspDocument(), s.ed.Cursor()
	ctx, cancel := context.WithTimeout(s.lspContext, lspTimeout)
	go func() {
		defer cancel()
		_, _, name, ok, err := client.PrepareRename(ctx, doc, offset)
		a.publish(func() {
			if a.current != s || s.ed.Buffer.Version() != doc.Version {
				return
			}
			if err != nil || !ok {
				a.notify("这里没有可以重命名的符号")
				return
			}
			a.symbolRename = symbolRenameState{open: true, name: name, s: s, version: doc.Version, offset: offset}
		})
	}()
}

// commitSymbolRename 只在全部修改都落在当前笔记时应用，涉及其他文件就整体放弃。
func (a *App) commitSymbolRename() {
	r := a.symbolRename
	a.symbolRename = symbolRenameState{}
	name := strings.TrimSpace(r.name)
	s, client := r.s, a.lsp
	if name == "" || client == nil || a.current != s || s.ed.Buffer.Version() != r.version {
		return
	}
	doc := s.lspDocument()
	ctx, cancel := context.WithTimeout(s.lspContext, lspTimeout)
	go func() {
		defer cancel()
		edits, err := client.Rename(ctx, doc, r.offset, name)
		a.publish(func() {
			if a.current != s {
				return
			}
			switch {
			case errors.Is(err, tinymist.ErrOtherFiles):
				a.notify("重命名会改动其他文件，已取消")
			case err != nil:
				a.notify("无法重命名：" + err.Error())
			case s.ed.Buffer.Version() != doc.Version:
				a.notify("笔记已改动，请重新重命名")
			default:
				applyEdits(s, edits)
			}
		})
	}()
}

// openHeadings 取当前笔记的标题，放进快速打开面板。
func (a *App) openHeadings() {
	s, client := a.current, a.lsp
	if !a.typstReady() {
		return
	}
	doc := s.lspDocument()
	ctx, cancel := context.WithTimeout(s.lspContext, lspTimeout)
	go func() {
		defer cancel()
		headings, err := client.Headings(ctx, doc)
		a.publish(func() {
			if a.current != s || s.ed.Buffer.Version() != doc.Version {
				return
			}
			if err != nil {
				a.notify("无法读取标题：" + err.Error())
				return
			}
			a.palette = paletteState{open: true, pointer: noPointer, headingMode: true, headings: headings, headingsFor: s, headingsVersion: doc.Version}
		})
	}()
}

// chooseHeading 把光标移到第 i 个标题。笔记在面板打开期间改动过时偏移已不可靠，只提示。
func (a *App) chooseHeading(i int) {
	p := a.palette
	s := p.headingsFor
	if s == nil || a.current != s || i < 0 || i >= len(p.headings) {
		return
	}
	if s.ed.Buffer.Version() != p.headingsVersion {
		a.notify("笔记已改动，请重新打开标题列表")
		return
	}
	off := p.headings[i].Offset
	s.ed.Select(off, off)
	s.ed.GoTo(s.ed.Buffer.Pos(off))
}
