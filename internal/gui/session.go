package gui

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"gotyp/internal/tinymist"
	"gotyp/internal/vault"
)

const (
	autosaveDelay = time.Second
	autosaveLimit = 10 * time.Second
	// saveRetryDelay 是写入失败后自动重试的间隔；冲突不自动重试。
	saveRetryDelay = 5 * time.Second
)

type session struct {
	id   string
	kind vault.Kind
	ed   *editor.State
	stat vault.FileStat

	saving    bool
	saveAgain bool
	afterSave func()
	conflict  bool
	deleted   bool // 冲突的原因是文件被删除
	err       string
	// retryPending 表示写入刚失败，下一帧据此安排 retryAt 的自动重试。
	retryPending bool
	retryAt      time.Time

	observed  uint64
	firstEdit time.Time
	lastEdit  time.Time

	source        string
	sourceVersion uint64
	md            markdown.Options

	// Typst 预览任务的状态，见 typstpreview.go。
	syncedVersion   uint64             // 已交给同步队列的 buffer 版本
	diagSnapshot    *tinymist.Document // 上次编辑器诊断使用的只读快照
	syncDue         time.Time          // 编辑停顿后同步的时刻，零值表示还没安排
	previewGen      uint64             // 启动或结束任务时加一，过期的启动结果据此丢弃
	previewTask     string
	previewURL      string
	previewStop     chan struct{} // 关闭后不再等待任务所在进程退出
	previewInvert   bool          // 任务启动时是否反转颜色
	previewStarting bool
	previewErr      string
	previewMissing  bool // 预览失败的原因是 tinymist 不可用
	previewErrOpen  bool
	scrollLine      int       // 上次让预览滚到的光标行
	scrollDue       time.Time // 光标停留后同步预览的时刻，零值表示没有待同步的移动
	formatting      bool

	statsCache   textStats
	statsVersion uint64
	statsReady   bool
	selCache     selectionCache

	// tinymist 补全与悬停的状态，见 complete.go。lspPath 为空表示未接入。
	lspPath        string
	completeCancel context.CancelFunc
	hoverCancel    context.CancelFunc
}

func newSession(id string, source string, st vault.FileStat) *session {
	buf := editor.NewBuffer(source)
	buf.MarkSaved()
	s := &session{
		id: id, stat: st,
		ed: &editor.State{Buffer: buf, SoftWrap: true, Placeholder: "开始输入", Label: "笔记源码"},
	}
	s.updateLanguage()
	s.observed = buf.Version()
	s.source, s.sourceVersion = source, buf.Version()
	return s
}

func (s *session) updateLanguage() {
	s.kind, _ = vault.KindFor(s.id)
	language := editor.LanguageFor(s.id)
	if s.kind == vault.KindTypst {
		language = typstLanguage
	}
	s.ed.Buffer.SetLanguage(language)
}

func (s *session) text() string {
	if version := s.ed.Buffer.Version(); version != s.sourceVersion {
		s.source = s.ed.Buffer.String()
		s.sourceVersion = version
	}
	return s.source
}

func (s *session) dirty() bool {
	return s != nil && s.ed.Buffer.Modified()
}

// applyEditorPrefs 应用与单篇笔记无关的编辑器偏好。
func (a *App) applyEditorPrefs(s *session) {
	s.ed.Font = ui.Font{Family: codeFamily(a.state.Appearance.CodeFont), Size: codeFontSize}
	s.ed.LineNumbers = editor.LineNumbersOff
	if a.lineNumbers {
		s.ed.LineNumbers = editor.LineNumbersOn
	}
}

