package gui

import (
	"context"
	"errors"
	"path"
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
	a.refreshTypstMirror(events)
	if vw := a.viewing; vw != nil {
		for _, e := range events {
			if e.Path == vw.id || e.OldPath == vw.id || e.Op == mygo.FileHint || affects(e.Path, vw.id) {
				a.reloadViewer()
				return
			}
		}
	}
	if s := a.current; s != nil {
		for _, e := range events {
			if e.Path == s.id || e.OldPath == s.id || e.Op == mygo.FileHint || affects(e.Path, s.id) {
				a.checkCurrent()
				return
			}
		}
	}
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

// checkCurrent 核对当前笔记的磁盘版本：没有未保存编辑就静默重新加载，否则进入冲突状态。
// 自己的保存会更新记录的文件状态，因此不会被误判为外部修改。
func (a *App) checkCurrent() {
	s, v := a.current, a.vault
	if s == nil || v == nil || s.saving || s.conflict {
		return
	}
	expected := s.stat
	go func() {
		err := v.Check(s.id, expected)
		a.publish(func() {
			if a.current != s || a.vault != v || s.saving || s.stat != expected {
				return
			}
			deleted := errors.Is(err, vault.ErrDeleted)
			switch {
			case err == nil:
			case !s.dirty() && deleted:
				a.closeSession()
			case !s.dirty():
				a.reloadCurrent()
			default:
				s.conflict, s.deleted = true, deleted
				s.err = err.Error()
			}
		})
	}()
}

// refreshTypstMirror 在主文件所在路径的目录中出现新增、删除或改名时重启常驻编译进程，让镜像看到新的文件。
// 镜像中只有这一路是真实目录，其他目录和文件内容的变化经符号链接由 typst watch 自行发现，不需要重启。
func (a *App) refreshTypstMirror(events []mygo.FileEvent) {
	s := a.current
	if s == nil || s.watch == nil {
		return
	}
	for _, e := range events {
		var dirs []string
		switch e.Op {
		case mygo.FileWrite:
			continue
		case mygo.FileHint:
			// 提示只说明这个目录之下有变化。
			dirs = []string{strings.TrimPrefix(e.Path, ".")}
		default:
			dirs = []string{parentDir(e.Path)}
			if e.OldPath != "" {
				dirs = append(dirs, parentDir(e.OldPath))
			}
		}
		for _, dir := range dirs {
			if onMirrorPath(dir, s.id) {
				a.stopWatch(s)
				return
			}
		}
	}
}

// onMirrorPath 报告目录 dir 是否在从仓库根到 id 所在目录的路上，镜像中只有这些目录是真实目录。
func onMirrorPath(dir, id string) bool {
	parent := parentDir(id)
	return dir == "" || dir == parent || strings.HasPrefix(parent, dir+"/")
}
