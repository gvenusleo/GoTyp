package gui

import (
	"path"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gotyp/internal/vault"
)

// renameState 是重命名对话框的状态。
type renameState struct {
	open bool
	id   string
	name string
}

// newNote 在当前笔记所在目录新建笔记，供快捷键和菜单栏使用。
func (a *App) newNote(kind vault.Kind) {
	dir := ""
	if a.current != nil {
		dir = parentDir(a.current.id)
	}
	a.createNoteIn(dir, kind)
}

// createNoteIn 直接创建「未命名」笔记并打开，不经过保存对话框。
func (a *App) createNoteIn(dir string, kind vault.Kind) {
	if a.vault == nil || a.busy {
		return
	}
	v := a.vault
	a.switchAfterSave(func() {
		a.generation++
		generation := a.generation
		a.busy = true
		go func() {
			id, st, err := v.CreateUntitled(dir, kind)
			a.publish(func() {
				if a.vault != v || a.generation != generation {
					return
				}
				a.busy = false
				if err != nil {
					a.notify("新建失败：" + err.Error())
					return
				}
				a.addToTree(id, false)
				a.showSession(newSession(id, "", st))
				a.rescan()
			})
		}()
	})
}

func (a *App) createFolder(dir string) {
	if a.vault == nil || a.busy {
		return
	}
	v := a.vault
	go func() {
		id, err := v.CreateFolder(dir)
		a.publish(func() {
			if a.vault != v {
				return
			}
			if err != nil {
				a.notify("新建文件夹失败：" + err.Error())
				return
			}
			a.addToTree(id, true)
			a.revealInOutline(id)
			a.startRename(id)
			a.rescan()
		})
	}()
}

// addToTree 先把新条目放进文件树，让它立即可见，随后的扫描会给出完整排序。
func (a *App) addToTree(id string, dir bool) {
	t := a.tree
	if t == nil {
		return
	}
	parent := parentDir(id)
	if _, ok := t.Kids[parent]; !ok {
		return
	}
	t.Kids[parent] = append(t.Kids[parent], id)
	if dir {
		t.Dirs[id] = true
		t.Kids[id] = []string{}
	} else {
		t.Files[id] = true
		t.Titles[id] = vault.Title(id, 0, "")
	}
	t.Roots = t.Kids[""]
}

func (a *App) startRename(id string) {
	if a.vault == nil || a.busy {
		return
	}
	a.rename = renameState{open: true, id: id, name: path.Base(id)}
}

// commitRename 先保存受影响的当前笔记，再改名并让会话跟随新路径。
func (a *App) commitRename() {
	r := a.rename
	a.rename.open = false
	name := strings.TrimSpace(r.name)
	if a.vault == nil || name == "" || name == path.Base(r.id) {
		return
	}
	if ext := path.Ext(r.id); a.tree != nil && !a.tree.Dirs[r.id] && path.Ext(name) == "" {
		name += ext
	}
	v := a.vault
	a.switchAfterSave(func() {
		a.busy = true
		go func() {
			id, err := v.Rename(r.id, name)
			a.publish(func() {
				if a.vault != v {
					return
				}
				a.busy = false
				if err != nil {
					a.notify("重命名失败：" + err.Error())
					return
				}
				a.renamed(r.id, id)
				a.rescan()
			})
		}()
	})
}

// renamed 让当前会话和侧栏展开状态跟随改名。改名不改变文件身份，已记录的文件状态仍然有效。
func (a *App) renamed(from, to string) {
	if a.outline.Open.Has(from) {
		a.outline.Open.Remove(from)
		a.outline.Open.Add(to)
	}
	// 查看器没有要保留的状态，改名后按新路径重新打开；新扩展名可能换一种打开方式。
	if vw := a.viewing; vw != nil && affects(from, vw.id) {
		a.loadSession(to + strings.TrimPrefix(vw.id, from))
		return
	}
	s := a.current
	if s == nil || !affects(from, s.id) {
		return
	}
	if next := to + strings.TrimPrefix(s.id, from); isImage(next) {
		// 改成了图片扩展名：内容已在改名前保存，直接按图片重新打开。
		a.loadSession(next)
		return
	}
	a.stopPreview(s)
	a.detachLSP(s)
	s.id = to + strings.TrimPrefix(s.id, from)
	s.updateLanguage()
	a.attachLSP(s)
	s.previewList = ui.ListState{}
	s.previewPages = nil
	s.previewErr, s.previewDiag = "", ""
	s.previewMissing = false
	s.previewErrOpen = false
	s.previewDur = 0
	s.renderAttempted = false
	s.watchFails = 0
	a.configurePreview(s)
	a.previewChanged(s)
	a.chosen.Clear()
	a.chosen.Add(s.id)
	a.revealInOutline(s.id)
	a.syncWindow()
	a.persistWorkspace()
	a.refreshMenu()
}

// trashItem 把条目移到系统废纸篓。涉及当前笔记时先保存，让废纸篓里是最新内容。
func (a *App) trashItem(id string) {
	if a.vault == nil || a.busy {
		return
	}
	v := a.vault
	name, err := v.Resolve(id)
	if err != nil {
		a.notify("无法解析路径：" + err.Error())
		return
	}
	run := func() {
		a.busy = true
		go func() {
			err := mygo.Shell.TrashItem(name)
			a.publish(func() {
				if a.vault != v {
					return
				}
				a.busy = false
				if err != nil {
					a.notify("移到废纸篓失败：" + err.Error())
					return
				}
				if open := a.openID(); open != "" && affects(id, open) {
					a.closeSession()
				}
				a.rescan()
			})
		}()
	}
	if a.current != nil && affects(id, a.current.id) {
		a.switchAfterSave(run)
		return
	}
	run()
}