// observe 核对 buffer 版本并安排自动保存。版本守卫保证一次编辑只安排一次任务。
func (a *App) observe(c *ui.Context, s *session) {
	version := s.ed.Buffer.Version()
	if version != s.observed {
		s.observed = version
		if s.dirty() {
			if s.firstEdit.IsZero() {
				s.firstEdit = c.Now()
			}
			s.lastEdit = c.Now()
		} else {
			s.firstEdit, s.lastEdit = time.Time{}, time.Time{}
		}
		a.previewChanged(s)
		a.syncWindow()
	}
	a.schedulePreview(c, s)
	if !s.dirty() || s.saving || s.conflict || a.busy {
		return
	}
	now := c.Now()
	if s.retryPending {
		s.retryPending = false
		s.retryAt = now.Add(saveRetryDelay)
	}
	due := s.lastEdit.Add(autosaveDelay)
	if latest := s.firstEdit.Add(autosaveLimit); latest.Before(due) {
		due = latest
	}
	if s.err != "" && s.retryAt.After(due) {
		due = s.retryAt
	}
	if !now.Before(due) {
		a.saveSession(s)
		return
	}
	c.After(due.Sub(now))
}

func (a *App) saveCurrent() {
	if a.current != nil {
		a.saveSession(a.current)
	}
}

func (a *App) saveSession(s *session) {
	if s.conflict {
		a.notify("文件存在外部修改，请先在提示行中处理")
		a.transitionFailed(s)
		return
	}
	if s.saving {
		s.saveAgain = true
		return
	}
	if !s.dirty() {
		action := s.afterSave
		s.afterSave = nil
		a.busy = false
		if action != nil {
			action()
		}
		return
	}
	v := a.vault
	if v == nil {
		return
	}
	snap := s.ed.Buffer.Snapshot()
	id, kind := s.id, s.kind
	expected := s.stat
	s.saving = true
	s.saveAgain = false
	s.err = ""
	go func() {
		st, err := v.Write(id, expected, snap)
		title := vault.Title(id, kind, snap.String())
		a.publish(func() { a.finishSave(s, v, snap, st, title, err) })
	}()
}

func (a *App) finishSave(s *session, v *vault.Vault, snap editor.Snapshot, st vault.FileStat, title string, err error) {
	if a.current != s || a.vault != v {
		return
	}
	s.saving = false
	if a.tree != nil {
		if _, ok := a.tree.Titles[s.id]; ok {
			a.tree.Titles[s.id] = title
		}
	}
	switch {
	case errors.Is(err, vault.ErrConflict):
		s.conflict = true
		s.deleted = errors.Is(err, vault.ErrDeleted)
		s.err = err.Error()
	case err != nil:
		s.err = "保存失败：" + err.Error()
		s.retryPending = true
	default:
		s.stat = st
		s.conflict, s.deleted = false, false
		s.err = ""
		s.retryAt = time.Time{}
		if s.ed.Buffer.Version() == snap.Version() {
			s.ed.Buffer.MarkSaved()
		}
	}
	if !s.dirty() {
		s.firstEdit, s.lastEdit = time.Time{}, time.Time{}
	}
	a.syncWindow()
	if err == nil && (s.saveAgain || (s.afterSave != nil && s.dirty())) {
		a.saveSession(s)
		return
	}
	action := s.afterSave
	s.afterSave = nil
	if err != nil {
		if action != nil {
			a.transitionFailed(s)
		}
		return
	}
	a.busy = false
	if action != nil {
		action()
	}
}

// switchAfterSave 在切换、关闭或退出前保存最新版本，失败时取消整个动作。
func (a *App) switchAfterSave(action func()) {
	a.busy = true
	if a.current == nil {
		a.busy = false
		action()
		return
	}
	a.current.afterSave = action
	a.saveSession(a.current)
}

// transitionFailed 在切换、关闭或退出前的保存失败时取消动作。关闭和退出时询问是否放弃未保存的修改，
// 否则冲突或持续的写入失败会让用户只能强制退出。
func (a *App) transitionFailed(s *session) {
	closing, quitting := a.closing, a.quitting
	a.cancelTransition()
	if (closing || quitting) && a.win != nil {
		// 推迟到当前事件处理结束后再弹出模态对话框，退出事件的回调中不阻塞。
		a.publish(func() { a.confirmDiscard(s, quitting) })
	}
}

