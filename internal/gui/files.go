package gui

import (
	"path"
	"slices"
	"strings"

	"github.com/egoist/mygo"

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
	a.createNoteIn(parentDir(a.openID()), kind)
}

// createNoteIn 直接创建「未命名」笔记并打开，不经过保存对话框。
func (a *App) createNoteIn(dir string, kind vault.Kind) {
	if a.vault == nil || a.busy {
		return
	}
	v := a.vault
	reserved := make([]string, 0, len(a.tabs))
	for _, tab := range a.tabs {
		reserved = append(reserved, tab.id)
	}
	a.busy = true
	go func() {
		id, st, err := v.CreateUntitled(dir, kind, reserved...)
		a.publish(func() {
			if a.vault != v {
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

// commitRename 先保存受影响的笔记，再让标签跟随新路径。
func (a *App) commitRename() {
	r := a.rename
	a.rename.open = false
	name := strings.TrimSpace(r.name)
	if a.vault == nil || a.busy || name == "" || name == path.Base(r.id) {
		return
	}
	if ext := path.Ext(r.id); a.tree != nil && !a.tree.Dirs[r.id] && path.Ext(name) == "" {
		name += ext
	}
	v := a.vault
	a.relocateItem(r.id, path.Join(parentDir(r.id), name), "重命名", func() (string, error) {
		return v.Rename(r.id, name)
	})
}

func (a *App) canMoveItem(id, dir string) bool {
	tree := a.tree
	return !a.busy && a.vault != nil && tree != nil && (tree.Dirs[id] || tree.Files[id]) &&
		(dir == "" || tree.Dirs[dir]) && dir != parentDir(id) && !affects(id, dir)
}

func (a *App) moveItem(id, dir string) {
	if !a.canMoveItem(id, dir) {
		return
	}
	v := a.vault
	a.relocateItem(id, path.Join(dir, path.Base(id)), "移动", func() (string, error) {
		return v.Move(id, dir)
	})
}

// relocateItem 先保存受影响的会话，再让文件树与标签跟随磁盘上的路径变更。
func (a *App) relocateItem(from, target, action string, change func() (string, error)) {
	for _, tab := range a.tabs {
		if !affects(from, tab.id) && affects(target, tab.id) {
			a.notify(action + "失败：目标路径已有打开的标签")
			return
		}
	}
	v := a.vault
	a.afterSaving(a.sessionsUnder(from), false, func() {
		a.busy = true
		go func() {
			id, err := change()
			a.publish(func() {
				if a.vault != v {
					return
				}
				a.busy = false
				if err != nil {
					a.notify(action + "失败：" + err.Error())
					return
				}
				a.itemRelocated(from, id)
				a.rescan()
			})
		}()
	})
}

// itemRelocated 保留标签顺序，并按新路径选择打开方式。
func (a *App) itemRelocated(from, to string) {
	for _, dir := range slices.Collect(a.outline.Open.All()) {
		if affects(from, dir) {
			a.outline.Open.Remove(dir)
			a.outline.Open.Add(to + strings.TrimPrefix(dir, from))
		}
	}
	if parentDir(from) != parentDir(to) {
		a.revealInOutline(to)
	}
	for _, tab := range a.tabs {
		if !affects(from, tab.id) {
			continue
		}
		tab.id = to + strings.TrimPrefix(tab.id, from)
		// 在途的监听结果仍指向旧路径，用新路径重新核对已加载的文件。
		tab.changed, tab.checking = tab.loaded(), false
		if tab == a.activeTab {
			a.stopPreview(a.current)
			a.detachLSP(a.current)
		}
		if s := tab.session; s != nil && !isImage(tab.id) {
			s.id = tab.id
			s.updateLanguage()
			if s.kind == vault.KindMarkdown {
				a.configurePreview(s)
			}
			s.resetPreview()
		} else {
			tab.session, tab.viewer = nil, nil
		}
		if tab == a.activeTab {
			a.activateTab(tab)
			if tab.session == nil {
				a.loadSession(tab.id)
			}
		}
	}
	a.persistWorkspace()
}

// trashItem 把条目移到系统废纸篓前，保存所有受影响的编辑会话。
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
	a.afterSaving(a.sessionsUnder(id), false, func() {
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
				a.removeTabs(func(tab *fileTab) bool { return affects(id, tab.id) })
				a.rescan()
			})
		}()
	})
}
