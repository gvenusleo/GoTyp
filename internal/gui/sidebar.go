package gui

import "gotyp/internal/vault"

// treeDrag 只在发起拖动的仓库内有效。
type treeDrag struct {
	vault *vault.Vault
	id    string
}

// dragTreeRow 让 Outline 继续负责点选和展开，拖动时只携带一个条目。
func (a *App) dragTreeRow(rows []int) any {
	if a.busy || len(rows) != 1 || rows[0] >= a.outline.Rows() {
		return nil
	}
	return treeDrag{vault: a.vault, id: a.outline.Item(rows[0])}
}

// revealInOutline 展开 id 的各级上级目录，让它在文件树中可见。
func (a *App) revealInOutline(id string) {
	for dir := parentDir(id); dir != ""; dir = parentDir(dir) {
		a.outline.Open.Add(dir)
	}
}

// clampSidebar 把宽度限制在允许范围内，窗口很窄时优先保证内容区。
func clampSidebar(w, window float32) float32 {
	upper := float32(sidebarMax)
	if window > 0 {
		upper = max(sidebarMin, min(upper, window-minContentWidth))
	}
	return min(max(w, sidebarMin), upper)
}

// syncSelection 让文件树的选中行回到当前笔记，切换被拒绝或失败后侧栏不与内容区脱节。
func (a *App) syncSelection() {
	a.chosen.Clear()
	a.selectedRow = -1
	id := a.openID()
	if id == "" {
		return
	}
	a.chosen.Add(id)
	for row := range a.outline.Rows() {
		if a.outline.Item(row) == id {
			a.selectedRow = row
			return
		}
	}
}
