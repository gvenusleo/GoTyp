package gui

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/tinymist"
	"gotyp/internal/vault"
)

const syncDelay = 150 * time.Millisecond

// previewChanged 取消尚未安装的旧 PDF，并在编辑停顿后提交新快照。
func (a *App) previewChanged(s *session) {
	if a.current != s || s.lspPath == "" {
		return
	}
	if s.previewCancel != nil {
		s.previewCancel()
		s.previewCancel = nil
	}
	s.syncDue = time.Time{}
	if !s.previewMissing {
		s.previewErr = ""
	}
}

func (a *App) schedulePreview(c *ui.Context, s *session) {
	if a.current != s || s.lspPath == "" || a.lsp == nil || (s.syncedVersion == s.observed && len(s.previewFiles) == 0) {
		return
	}
	if s.syncDue.IsZero() {
		s.syncDue = c.Now().Add(syncDelay)
	}
	if wait := s.syncDue.Sub(c.Now()); wait > 0 {
		c.After(wait)
	} else {
		a.syncLSP(s)
	}
}

func (a *App) syncLSP(s *session) {
	client, ctx, doc := a.lsp, s.lspContext, s.lspDocument()
	paths := s.previewFiles
	s.previewFiles = nil
	s.syncedVersion, s.syncDue = doc.Version, time.Time{}
	s.previewStarting = !s.previewReady
	a.lspQueue.run(func() {
		var err error
		if len(paths) > 0 {
			err = client.Refresh(ctx, doc, paths)
		} else {
			err = client.OpenContext(ctx, doc)
		}
		if err != nil {
			a.publish(func() { a.receivePDF(client, doc, nil, err) })
		}
	})
}

// receivePDF 先核对源码快照，再在后台解码；解析途中出现新编辑时取消安装。
func (a *App) receivePDF(client *tinymist.Client, doc tinymist.Document, data []byte, err error) bool {
	s := a.current
	if a.ctx.Err() != nil || a.lsp != client || s == nil || s.lspPath != doc.Path ||
		s.lspID != doc.Session || s.ed.Buffer.Version() != doc.Version {
		return false
	}
	if err != nil {
		s.previewStarting = false
		if errors.Is(err, context.Canceled) {
			if s.lspContext != nil && s.lspContext.Err() == nil {
				// 补全或悬停先同步后被取消时，预览仍需由当前会话重新安排。
				s.syncedVersion = 0
				s.previewStarting = !s.previewReady
			}
			return false
		}
		if errors.Is(err, tinymist.ErrDisconnected) {
			s.syncedVersion, s.previewErr = 0, ""
			s.previewStarting = !s.previewReady
			return false
		}
		s.previewMissing = errors.Is(err, tinymist.ErrUnavailable)
		s.previewErr = "无法更新 Typst 预览：" + err.Error()
		a.refreshMenu()
		return false
	}
	if a.preview == nil {
		return false
	}
	if s.previewCancel != nil {
		s.previewCancel()
	}
	ctx, cancel := context.WithCancel(s.lspContext)
	s.previewCancel = cancel
	viewer := a.preview
	go func() {
		err := viewer.Load(ctx, data)
		a.publish(func() { a.finishPDF(s, client, doc, ctx, err) })
	}()
	return true
}

func (a *App) finishPDF(s *session, client *tinymist.Client, doc tinymist.Document, ctx context.Context, err error) {
	if a.ctx.Err() != nil || ctx.Err() != nil || a.current != s || a.lsp != client ||
		s.lspID != doc.Session || s.ed.Buffer.Version() != doc.Version {
		return
	}
	s.previewStarting = false
	if err != nil {
		s.previewErr = "无法显示 Typst 预览：" + err.Error()
		return
	}
	s.previewReady, s.previewMissing, s.previewErr = true, false, ""
}

func (a *App) stopPreview(s *session) {
	if s == nil {
		return
	}
	if s.previewCancel != nil {
		s.previewCancel()
		s.previewCancel = nil
	}
	s.previewReady, s.previewStarting = false, false
	if a.preview != nil {
		a.preview.Clear()
	}
}

func (s *session) resetPreview() {
	s.previewErr, s.previewMissing, s.previewErrOpen = "", false, false
}

func previewUnavailable(s *session) bool {
	return s.kind == vault.KindTypst && s.previewMissing
}
