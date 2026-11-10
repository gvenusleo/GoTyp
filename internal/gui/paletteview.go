package gui

import (
	"path"
	"strconv"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/tinymist"
)

// paletteView 是快速打开面板：靠窗口上方的浮层，顶部是输入框，其下是结果，底部是按键提示。
func (a *App) paletteView(c *ui.Context) {
	if !a.palette.open || a.tree == nil {
		return
	}
	t := c.Theme()
	width, height := c.Size()
	ui.DialogBase(c, &a.palette.open, func(backdrop, panel ui.Element) {
		backdrop.Justify(ui.Start).Padding(paletteTop, 0, 0).Background(backdropColor)
		panel.Width(min(paletteWidth, width-2*emptyInset)).Radius(panelRound).Clip().
			Background(t.Background).Border(1, t.Border).Shadow(0, 12, 32, 0, panelShadowColor)
		results := a.paletteResults()
		input := a.paletteInput(c)
		list, selected := a.paletteList(c, results, min(paletteHeight, height-paletteTop-90))
		// 键盘焦点一直在输入框里：告诉读屏它控制下面的结果，↑↓ 停在的是哪一项。
		input.Expanded(len(results) > 0).Controls(list).ActiveDescendant(selected)
		paletteHelp(c, a.palette.headingMode)
	})
}

// paletteInput 画输入框并返回它。结果排在输入框下面而不是弹出，所以不用 ComboboxBase，
// 只借用组合框的语义。
func (a *App) paletteInput(c *ui.Context) ui.Element {
	t := c.Theme()
	var input ui.Element
	placeholder := "搜索笔记"
	if a.palette.headingMode {
		placeholder = "跳到标题"
	}
	ui.Row(c).AlignItems(ui.Center).Gap(t.Space(2.5)).Padding(t.Space(3), t.Space(4)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Icon(c, iconSearch).FontSize(iconButtonSize).TextColor(t.TextMuted).Shrink(0)
		input = ui.TextInputBase(c.Key("palette-input"), &a.palette.query).AutoFocus().
			Placeholder(placeholder).Label(placeholder).Role(ui.RoleComboBox).
			AutoComplete(ui.AutoCompleteList).Grow(1).MinWidth(0).OnTextKey(a.paletteKey)
	})
	return input
}

// paletteList 画结果。高亮的一项是回车作用的项，键盘与指针选的是同一行。
// 返回结果列表与高亮的那一行，没有结果时都是零值。
func (a *App) paletteList(c *ui.Context, results []string, maxHeight float32) (list, highlighted ui.Element) {
	t := c.Theme()
	p := &a.palette
	if len(results) == 0 {
		empty := "没有匹配的笔记"
		if p.headingMode {
			empty = "没有匹配的标题"
		}
		ui.Text(c, empty).FontSize(textSize).TextColor(t.TextMuted).
			Padding(t.Space(6)).TextAlign(ui.Center).FillWidth()
		return list, highlighted
	}
	selected := min(p.index, len(results)-1)
	ui.Scroll(c).MaxHeight(maxHeight).Children(func() {
		list = ui.Column(c).Padding(t.Space(1.5)).Role(ui.RoleListBox).Label("搜索结果")
		list.Children(func() {
			for i, id := range results {
				choose := func() {
					p.index = i
					a.choosePalette()
				}
				var row ui.Element
				if p.headingMode {
					n, _ := strconv.Atoi(id)
					row = headingRow(c, n, p.headings[n], choose)
				} else {
					row = paletteRow(c, id, choose)
				}
				if x, y, over := row.PointerPosition(); over {
					b := row.Bounds()
					p.pointerTo([2]float32{b.X + x, b.Y + y}, i)
				}
				row.AccessSelected(i == selected)
				if i == selected {
					highlighted = row
					row.Background(selectedFill(t))
					if p.moved {
						row.ScrollIntoView()
					}
				}
			}
		})
	})
	p.moved = false
	return list, highlighted
}

// paletteRow 画一行结果：类型图标、文件名和所在目录。点击不夺走输入框的焦点。
func paletteRow(c *ui.Context, id string, choose func()) ui.Element {
	t := c.Theme()
	svg := fileIcon(id)
	row := ui.ButtonBase(c.Key(id)).Label(id).Role(ui.RoleOption).KeepFocus().
		Padding(t.Space(1.5), t.Space(2.5)).Radius(t.Radius).
		Justify(ui.Start).AlignItems(ui.Center).Gap(t.Space(2.5))
	row.OnClick(choose)
	row.Children(func() {
		ui.Icon(c, svg).FontSize(iconSize).TextColor(t.TextMuted).Shrink(0)
		ui.Text(c, path.Base(id)).FontSize(textSize).SingleLine().Ellipsis("…").MinWidth(0)
		ui.Text(c, parentDir(id)).FontSize(detailFontSize).TextColor(t.TextMuted).
			SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
	})
	return row
}

// headingRow 画一行标题：按层级缩进，与文件树的缩进一致。
func headingRow(c *ui.Context, i int, h tinymist.Heading, choose func()) ui.Element {
	t := c.Theme()
	row := ui.ButtonBase(c.Key([2]any{"heading", i})).Label(h.Name).Role(ui.RoleOption).KeepFocus().
		Padding(t.Space(1.5), t.Space(2.5), t.Space(1.5), t.Space(2.5)+float32(h.Level-1)*iconSize).
		Radius(t.Radius).Justify(ui.Start).AlignItems(ui.Center).Gap(t.Space(2.5))
	row.OnClick(choose)
	row.Children(func() {
		ui.Icon(c, iconHash).FontSize(iconSize).TextColor(t.TextMuted).Shrink(0)
		ui.Text(c, h.Name).FontSize(textSize).SingleLine().Ellipsis("…").MinWidth(0)
	})
	return row
}

// paletteHelp 画底部的按键提示。
func paletteHelp(c *ui.Context, headings bool) {
	t := c.Theme()
	open := "↵ 打开"
	if headings {
		open = "↵ 跳转"
	}
	ui.Row(c).Gap(t.Space(4)).Padding(t.Space(2), t.Space(4)).
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		for _, tip := range []string{"↑↓ 选择", open, "esc 关闭"} {
			ui.Text(c, tip).FontSize(detailFontSize).TextColor(t.TextMuted)
		}
	})
}
