package gui

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/vault"
)

func (a *App) sidebarView(c *ui.Context) {
	ui.Themed(c, a.themes.sidebar).Fill().MinWidth(0).Children(func() {
		t := c.Theme()
		// 顶部留给红绿灯和浮在上方的侧栏开关（见 workspaceView），同时可以拖动窗口。
		ui.Row(c).Height(headerHeight).Shrink(0).DragWindow()
		ui.Column(c).Padding(0, t.Space(2), t.Space(1.5)).Gap(2).Children(func() {
			sidebarEntry(c, iconPlus, "新建", "", true).Menu(a.newMenu)
			sidebarEntry(c, iconSearch, "搜索", "⌘P", false).OnClick(a.openPalette)
		})
		a.treeView(c)
		// 仓库切换放在侧栏底部，与笔记操作分开。它比状态栏高，加分隔线会与状态栏的顶边错开，
		// 所以只靠留白隔开，左右边距与顶部入口相同。
		ui.Row(c).Shrink(0).Padding(t.Space(1), t.Space(2), t.Space(2)).
			Children(func() { a.vaultButton(c) })
	})
}

// vaultButton 显示仓库名和所在路径，点击后切换最近仓库或打开其他文件夹。
// 两行文字让同名仓库也能分辨，不必悬停查看路径。
func (a *App) vaultButton(c *ui.Context) {
	t := c.Theme()
	root := a.vault.Root()
	name := filepath.Base(root)
	b := ui.ButtonBase(c).Label("切换仓库："+name).Tooltip(shortPath(root)).
		Height(vaultHeight).Padding(0, t.Space(1.5)).Radius(t.Radius).MinWidth(0).Grow(1).Transition(hoverMotion)
	switch {
	case b.Pressed():
		b.Background(pressFill(t))
	case b.Hovered():
		b.Background(rowFill(t))
	}
	b.Children(func() {
		ui.Row(c).Gap(t.Space(2.5)).AlignItems(ui.Center).FillWidth().MinWidth(0).Children(func() {
			ui.Row(c).Size(vaultBadge, vaultBadge).Shrink(0).Radius(t.Radius).
				Background(t.Background).Border(1, t.Border).
				Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconFolder).FontSize(iconSize).TextColor(t.TextMuted)
			})
			ui.Column(c).Gap(1).Grow(1).MinWidth(0).Children(func() {
				// 仓库名是次要信息，不加粗、用次要文字色，不抢文件树的视线；路径再淡一档。
				ui.Text(c, name).FontSize(textSize).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0)
				ui.Text(c, shortPath(filepath.Dir(root))).FontSize(captionSize).TextColor(t.TextMuted.Alpha(0.7)).
					SingleLine().Ellipsis("…").MinWidth(0)
			})
			ui.Icon(c, iconChevronsUpDown).FontSize(iconSize).TextColor(t.TextMuted)
		})
	}).Menu(a.vaultMenu)
}

func (a *App) vaultMenu(m *ui.Menu) {
	for _, root := range a.state.Recent {
		if a.vault != nil && root == a.vault.Root() {
			continue
		}
		if m.Item(filepath.Base(root)).Subtitle(shortPath(root)).Chosen() {
			a.openVault(root)
		}
	}
	if len(a.state.Recent) > 1 {
		m.Separator()
	}
	if m.Item("打开文件夹…").Shortcut(ui.Cmd, ui.KeyO).Chosen() {
		a.openVaultDialog()
	}
	if m.Item("在文件管理器中显示").Chosen() {
		a.revealPath(a.vault.Root())
	}
}

// newMenu 在当前笔记所在目录新建；右键菜单的同名项则在被点击的目录中新建。
func (a *App) newMenu(m *ui.Menu) {
	dir := ""
	if a.current != nil {
		dir = parentDir(a.current.id)
	}
	a.newItems(m, dir)
}

func (a *App) newItems(m *ui.Menu, dir string) {
	if m.Item("Markdown 笔记").Shortcut(ui.Cmd, ui.KeyN).Chosen() {
		a.createNoteIn(dir, vault.KindMarkdown)
	}
	if m.Item("Typst 笔记").Shortcut(ui.Cmd|ui.Shift, ui.KeyN).Chosen() {
		a.createNoteIn(dir, vault.KindTypst)
	}
	if m.Item("文件夹").Chosen() {
		a.createFolder(dir)
	}
}

// sidebarEntry 是侧栏顶部的一个入口：图标加文字，行尾可带快捷键。primary 的图标放在圆形浅底里，
// 表明它是主要操作。图标槽与文件树的箭头左右对齐。
func sidebarEntry(c *ui.Context, svg *ui.SVG, label, keys string, primary bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(label)).Label(label).Padding(t.Space(1), t.Space(1.5)).Radius(t.Radius).
		FillWidth().Transition(hoverMotion)
	switch {
	case b.Pressed():
		b.Background(pressFill(t))
	case b.Hovered():
		b.Background(rowFill(t))
	}
	return b.Children(func() {
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).FillWidth().Children(func() {
			slot := ui.Row(c).Size(entrySlot, entrySlot).Shrink(0).Radius(entrySlot / 2).
				Justify(ui.Center).AlignItems(ui.Center)
			size, color := float32(iconButtonSize), t.TextMuted
			if primary {
				slot.Background(buttonFill(t))
				size, color = iconSize, t.Text
			}
			slot.Children(func() { ui.Icon(c, svg).FontSize(size).TextColor(color) })
			ui.Text(c, label).SingleLine().Grow(1)
			if keys != "" {
				ui.Text(c, keys).FontSize(detailFontSize).TextColor(t.TextMuted)
			}
		})
	})
}

