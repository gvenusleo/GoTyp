package gui

import (
	"path"

	"github.com/egoist/mygo"

	"gotyp/internal/vault"
)

type saveTransition struct {
	sessions     []*session
	allowDiscard bool
	done         func()
}

// afterSaving 等待目标会话保存最新版本，期间暂停编辑和文件操作。
func (a *App) afterSaving(sessions []*session, allowDiscard bool, done func()) {
	if len(sessions) == 0 {
		done()
		return
	}
	a.transition = &saveTransition{sessions: sessions, allowDiscard: allowDiscard, done: done}
	a.busy = true
	a.continueSaving(nil)
}

func (a *App) continueSaving(err error) {
	tr := a.transition
	for len(tr.sessions) > 0 {
		s := tr.sessions[0]
		switch {
		case err != nil && s.dirty():
			a.saveTransitionFailed(tr, s, err)
			return
		case s.saving:
			return
		case !s.dirty():
			err = nil
			tr.sessions = tr.sessions[1:]
		case s.conflict:
			a.saveTransitionFailed(tr, s, vault.ErrConflict)
			return
		default:
			a.saveSession(s)
			return
		}
	}
	a.transition = nil
	a.busy = false
	tr.done()
}

func (a *App) saveTransitionFailed(tr *saveTransition, s *session, saveErr error) {
	if !tr.allowDiscard || a.win == nil {
		a.transition = nil
		a.busy = false
		a.syncSelection()
		a.notify("无法保存「" + path.Base(s.id) + "」：" + saveErr.Error())
		return
	}
	// 原生关闭回调必须先返回，之后才能显示模态对话框。
	a.publish(func() {
		detail := s.err
		if detail == "" {
			detail = saveErr.Error()
		}
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent: a.win, Type: mygo.MessageWarning,
			Message: "「" + path.Base(s.id) + "」的修改无法保存", Detail: detail,
			Buttons: []string{"取消", "放弃修改"}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1},
		})
		if err != nil || res.Button != 1 {
			a.transition = nil
			a.busy = false
			return
		}
		tr.sessions = tr.sessions[1:]
		a.continueSaving(nil)
	})
}
