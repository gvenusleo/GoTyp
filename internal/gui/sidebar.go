package gui

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
	if a.current == nil {
		return
	}
	a.chosen.Add(a.current.id)
	for row := range a.outline.Rows() {
		if a.outline.Item(row) == a.current.id {
			a.selectedRow = row
			return
		}
	}
}