func (a *App) treeView(c *ui.Context) {
	tree := a.tree
	if tree == nil || len(tree.Roots) == 0 {
		ui.Spacer(c)
		return
	}
	t := c.Theme()
	gap := t.Space(1.5)
	// 每深一层缩进一个图标加间距，子项的图标正好落在父目录名称的起点下方。
	indent := iconSize + gap
	// 第一层图标与顶部入口的图标中心对齐。
	inset := (entrySlot-iconSize)/2 + t.Space(1.5)
	// Prefix 替换 Outline 自带的缩进和展开箭头：文件夹图标已经表明它可以展开，不再画箭头。
	// 它只在本次构建中使用，构建完就清掉，不让应用状态留着捕获了 c 的闭包。
	var prefix ui.OutlineRow
	a.outline.Prefix = func(r ui.OutlineRow) {
		ui.Box(c).Width(inset + float32(r.Depth)*indent).Shrink(0)
		prefix = r
	}
	a.outline.List.Selected = &a.selectedRow
	a.outline.List.Selection = &a.chosen
	a.outline.List.Label = func(row int) string { return itemLabel(a.outline.Item(row)) }
	clicked := ""
	ui.Outline(c, &a.outline, tree.Roots, tree.Children, func(id string) {
		dir := tree.Dirs[id]
		// 目录的整行内容就是 Outline 的展开开关，点在行上任何位置都会展开或收起；
		// 文件行的点击交给列表，选中后打开。
		row := ui.Row(c)
		if dir {
			row = prefix.Arrow()
			if row.Clicked() {
				clicked = id
			}
		}
		row.Grow(1).MinWidth(0).Padding(t.Space(1.25), t.Space(2), t.Space(1.25), 0).Children(func() {
			ui.Row(c).Gap(gap).AlignItems(ui.Center).FillWidth().MinWidth(0).Children(func() {
				svg := fileIcon(id)
				switch {
				case dir && a.outline.Open.Has(id):
					svg = iconFolderOpen
				case dir:
					svg = iconFolder
				}
				ui.Icon(c, svg).FontSize(iconSize).TextColor(t.TextMuted)
				ui.Text(c, itemLabel(id)).FontSize(textSize).SingleLine().Ellipsis("…").MinWidth(0)
			})
		}).ContextMenu(func(m *ui.Menu) { a.itemMenu(m, id) })
	}).Grow(1).Padding(0, t.Space(2), t.Space(2), t.Space(2)).Label("笔记").OnChange(a.openSelected)
	a.outline.Prefix = nil
	// 开关接走了点击，列表不会选中这一行，在这里选中被点的目录；树构建完才改选择。
	if clicked != "" {
		a.chosen.Clear()
		a.chosen.Add(clicked)
	}
}

// itemMenu 是文件树行的右键菜单。
func (a *App) itemMenu(m *ui.Menu, id string) {
	dir := parentDir(id)
	if a.tree.Dirs[id] {
		dir = id
	}
	m.Submenu("新建", func(m *ui.Menu) { a.newItems(m, dir) })
	m.Separator()
	if m.Item("重命名…").Chosen() {
		a.startRename(id)
	}
	if m.Item("复制路径").Chosen() {
		a.copyPath(id)
	}
	if !a.tree.Dirs[id] && m.Item("在默认应用中打开").Chosen() {
		a.openVaultFile(id)
	}
	if m.Item("在文件管理器中显示").Chosen() {
		if name, err := a.vault.Resolve(id); err == nil {
			a.revealPath(name)
		}
	}
	m.Separator()
	if m.Item("移到废纸篓").Danger().Chosen() {
		a.trashItem(id)
	}
}

// workspaceView 并排放置侧栏与内容区，侧栏的展开与收起带动画：
//   - 完全展开时用 ui.Split，分隔条的拖动、光标、键盘操作和双击恢复都由它负责；
//   - 动画途中用固定宽度的列，侧栏内容保持完整宽度、只被裁剪，不逐帧重新换行；
//   - 完全收起时只有内容区。
func (a *App) workspaceView(c *ui.Context) {
	width, _ := c.Size()
	target := float32(1)
	if a.sidebarHidden || a.focus {
		target = 0
	}
	// 动画值挂在不占位的元素上，只动画展开程度，拖动宽度时不受动画影响。
	shown := ui.Box(c.Key("sidebar-anim")).Size(0, 0).AnimateTo("shown", target, sidebarMotion)
	a.sidebarShown = shown
	a.sidebarWidth = clampSidebar(a.sidebarWidth, width)
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		switch {
		case shown >= 1:
			ui.Split(c, &a.sidebarWidth, func() { a.sidebarView(c) }, func() { a.contentView(c) }).
				SplitReset(sidebarDefault).OnChange(a.persistWorkspace).Fill()
			// Split 只保证两侧各留 40，宽度范围在这里限制，拖动的同一帧生效。
			a.sidebarWidth = clampSidebar(a.sidebarWidth, width)
		case shown > 0:
			ui.Column(c.Key("sidebar")).Width(a.sidebarWidth*shown).Shrink(0).ClipX().
				BorderWidth(0, 1, 0, 0).BorderColor(c.Theme().Border).Children(func() {
				ui.Column(c).Width(a.sidebarWidth).Shrink(0).Grow(1).Children(func() { a.sidebarView(c) })
			})
			a.contentView(c)
		default:
			a.contentView(c)
		}
		if !a.focus {
			label := "隐藏侧栏"
			if a.sidebarHidden {
				label = "显示侧栏"
			}
			ghostButton(c.Key("sidebar-toggle"), label, iconPanelLeft).Absolute().
				Top((headerHeight - barButton) / 2).Left(toggleInset(c)).OnClick(a.toggleSidebar)
		}
	})
}
