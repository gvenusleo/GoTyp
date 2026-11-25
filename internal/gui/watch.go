package gui

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"gotyp/internal/vault"
)

// watch 递归监听仓库，变化经防抖后回到主线程重新扫描。监听失败时退回为不自动刷新。
func (a *App) watch(v *vault.Vault) {
	if a.watchCancel != nil {
		a.watchCancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.watchCancel = cancel
	go func() {
		w, err := mygo.WatchFiles(ctx, v.Root(), mygo.FileWatchOptions{
			Recursive: true,
			Debounce:  150 * time.Millisecond,
			// 忽略隐藏条目，其中包括保存时在同一目录写入的 .gotyp-save-* 临时文件。
			Ignore: func(name string, _ bool) bool { return strings.HasPrefix(path.Base(name), ".") },
		})
		if err != nil {
			a.publish(func() { a.watchStopped(v, err) })
			return
		}
		defer w.Close()
		for {
			events, err := w.Next(ctx)
			if err != nil {
				a.publish(func() { a.watchStopped(v, err) })
				return
			}
			a.publish(func() { a.filesChanged(v, events) })
		}
	}()
}

// watchStopped 提示监听意外停止，例如仓库条目过多或目录被移走；切换仓库引起的取消不提示。
func (a *App) watchStopped(v *vault.Vault, err error) {
	if a.vault != v || errors.Is(err, context.Canceled) {
		return
	}
	a.notify("已停止监听仓库，文件变化不再自动刷新")
}

func (a *App) filesChanged(v *vault.Vault, events []mygo.FileEvent) {
	if a.vault != v {
		return
	}
	// 只有内容写入时不改变文件树，只更新受影响笔记的标题；自己的保存也会产生写入事件。
	var written []string
	for _, e := range events {
		if e.Op != mygo.FileWrite {
			a.rescan()
			written = nil
			break
		}
		written = append(written, e.Path)
	}
	a.refreshTitles(written)
	a.readingPDF.record(events)
	for _, tab := range a.tabs {
		tab.pdfRead.record(events)
		if fileEventsAffect(events, tab.id, tab.viewer) {
			tab.changed = true
			tab.viewer.cancelPDF()
		}
	}
	if s := a.current; s != nil && s.lspPath != "" {
		for _, e := range events {
			if e.Path != s.id {
				s.previewFiles = append(s.previewFiles, filepath.Join(v.Root(), filepath.FromSlash(e.Path)))
				if e.OldPath != "" {
					s.previewFiles = append(s.previewFiles, filepath.Join(v.Root(), filepath.FromSlash(e.OldPath)))
				}
			}
		}
	}
	a.checkTabs()
}

// PDF 读取结束后才能确定实际目标，需要保留读取期间的变更路径。
type pdfRead struct {
	paths map[string]struct{}
	hint  bool
}

func newPDFRead(id string) *pdfRead {
	if isPDF(id) {
		return &pdfRead{paths: make(map[string]struct{})}
	}
	return nil
}

func (read *pdfRead) record(events []mygo.FileEvent) {
	if read == nil || read.hint {
		return
	}
	for _, event := range events {
		if event.Op == mygo.FileHint {
			read.hint, read.paths = true, nil
			return
		}
		read.paths[event.Path] = struct{}{}
		if event.OldPath != "" {
			read.paths[event.OldPath] = struct{}{}
		}
	}
}

func (read *pdfRead) changed(id string, vw *viewer) bool {
	if read == nil {
		return false
	}
	if read.hint {
		return true
	}
	for changed := range read.paths {
		if pathAffectsViewer(changed, id, vw) {
			return true
		}
	}
	return false
}

func fileEventsAffect(events []mygo.FileEvent, id string, vw *viewer) bool {
	return slices.ContainsFunc(events, func(e mygo.FileEvent) bool {
		return e.Op == mygo.FileHint || pathAffectsViewer(e.Path, id, vw) || pathAffectsViewer(e.OldPath, id, vw)
	})
}

func pathAffectsViewer(changed, id string, vw *viewer) bool {
	return affects(changed, id) || vw != nil && vw.pdfTarget != "" && affects(changed, vw.pdfTarget)
}

// refreshTitles 在后台重新读取被写入笔记的标题。
func (a *App) refreshTitles(ids []string) {
	if a.tree == nil {
		return
	}
	ids = slices.DeleteFunc(ids, func(id string) bool { _, ok := a.tree.Titles[id]; return !ok })
	if len(ids) == 0 {
		return
	}
	v := a.vault
	go func() {
		titles := make(map[string]string, len(ids))
		for _, id := range ids {
			titles[id] = v.ReadTitle(id)
		}
		a.publish(func() {
			if a.vault != v || a.tree == nil {
				return
			}
			for id, title := range titles {
				if _, ok := a.tree.Titles[id]; ok {
					a.tree.Titles[id] = title
				}
			}
			a.palette.forTree = nil
		})
	}()
}

// rescan 在后台重新扫描仓库，同一时间只有一次扫描，期间的新请求合并为下一次。
func (a *App) rescan() {
	if a.vault == nil {
		return
	}
	if a.scanRunning {
		a.scanAgain = true
		return
	}
	v := a.vault
	a.scanRunning = true
	go func() {
		tree, err := v.Scan()
		a.publish(func() {
			a.scanRunning = false
			if a.vault != v {
				return
			}
			if err == nil {
				a.tree = tree
			}
			if a.scanAgain {
				a.scanAgain = false
				a.rescan()
			}
		})
	}()
}

// checkTabs 及时核对有未保存编辑的后台标签。干净的后台标签等激活后再读。
func (a *App) checkTabs() {
	if a.vault == nil || a.busy {
		return
	}
	for _, tab := range a.tabs {
		s := tab.session
		if !tab.loaded() || !tab.changed || tab.checking ||
			(s != nil && (s.saving || s.conflict)) || (tab != a.activeTab && !s.dirty()) {
			continue
		}
		a.checkTab(tab)
	}
}

func (a *App) checkTab(tab *fileTab) {
	v, id, s, vw := a.vault, tab.id, tab.session, tab.viewer
	var expected vault.FileStat
	var version uint64
	clean := !s.dirty()
	if s != nil {
		expected, version = s.stat, s.ed.Buffer.Version()
	}
	tab.changed, tab.checking = false, true
	read := newPDFRead(id)
	tab.pdfRead = read
	go func() {
		var err error
		if s != nil {
			err = v.Check(id, expected)
		}
		changed := s == nil || err != nil
		var source string
		var stat vault.FileStat
		var next *viewer
		if changed && clean && !fileDeleted(err) {
			source, stat, next, err = readFile(v, id)
		}
		a.publish(func() {
			if v != a.vault || a.findTab(id) != tab || tab.session != s || tab.viewer != vw || tab.pdfRead != read {
				return
			}
			tab.checking, tab.pdfRead = false, nil
			if a.busy || tab.changed || read.changed(id, next) || (s != nil && (s.saving || s.stat != expected)) {
				tab.changed = true
				return
			}
			if changed {
				a.applyFileChange(tab, clean, version, source, stat, next, err)
			}
		})
	}()
}

func fileDeleted(err error) bool {
	return errors.Is(err, vault.ErrDeleted) || errors.Is(err, fs.ErrNotExist)
}

func (a *App) applyFileChange(tab *fileTab, read bool, version uint64, source string, stat vault.FileStat, vw *viewer, err error) {
	s := tab.session
	switch {
	case s.dirty():
		s.conflict, s.deleted = true, fileDeleted(err)
		s.err = vault.ErrConflict.Error()
		if s.deleted {
			s.err = vault.ErrDeleted.Error()
		}
	case s != nil && (!read || s.ed.Buffer.Version() != version):
		tab.changed = true
	case fileDeleted(err):
		a.removeTabs(func(other *fileTab) bool { return other == tab })
	case err != nil && tab.viewer != nil && isPDF(tab.id):
		tab.viewer.cancelPDF()
		tab.viewer.note = "无法重新读取 PDF：" + err.Error()
	case err != nil:
		a.notify("无法重新读取「" + path.Base(tab.id) + "」：" + err.Error())
	case s != nil && vw == nil:
		a.replaceSource(s, source, stat)
	default:
		tab.viewer = vw
		tab.session = nil
		if vw == nil {
			tab.session = newSession(tab.id, source, stat)
		}
		if tab == a.activeTab {
			a.activateTab(tab)
		}
	}
}
