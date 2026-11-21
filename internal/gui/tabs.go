package gui

import (
	"slices"

	"gotyp/internal/vault"
)

type fileTab struct {
	id       string
	session  *session
	viewer   *viewer
	changed  bool
	checking bool
}

func (tab *fileTab) loaded() bool {
	return tab.session != nil || tab.viewer != nil
}

func (a *App) openID() string {
	if a.activeTab != nil {
		return a.activeTab.id
	}
	return ""
}

func (a *App) findTab(id string) *fileTab {
	for _, tab := range a.tabs {
		if tab.id == id {
			return tab
		}
	}
	return nil
}

func (a *App) tabFor(id string) *fileTab {
	if tab := a.findTab(id); tab != nil {
		return tab
	}
	tab := &fileTab{id: id}
	a.tabs = append(a.tabs, tab)
	return tab
}

// restoreTabs 只打开活动文件，其余标签等到首次激活再读取。
func (a *App) restoreTabs(w vault.Workspace) {
	selected := w.Selected
	if w.Tabs == nil {
		if !a.tree.Files[selected] {
			selected = ""
			for _, id := range files(a.tree) {
				if _, ok := a.tree.Titles[id]; ok {
					selected = id
					break
				}
			}
		}
		if selected != "" {
			a.loadSession(selected)
		}
		return
	}
	for _, id := range w.Tabs {
		if a.tree.Files[id] {
			a.tabFor(id)
		}
	}
	if a.findTab(selected) == nil && len(a.tabs) > 0 {
		selected = a.tabs[0].id
	}
	if tab := a.findTab(selected); tab != nil {
		a.loadSession(tab.id)
	} else {
		a.persistWorkspace()
	}
}

func (a *App) openNote(id string) {
	if a.busy || a.vault == nil || a.openID() == id {
		return
	}
	if tab := a.findTab(id); tab != nil && tab.loaded() {
		a.activateTab(tab)
		return
	}
	a.loadSession(id)
}

func (a *App) showSession(s *session) {
	tab := a.tabFor(s.id)
	tab.session, tab.viewer, tab.checking = s, nil, false
	a.activateTab(tab)
}

func (a *App) showViewer(vw *viewer) {
	tab := a.tabFor(vw.id)
	tab.session, tab.viewer, tab.checking = nil, vw, false
	a.pendingJump = nil
	a.activateTab(tab)
}

func (a *App) activateTab(tab *fileTab) {
	var next *session
	var viewing *viewer
	if tab != nil {
		next, viewing = tab.session, tab.viewer
	}
	if a.current != next {
		a.stopPreview(a.current)
		a.detachLSP(a.current)
	}
	previous := a.activeTab
	if previous != nil && previous != tab && previous.viewer != nil {
		// 只读文件下次激活时重读，及时释放可能很大的图片。
		previous.viewer, previous.checking = nil, false
	}
	a.activeTab, a.current, a.viewing = tab, next, viewing
	a.tabIndex = max(0, slices.Index(a.tabs, tab))
	a.revealTab = tab != nil && previous != tab
	if next != nil {
		a.applyEditorPrefs(next)
		if next.md.OnLink == nil && next.kind == vault.KindMarkdown {
			a.configurePreview(next)
		}
		if next.lspPath == "" {
			a.attachLSP(next)
		}
		a.applyPendingJump(next)
	}
	if tab == nil || viewing != nil {
		a.focus = false
	}
	if tab != nil {
		a.revealInOutline(tab.id)
	}
	a.syncSelection()
	a.syncWindow()
	a.persistWorkspace()
	if tab != nil && tab.changed {
		a.checkTabs()
	}
	a.refreshMenu()
}

func (a *App) stepTab(delta int) {
	if a.busy || len(a.tabs) == 0 {
		return
	}
	n := len(a.tabs)
	i := slices.Index(a.tabs, a.activeTab)
	if i < 0 {
		i = 0
		if delta > 0 {
			i = n - 1
		}
	}
	a.openNote(a.tabs[(i+delta+n)%n].id)
}

func (a *App) closeActiveTab() {
	a.closeTab(a.activeTab)
}

func (a *App) closeTab(tab *fileTab) {
	if tab == nil || a.findTab(tab.id) != tab {
		return
	}
	a.closeTabs(func(other *fileTab) bool { return tab == other })
}

func (a *App) closeTabs(match func(*fileTab) bool) {
	if a.busy {
		return
	}
	a.afterSaving(a.sessionsMatching(match), true, func() { a.removeTabs(match) })
}

// removeTabs 释放已关闭的会话。当前标签关闭时优先选择它右侧的第一个剩余标签。
func (a *App) removeTabs(match func(*fileTab) bool) {
	active := a.activeTab
	next := 0
	for _, tab := range a.tabs {
		if tab == active {
			break
		}
		if !match(tab) {
			next++
		}
	}
	a.tabs = slices.DeleteFunc(a.tabs, match)
	if active == nil || !match(active) {
		a.tabIndex = max(0, slices.Index(a.tabs, active))
		a.persistWorkspace()
		a.refreshMenu()
		return
	}
	if len(a.tabs) == 0 {
		a.activateTab(nil)
		return
	}
	neighbor := a.tabs[min(next, len(a.tabs)-1)]
	if neighbor.loaded() {
		a.activateTab(neighbor)
	} else {
		a.activateTab(nil)
		a.loadSession(neighbor.id)
	}
}

func (a *App) closeSession() {
	active := a.activeTab
	if active != nil {
		a.removeTabs(func(tab *fileTab) bool { return tab == active })
	}
}

// sessionsUnder 返回指定路径下需要等待保存的编辑会话。空路径代表整个仓库。
func (a *App) sessionsUnder(parent string) []*session {
	return a.sessionsMatching(func(tab *fileTab) bool { return parent == "" || affects(parent, tab.id) })
}

func (a *App) sessionsMatching(match func(*fileTab) bool) []*session {
	var sessions []*session
	for _, tab := range a.tabs {
		if s := tab.session; s != nil && (s.dirty() || s.saving) && match(tab) {
			sessions = append(sessions, s)
		}
	}
	return sessions
}
