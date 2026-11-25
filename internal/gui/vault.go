package gui

import (
	"errors"
	"io/fs"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gotyp/internal/vault"
)

func (a *App) openVaultDialog() {
	if a.openingVault || a.busy {
		return
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: a.win, Title: "打开文件夹", Directory: true,
		CreateDirectories: true,
	})
	if err != nil || len(paths) == 0 {
		return
	}
	a.openVault(paths[0])
}

func (a *App) openVault(root string) {
	if a.openingVault || a.busy || (a.vault != nil && a.vault.Root() == root) {
		return
	}
	a.afterSaving(a.sessionsUnder(""), false, func() { a.startOpenVault(root) })
}

func (a *App) startOpenVault(root string) {
	a.generation++
	generation := a.generation
	a.openingVault = true
	a.busy = true
	a.err = ""
	go func() {
		v, err := vault.Open(root)
		var tree *vault.Tree
		if err == nil {
			tree, err = v.Scan()
		}
		a.publish(func() {
			if a.generation != generation {
				return
			}
			a.openingVault = false
			a.busy = false
			// 外接磁盘未挂载等情况只是暂时打不开，保留最近记录和界面状态。
			if err != nil {
				a.notify("无法打开 " + shortPath(root) + "：" + err.Error())
				return
			}
			a.useVault(v, tree)
		})
	}()
}

// useVault 切换到已扫描好的仓库，并恢复它上次的界面状态和笔记。
func (a *App) useVault(v *vault.Vault, tree *vault.Tree) {
	a.stopPreview(a.current)
	a.viewing.cancelPDF()
	if a.preview != nil && a.viewing != nil && isPDF(a.viewing.id) {
		a.clearPreview()
	}
	a.detachLSP(a.current)
	a.startLSP(v.Root())
	a.vault, a.tree, a.current, a.viewing = v, tree, nil, nil
	a.tabs, a.activeTab = nil, nil
	a.tabIndex, a.revealTab = 0, false
	a.tabScroll.X = 0
	a.focus = false
	a.selectedRow = -1
	a.outline = ui.OutlineState[string]{}
	a.chosen = ui.Selection[string]{}
	a.dragHover = folderDragHover{}
	if a.palette.moveID != "" {
		a.palette = paletteState{}
	}
	w := a.state.Workspaces[v.Root()]
	a.applyWorkspace(w)
	a.state.Touch(v.Root())
	a.saveState()
	a.watch(v)
	a.syncWindow()
	a.refreshMenu()
	a.restoreTabs(w)
}

func (a *App) applyWorkspace(w vault.Workspace) {
	if w.SidebarWidth > 0 {
		a.sidebarWidth = clampSidebar(w.SidebarWidth, 0)
	}
	if w.EditorPercent > 0 {
		a.editorPercent = w.EditorPercent
	}
	a.lineNumbers = !w.HideLineNumbers
	a.sidebarHidden = w.SidebarHidden
	a.mode = modeSplit
	for i, name := range modeNames {
		if w.Mode == name {
			a.mode = i
		}
	}
}

func (a *App) openSelected() {
	if a.tree == nil || a.selectedRow < 0 {
		return
	}
	if a.busy {
		a.syncSelection()
		return
	}
	id := a.outline.Item(a.selectedRow)
	if a.tree.Dirs[id] {
		return
	}
	a.openNote(id)
}

func (a *App) loadSession(id string) {
	if a.vault == nil {
		return
	}
	v := a.vault
	a.generation++
	generation := a.generation
	a.busy = true
	a.err = ""
	read := newPDFRead(id)
	a.readingPDF = read
	go func() {
		source, st, vw, err := readFile(v, id)
		a.publish(func() {
			if a.vault != v || a.generation != generation {
				return
			}
			a.busy = false
			a.readingPDF = nil
			if read.changed(id, vw) {
				a.loadSession(id)
				return
			}
			switch {
			case err != nil:
				// 跳转只对这次打开有效，失败后不能留到以后手动打开同一篇笔记时才生效。
				a.pendingJump = nil
				a.tabIndex = max(0, slices.Index(a.tabs, a.activeTab))
				a.syncSelection()
				a.err = "无法打开文件：" + err.Error()
				if a.activeTab != nil {
					a.notify(a.err)
				}
				if errors.Is(err, fs.ErrNotExist) {
					a.removeTabs(func(tab *fileTab) bool { return tab.id == id })
					if a.activeTab == nil && len(a.tabs) > 0 {
						a.openNote(a.tabs[0].id)
					}
				}
			case vw != nil:
				a.pendingJump = nil
				a.showViewer(vw)
			default:
				a.showSession(newSession(id, source, st))
			}
		})
	}()
}

func (a *App) persistWorkspace() {
	if a.vault == nil {
		return
	}
	w := vault.Workspace{
		Tabs:         make([]string, 0, len(a.tabs)),
		Selected:     a.openID(),
		Mode:         modeNames[a.mode],
		SidebarWidth: a.sidebarWidth, EditorPercent: a.editorPercent,
		HideLineNumbers: !a.lineNumbers, SidebarHidden: a.sidebarHidden,
	}
	for _, tab := range a.tabs {
		w.Tabs = append(w.Tabs, tab.id)
	}
	if w.Equal(a.state.Workspaces[a.vault.Root()]) {
		return
	}
	a.state.SetWorkspace(a.vault.Root(), w)
	a.saveState()
}

// saveState 在后台写入应用状态，写入期间的新变化合并为下一次写入。
func (a *App) saveState() {
	if a.statePath == "" {
		return
	}
	if a.stateSaving {
		a.statePending = true
		return
	}
	a.stateSaving = true
	name, st := a.statePath, a.state.Clone()
	go func() {
		err := vault.SaveState(name, st)
		a.publish(func() {
			a.stateSaving = false
			if err != nil {
				a.notify("界面状态保存失败：" + err.Error())
			}
			if a.statePending {
				a.statePending = false
				a.saveState()
			} else if done := a.stateFlushed; done != nil {
				a.stateFlushed = nil
				done()
			}
		})
	}()
}

// currentPath 解析当前文件的绝对路径。
func (a *App) currentPath() (string, bool) {
	id := a.openID()
	if id == "" || a.vault == nil {
		return "", false
	}
	name, err := a.vault.Resolve(id)
	if err != nil {
		a.notify("无法解析笔记路径：" + err.Error())
		return "", false
	}
	return name, true
}

func (a *App) copyCurrentPath() {
	if id := a.openID(); id != "" {
		a.copyPath(id)
	}
}

func (a *App) copyPath(id string) {
	if a.vault == nil {
		return
	}
	name, err := a.vault.Resolve(id)
	if err != nil {
		a.notify("无法解析路径：" + err.Error())
		return
	}
	a.services.WriteClipboard(name)
	a.notify("已复制路径")
}

func (a *App) revealCurrent() {
	if name, ok := a.currentPath(); ok {
		a.revealPath(name)
	}
}

func (a *App) revealPath(name string) {
	mygo.Shell.ShowItemInFolder(name)
}
