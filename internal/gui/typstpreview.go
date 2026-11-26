package gui

import (
	"context"
	"errors"

	"github.com/gvenusleo/mygo-pdf/pdf"

	"gotyp/internal/tinymist"
	"gotyp/internal/vault"
)

type previewRevision struct {
	version      uint64
	dependencies uint64
}

type previewResult struct {
	text         string
	dependencies uint64
	err          string
}

func (s *session) previewRevision() previewRevision {
	return previewRevision{s.ed.Buffer.Version(), s.previewDependencies}
}

// previewChanged 取消尚未安装的旧 PDF，已显示的画面继续保留。
func (a *App) previewChanged(s *session) {
	if a.current != s || s.lspPath == "" {
		return
	}
	if s.previewCancel != nil {
		s.previewCancel()
		s.previewCancel = nil
	}
	if !s.previewMissing {
		s.previewErr = ""
	}
}

func (a *App) schedulePreview(s *session) {
	if s == nil || a.current != s || s.lspPath == "" || a.lsp == nil || s.syncing {
		return
	}
	revision := s.previewRevision()
	if s.syncedVersion == nil || *s.syncedVersion != revision.version || s.syncedDependencies != revision.dependencies {
		a.syncLSP(s)
		return
	}
	if a.preview == nil || s.previewing || (s.previewRequested != nil && *s.previewRequested == revision) {
		return
	}
	s.previewRequested = new(revision)
	doc := s.lspDocument()
	if cached := s.previewResult; cached != nil && cached.dependencies == revision.dependencies && cached.text == doc.Text {
		s.previewErr, s.previewStarting = cached.err, false
		return
	}
	a.startPreview(s, doc, revision)
}

func (a *App) syncLSP(s *session) {
	client, ctx, doc := a.lsp, s.lspContext, s.lspDocument()
	revision := s.previewRevision()
	refresh := s.syncedDependencies != revision.dependencies
	s.syncedVersion, s.syncedDependencies, s.syncing = new(doc.Version), revision.dependencies, true
	s.previewStarting = !s.previewReady
	a.lspQueue.run(func() {
		var err error
		if refresh {
			err = client.Refresh(ctx, doc)
		} else {
			err = client.OpenContext(ctx, doc)
		}
		a.publish(func() {
			if a.current != s || a.lsp != client || s.lspID != doc.Session {
				return
			}
			s.syncing = false
			if err == nil || a.receiveSyncError(s, client, doc, revision, err) {
				a.schedulePreview(s)
			}
		})
	})
}

func (a *App) receiveSyncError(s *session, client *tinymist.Client, doc tinymist.Document, revision previewRevision, err error) bool {
	if s.previewRevision() != revision {
		s.syncedVersion = nil
		return true
	}
	s.previewRequested = new(revision)
	a.receivePDF(client, doc, revision, nil, err)
	return errors.Is(err, tinymist.ErrDisconnected)
}

func (a *App) startPreview(s *session, doc tinymist.Document, revision previewRevision) {
	client, ctx := a.lsp, s.lspContext
	s.previewing, s.previewStarting = true, !s.previewReady
	files := &a.previewFiles
	files.Go(func() {
		file, err := client.Preview(ctx, doc)
		a.publishPreview(ctx, file, func() {
			if a.current == s && a.lsp == client && s.lspID == doc.Session {
				s.previewing = false
			}
			if !a.receivePDF(client, doc, revision, file, err) && file != nil {
				files.Go(file.Close)
			}
			a.schedulePreview(s)
		})
	})
}

func (a *App) publishPreview(ctx context.Context, file *tinymist.PreviewFile, apply func()) {
	if file == nil {
		a.publish(apply)
		return
	}
	// 窗口关闭时，已排队的界面回调可能不再运行。
	files := &a.previewFiles
	files.Add(1)
	stop := context.AfterFunc(ctx, func() {
		defer files.Done()
		file.Close()
	})
	a.publish(func() {
		if stop() {
			defer files.Done()
			apply()
		}
	})
}

// receivePDF 返回查看器是否接管了文件。
func (a *App) receivePDF(client *tinymist.Client, doc tinymist.Document, revision previewRevision, file *tinymist.PreviewFile, err error) bool {
	s := a.current
	if a.ctx.Err() != nil || a.lsp != client || s == nil || s.lspPath != doc.Path ||
		s.lspID != doc.Session || s.previewRevision() != revision {
		return false
	}
	if err != nil {
		a.previewFailed(s, doc, revision, err)
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
	a.loadPreviewFile(ctx, file, func(state pdf.State) {
		a.finishPDF(s, client, doc, revision, ctx, state.Err)
	})
	return true
}

func (a *App) previewFailed(s *session, doc tinymist.Document, revision previewRevision, err error) {
	s.previewStarting = false
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, tinymist.ErrDisconnected):
		s.syncedVersion, s.previewRequested, s.previewErr = nil, nil, ""
		s.previewStarting = !s.previewReady
		return
	}
	if s.previewCancel != nil {
		s.previewCancel()
		s.previewCancel = nil
	}
	s.previewMissing = errors.Is(err, tinymist.ErrUnavailable)
	s.previewErr = "无法更新 Typst 预览：" + err.Error()
	if errors.Is(err, tinymist.ErrCompile) {
		s.previewResult = &previewResult{doc.Text, revision.dependencies, s.previewErr}
	}
	a.refreshMenu()
}

func (a *App) finishPDF(s *session, client *tinymist.Client, doc tinymist.Document, revision previewRevision, ctx context.Context, err error) {
	if a.ctx.Err() != nil || ctx.Err() != nil || a.current != s || a.lsp != client ||
		s.lspID != doc.Session || s.previewRevision() != revision {
		return
	}
	s.previewCancel()
	s.previewCancel = nil
	s.previewStarting = false
	if s.previewMissing {
		s.previewMissing = false
		a.refreshMenu()
	}
	if err != nil {
		s.previewErr = "无法显示 Typst 预览：" + err.Error()
		return
	}
	s.previewReady, s.previewErr = true, ""
	s.previewResult = &previewResult{text: doc.Text, dependencies: revision.dependencies}
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
	s.previewRequested, s.previewResult = nil, nil
	if a.preview != nil {
		a.clearPreview()
	}
}

func (s *session) resetPreview() {
	s.previewErr, s.previewMissing, s.previewErrOpen = "", false, false
}

func previewUnavailable(s *session) bool {
	return s.kind == vault.KindTypst && s.previewMissing
}