func (a *App) confirmDiscard(s *session, quit bool) {
	if a.current != s {
		return
	}
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent: a.win, Type: mygo.MessageWarning,
		Message: "「" + path.Base(s.id) + "」的修改无法保存", Detail: s.err,
		Buttons: []string{"取消", "放弃修改"}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1},
	})
	if err != nil || res.Button != 1 {
		return
	}
	a.discarding = true
	if quit {
		mygo.App.Quit()
	} else {
		a.win.Close()
	}
}

func (a *App) cancelTransition() {
	a.pendingJump = nil
	a.busy = false
	a.closing = false
	a.quitting = false
	if a.current != nil {
		a.current.afterSave = nil
	}
	a.syncSelection()
}

func (a *App) reloadCurrent() {
	s := a.current
	if s == nil || a.vault == nil || a.busy {
		return
	}
	a.generation++
	generation := a.generation
	v := a.vault
	a.busy = true
	go func() {
		source, st, err := v.Read(s.id)
		a.publish(func() {
			if a.current != s || a.vault != v || a.generation != generation {
				return
			}
			a.busy = false
			if err != nil {
				s.err = "重新加载失败：" + err.Error()
				return
			}
			s.ed.Buffer.Reset(source)
			s.stat = st
			s.conflict, s.deleted = false, false
			s.err = ""
			s.observed = s.ed.Buffer.Version()
			s.source, s.sourceVersion = source, s.observed
			s.firstEdit, s.lastEdit = time.Time{}, time.Time{}
			a.previewChanged(s)
			a.syncWindow()
		})
	}()
}

// overwriteCurrent 在用户明确选择后，用当前编辑覆盖外部版本或重新创建已删除的文件。
// 以此刻的文件状态作为预期，写入前若又被修改仍会报告冲突。
func (a *App) overwriteCurrent() {
	s, v := a.current, a.vault
	if s == nil || v == nil || s.saving || a.busy {
		return
	}
	snap := s.ed.Buffer.Snapshot()
	id, kind := s.id, s.kind
	s.saving = true
	s.conflict, s.deleted = false, false
	s.err = ""
	go func() {
		cur, err := v.CurrentStat(id)
		var st vault.FileStat
		if err == nil {
			st, err = v.Write(id, cur, snap)
		}
		title := vault.Title(id, kind, snap.String())
		a.publish(func() { a.finishSave(s, v, snap, st, title, err) })
	}()
}

// discardCurrent 关闭已被删除的笔记并放弃其中的编辑。
func (a *App) discardCurrent() {
	if a.current == nil || a.current.saving || a.busy {
		return
	}
	a.closeSession()
	a.rescan()
}

func (a *App) saveCopyDialog() {
	s := a.current
	if s == nil {
		return
	}
	name, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent: a.win, Title: "另存为副本", DefaultPath: filepath.Base(s.id),
		CreateDirectories: true,
	})
	if err != nil || name == "" {
		return
	}
	snap := s.ed.Buffer.Snapshot()
	go func() {
		err := vault.WriteCopy(name, snap)
		a.publish(func() {
			if err != nil {
				a.notify("副本保存失败：" + err.Error())
			} else {
				a.notify("副本已保存")
			}
		})
	}()
}

func (a *App) syncWindow() {
	if a.win == nil {
		return
	}
	id := a.openID()
	if id == "" {
		a.win.SetTitle("GoTyp")
		a.win.SetDocumentEdited(false)
		a.win.SetRepresentedFile("")
		return
	}
	a.win.SetTitle(path.Base(id))
	a.win.SetDocumentEdited(a.current.dirty())
	if a.vault != nil {
		// 仓库根路径和文件 ID 已在读写时校验，窗口元数据无需再次访问文件系统。
		a.win.SetRepresentedFile(filepath.Join(a.vault.Root(), filepath.FromSlash(id)))
	}
}
