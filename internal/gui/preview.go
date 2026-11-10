package gui

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"gotyp/internal/typst"
	"gotyp/internal/vault"
)

const (
	previewDelay = 400 * time.Millisecond
	// exportTimeout 是导出 PDF 的上限，文档中有死循环时不会一直挂着。
	exportTimeout = 2 * time.Minute
)

// configurePreview 为每篇笔记建立独立图片缓存，避免不同目录的同名图片串用。
func (a *App) configurePreview(s *session) {
	s.md = markdown.Options{}
	if a.vault != nil {
		if name, err := a.vault.Resolve(s.id); err == nil {
			s.md.LoadImage = markdown.FileImages(filepath.Dir(name))
		}
	}
	s.md.OnLink = func(dest string) { a.openMarkdownLink(s, dest) }
}

func (a *App) previewChanged(s *session) {
	if a.current != s || s.kind != vault.KindTypst {
		return
	}
	s.renderGeneration++
	s.previewPending = true
	s.previewEdit = time.Time{}
	if s.renderCancel != nil {
		s.renderCancel()
	}
}

// stopPreview 在笔记不再显示时结束它的一次性编译和常驻进程。
func (a *App) stopPreview(s *session) {
	if s == nil {
		return
	}
	a.stopWatch(s)
	s.renderGeneration++
	s.previewPending = false
	s.previewEdit = time.Time{}
	if s.renderCancel != nil {
		s.renderCancel()
		s.renderCancel = nil
	}
	s.activeRender = 0
}

// restartPreview 换清晰度后按新分辨率重新编译。常驻进程的分辨率在启动时固定，只能重启；
// 新页面出来前继续显示旧页面。
func (a *App) restartPreview(s *session) {
	if s.kind != vault.KindTypst {
		return
	}
	a.stopPreview(s)
	s.renderAttempted = false
}

// schedulePreview 只在预览可见时安排编译，连续输入只保留最新快照。
// 默认交给常驻的 typst watch 进程，进程反复失败后退回一次性编译。
func (a *App) schedulePreview(c *ui.Context, s *session) {
	if a.current != s || s.kind != vault.KindTypst || a.mode == modeEdit || a.vault == nil {
		return
	}
	a.recolorPreview(s)
	oneShot := s.watchFails >= maxWatchFails
	if !s.previewPending && previewStale(s, oneShot) {
		s.previewPending = true
		s.previewEdit = c.Now()
	}
	if !s.previewPending {
		return
	}
	if oneShot && s.activeRender != 0 {
		c.After(100 * time.Millisecond)
		return
	}
	if s.previewEdit.IsZero() {
		s.previewEdit = c.Now()
	}
	delay := watchDelay
	if oneShot {
		delay = previewDelay
	}
	if due := s.previewEdit.Add(delay); c.Now().Before(due) {
		c.After(due.Sub(c.Now()))
		return
	}
	if oneShot {
		a.startPreview(s)
	} else {
		a.sendWatch(s, c.Now())
	}
}

// previewStale 报告预览是否落后于当前编辑。
func previewStale(s *session, oneShot bool) bool {
	if oneShot {
		return !s.renderAttempted || s.attemptedVersion != s.observed
	}
	return s.watch == nil || s.sentVersion != s.observed
}

type previewResult struct {
	id      uint64
	version uint64
	pages   []*ui.Bitmap
	data    [][]byte // 每页 PNG 原始字节，切换主题时据此重新调色
	tone    pageTone // pages 解码时用的配色
	dur     time.Duration
	diag    string
	err     error
}

func (a *App) startPreview(s *session) {
	if a.vault == nil {
		return
	}
	if s.renderGeneration == 0 {
		s.renderGeneration = 1
	}
	id := s.renderGeneration
	version := s.observed
	req := typst.Request{Root: a.vault.Root(), LogicalPath: s.id, Source: s.text(), PPI: a.state.Appearance.PreviewPPI}
	tone := a.pageTone()
	ctx, cancel := context.WithCancel(a.ctx)
	s.activeRender = id
	s.renderCancel = cancel
	s.previewPending = false
	s.previewEdit = time.Time{}
	go func() {
		start := time.Now()
		result, err := typst.Preview(ctx, req)
		dur := time.Since(start)
		var pages []*ui.Bitmap
		if err == nil {
			pages, err = decodePages(result.Files, tone)
		}
		a.publish(func() {
			a.finishPreview(s, previewResult{
				id: id, version: version, pages: pages, data: result.Files, tone: tone,
				dur: dur, diag: result.Diagnostics, err: err,
			})
		})
	}()
}

