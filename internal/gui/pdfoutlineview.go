package gui

import (
	"cmp"
	"fmt"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/vault"
)

func (a *App) outlineSidebarView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c.Key("outline-sidebar")).Fill().MinWidth(0).MinHeight(0).Background(t.Background).Children(func() {
		ui.Row(c).Height(headerHeight).Shrink(0).DragWindow()
		ui.Divider(c)
		ui.Themed(c, a.themes.outline).Grow(1).MinHeight(0).FillWidth().
			Padding(t.Space(3)).Children(func() {
			a.pdfOutlineView(c, t.Accent)
		})
	})
}

func (a *App) pdfOutlineView(c *ui.Context, focusColor ui.Color) {
	preview := a.activePDF()
	if s := a.current; s != nil && s.kind == vault.KindTypst && a.activeTab.layout.mode == modeEdit {
		outlineStatus(c, "切换到分屏或预览以查看大纲")
		return
	}
	if preview == nil || preview.outline.displayed == 0 {
		if a.viewing != nil && isPDF(a.viewing.id) && a.viewing.note == "" ||
			a.current != nil && a.current.kind == vault.KindTypst && a.current.previewStarting {
			ui.Spinner(c).Label("正在读取大纲").Margin(pdfOutlineLabelInset(c.Theme()))
		} else {
			outlineStatus(c, "没有可用的大纲")
		}
		return
	}
	p := &preview.outline
	if p.result.DisplayedGeneration != p.displayed {
		ui.Spinner(c).Label("正在读取大纲").Margin(pdfOutlineLabelInset(c.Theme()))
		return
	}
	if len(p.rows) == 0 {
		label := "此 PDF 没有大纲"
		if p.result.Err != nil {
			label = "无法读取大纲"
		}
		outlineStatus(c, label)
		return
	}
	if p.result.Err != nil || p.result.Truncated {
		outlineStatus(c, "大纲未完整显示")
	}
	a.pdfOutlineList(c, preview, focusColor)
}

func outlineStatus(c *ui.Context, text string) {
	t := c.Theme()
	ui.Text(c, text).FontSize(detailFontSize).TextColor(t.TextMuted).
		PaddingX(pdfOutlineLabelInset(t)).Shrink(0)
}

func (a *App) pdfOutlineList(c *ui.Context, preview *pdfPreview, focusColor ui.Color) {
	p := &preview.outline
	key := a.activeTab.id
	tree := &p.tree
	tree.List.Selected, tree.List.Label = &p.selection, p.rowLabel
	var prefix ui.OutlineRow
	tree.Prefix = func(row ui.OutlineRow) { prefix = row }
	defer func() { tree.Prefix = nil }()
	current := p.visibleCurrentRow()
	id := -1
	if current >= 0 {
		id = p.rows[current].id
	}
	if p.lastHeading != id {
		p.lastHeading = id
		if current >= 0 {
			row := p.visibleRowIndex(current)
			if !tree.List.Handle.FocusWithin(c) {
				tree.List.Choose(row)
			}
			tree.List.ScrollIntoView(row)
		}
	}
	clicked := -1
	ui.Outline(c.Key("pdf-outline-tree"+key), tree, p.roots, p.children, func(id int) {
		index := p.indexes[id]
		selected := p.selection >= 0 && p.selection < tree.Rows() && tree.Item(p.selection) == id
		if pdfOutlineTitleRow(c, prefix, p.rows[index], current == index, selected && tree.List.FocusWithin(c)) {
			clicked = index
		}
	}).Grow(1).MinHeight(0).MinWidth(0).Label("章节").FocusRingColor(focusColor).OnSubmit(func() {
		if index := p.selection; index >= 0 && index < tree.Rows() {
			a.jumpPDFOutline(preview, p.indexes[tree.Item(index)])
		}
	})
	if clicked >= 0 {
		a.jumpPDFOutline(preview, clicked)
		c.AnimationFrame()
	}
}

func (p *pdfOutlineState) children(id int) []int { return p.rows[p.indexes[id]].children }

func (p *pdfOutlineState) rowLabel(index int) string {
	entry := p.rows[p.indexes[p.tree.Item(index)]].entry
	title := cmp.Or(entry.Title, "未命名书签")
	if entry.Page > 0 {
		return fmt.Sprintf("%s，第 %d 页", title, entry.Page)
	}
	return title
}

func pdfOutlineTitleRow(c *ui.Context, row ui.OutlineRow, item pdfOutlineRow, current, keyboardSelected bool) bool {
	t := c.Theme()
	title := cmp.Or(item.entry.Title, "未命名书签")
	line := ui.Row(c).Height(pdfOutlineRowHeight).Grow(1).MinWidth(0).AlignItems(ui.Center)
	text, background := t.TextMuted, t.Background
	switch {
	case current:
		text, background = t.Text, selectedFill(t).Over(t.Background)
	case keyboardSelected:
		text, background = t.Text, buttonFill(t).Over(t.Background)
	case line.Hovered():
		text, background = t.Text, rowFill(t).Over(t.Background)
	}
	depth := row.Depth
	inset := t.Space(2)
	var titleRow ui.Element
	line.PaddingX(inset).Radius(t.Radius).Background(background).Children(func() {
		for level := range depth {
			x := inset + float32(level)*pdfOutlineIndent + (pdfOutlineArrowSize-pdfOutlineGuideWidth)/2
			ui.Box(c).Absolute().Left(x).Top(0).Bottom(0).Width(pdfOutlineGuideWidth).
				Background(pdfOutlineGuideColor(t)).PassThrough()
		}
		ui.Box(c).Width(float32(depth) * pdfOutlineIndent).Shrink(0)
		pdfOutlineArrow(c, row)
		titleRow = ui.Row(c).Grow(1).MinWidth(0).AlignSelf(ui.Stretch).AlignItems(ui.Center).Tooltip(title)
		if item.entry.Page > 0 {
			titleRow.Cursor(ui.CursorPointer)
		}
		titleRow.Children(func() {
			label := ui.Text(c, title).FontSize(textSize).TextColor(text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			if current {
				label.Current(ui.CurrentLocation)
			}
		})
	})
	return item.entry.Page > 0 && titleRow.RowClicked()
}

func pdfOutlineArrow(c *ui.Context, row ui.OutlineRow) {
	arrow := row.Arrow().Size(pdfOutlineArrowSize, pdfOutlineRowHeight).Shrink(0).Center()
	if !row.Branch {
		return
	}
	t := c.Theme()
	angle := float32(90)
	if row.Open {
		angle = 180
	}
	arrow.Cursor(ui.CursorPointer).Children(func() {
		ui.Icon(c, iconChevronUp).FontSize(iconSmallSize).TextColor(t.TextMuted).Rotate(angle)
	})
}