func (a *App) finishPreview(s *session, result previewResult) {
	if s.activeRender != result.id {
		return
	}
	s.activeRender = 0
	s.renderCancel = nil
	if a.current != s || s.renderGeneration != result.id || result.version != s.ed.Buffer.Version() {
		return
	}
	s.renderAttempted = true
	s.attemptedVersion = result.version
	s.previewDur = result.dur
	s.previewMissing = errors.Is(result.err, typst.ErrUnavailable)
	if result.err != nil {
		s.previewErr = result.err.Error()
		return
	}
	s.setPreviewPages(result)
	s.previewErr = ""
	s.previewDiag = result.diag
}

func (a *App) exportPDFDialog() {
	s := a.current
	if s == nil || s.kind != vault.KindTypst {
		return
	}
	if s.exporting {
		a.notify("正在导出 PDF")
		return
	}
	name, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent: a.win, Title: "导出 PDF",
		DefaultPath:       filepath.Join(a.vault.Root(), filepath.FromSlash(strings.TrimSuffix(s.id, path.Ext(s.id))+".pdf")),
		Filters:           []mygo.FileFilter{{Name: "PDF", Extensions: []string{"pdf"}}},
		CreateDirectories: true,
	})
	if err != nil || name == "" {
		return
	}
	req := typst.Request{Root: a.vault.Root(), LogicalPath: s.id, Source: s.text()}
	s.exporting = true
	// 导出是独立的一次性编译，不暂停预览。
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, exportTimeout)
		defer cancel()
		result, err := typst.PDF(ctx, req)
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("编译超时")
		}
		if err == nil {
			err = vault.WriteCopy(name, bytes.NewReader(result.Files[0]))
		}
		a.publish(func() {
			s.exporting = false
			if err != nil {
				a.notify("PDF 导出失败：" + err.Error())
			} else {
				a.notify("PDF 已导出")
			}
		})
	}()
}

func (a *App) openMarkdownLink(s *session, dest string) {
	if a.current != s {
		return
	}
	if strings.HasPrefix(dest, "#") {
		s.md.Anchor = dest
		s.md.AnchorVersion++
		return
	}
	u, err := url.Parse(dest)
	if err != nil {
		a.notify("无法打开链接")
		return
	}
	if u.Scheme != "" {
		if ui.SafeToOpen(dest) {
			a.services.OpenURL(dest)
		} else {
			a.notify("不支持此链接")
		}
		return
	}
	if u.Path == "" || strings.HasPrefix(u.Path, "/") {
		a.notify("无法打开笔记链接")
		return
	}
	id := path.Clean(path.Join(path.Dir(s.id), u.Path))
	if id == "." || strings.HasPrefix(id, "../") {
		a.notify("链接超出笔记仓库")
		return
	}
	if _, ok := vault.KindFor(id); !ok {
		a.openVaultFile(id)
		return
	}
	if a.tree == nil {
		return
	}
	if _, ok := a.tree.Titles[id]; !ok {
		a.notify("链接的笔记不存在")
		return
	}
	a.openNote(id)
}

// openVaultFile 用系统默认应用打开仓库内的文件，例如链接的 PDF 或应用内无法显示的文件。路径解析在后台进行。
func (a *App) openVaultFile(id string) {
	v := a.vault
	if v == nil {
		return
	}
	go func() {
		name, err := v.Resolve(id)
		if err == nil {
			var info os.FileInfo
			if info, err = os.Stat(name); err == nil && !info.Mode().IsRegular() {
				err = errors.New("不是文件")
			}
		}
		a.publish(func() {
			if a.vault != v {
				return
			}
			if err != nil {
				a.notify("无法打开文件")
				return
			}
			if err := mygo.Shell.OpenPath(name); err != nil {
				a.notify("无法打开文件：" + err.Error())
			}
		})
	}()
}

// previewUnavailable 报告预览失败是否因为缺少 Typst 编译器。
func previewUnavailable(s *session) bool {
	return s.previewMissing && s.previewErr != ""
}
