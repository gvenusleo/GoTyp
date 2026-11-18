package gui

import "github.com/egoist/mygo/ui"

// settingsCategory 是设置面板左侧的一个分类，view 画它右侧的各项。
type settingsCategory struct {
	name string
	icon *ui.SVG
	view func(a *App, c *ui.Context)
}

var settingsCategories = []settingsCategory{
	{"外观", iconPalette, (*App).appearanceView},
}

// settingsView 是设置面板：左侧是分类，右侧是当前分类的各项，右上角关闭。
func (a *App) settingsView(c *ui.Context) {
	if !a.settings.open {
		return
	}
	t := c.Theme()
	width, height := c.Size()
	ui.DialogBase(c, &a.settings.open, func(backdrop, panel ui.Element) {
		backdrop.Background(backdropColor)
		panel.Width(min(settingsWidth, width-2*emptyInset)).Height(min(settingsHeight, height-2*emptyInset)).
			Radius(panelRound).Clip().Background(t.Background).Border(1, t.Border).
			Shadow(0, 12, 32, 0, panelShadowColor).Label("设置")
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.settingsNav(c)
			a.settingsContent(c)
		})
	})
}

// settingsNav 画左侧的分类，点击或上下方向键切换。
func (a *App) settingsNav(c *ui.Context) {
	t := c.Theme()
	s := &a.settings
	ui.Column(c).Width(settingsNavWidth).Shrink(0).Padding(settingsPad, t.Space(2)).Gap(t.Space(3)).
		Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Text(c, "设置").Bold().Padding(0, t.Space(2))
		tabs := ui.TabsBase(c.Key("settings-nav"), &s.category, len(settingsCategories))
		tabs.List.Column().FocusGroup(ui.Vertical).AlignItems(ui.Stretch).Gap(2).Label("设置分类").
			Children(func() {
				for i, category := range settingsCategories {
					tab := tabs.Tab(i).AlignItems(ui.Center).Gap(t.Space(2)).Padding(t.Space(1.5), t.Space(2)).
						Radius(t.Radius).Label(category.name).Transition(hoverMotion)
					color := t.TextMuted
					switch {
					case i == s.category:
						tab.Background(selectedFill(t))
						color = t.Text
					case tab.Pressed():
						tab.Background(pressFill(t))
					case tab.Hovered():
						tab.Background(rowFill(t))
					}
					tab.Children(func() {
						ui.Icon(c, category.icon).FontSize(iconSize).TextColor(color)
						ui.Text(c, category.name).FontSize(textSize).TextColor(color)
					})
				}
			})
	})
}

// settingsContent 画右侧：分类标题固定在顶部，其下的各项放不下时滚动。
func (a *App) settingsContent(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Padding(settingsPad-t.Space(1), t.Space(3), 0, settingsPad).
			Children(func() {
				ui.Text(c, settingsCategories[a.settings.category].name).Bold().Grow(1)
				ghostButton(c.Key("settings-close"), "关闭设置", iconClose).
					OnClick(func() { a.settings.open = false })
			})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Gap(settingsGap).Padding(settingsPad).Children(func() {
				settingsCategories[a.settings.category].view(a, c)
			})
		})
	})
}

// appearanceView 画「外观」：主题、界面字体、代码字体，以及按当前字体排版的预览。
func (a *App) appearanceView(c *ui.Context) {
	s := &a.settings
	settingRow(c, "主题", func() { a.themeSwitch(c) })
	settingRow(c, "界面字体", func() {
		fontBox(c, "界面字体", &s.uiFont, s.uiFonts, a.chooseFonts)
	})
	settingRow(c, "代码字体", func() {
		fontBox(c, "代码字体", &s.codeFont, s.codeFonts, a.chooseFonts)
	})
	a.fontPreview(c)
}

// themeSwitch 与文档头的模式分段同一种样式：浅色轨道，选中段浮起。
func (a *App) themeSwitch(c *ui.Context) {
	t := c.Theme()
	s := &a.settings
	seg := ui.SegmentedBase(c.Key("settings-theme"), &s.theme, len(themeChoices))
	seg.Track.Padding(modeTrackInset).Radius(t.Radius + 1).Background(t.Surface).Label("主题").Children(func() {
		for i, choice := range themeChoices {
			part := seg.Segment(i).Padding(t.Space(1), t.Space(2.5)).Radius(t.Radius - 1).
				Label(choice.label).Tooltip(choice.label).Transition(hoverMotion)
			color := t.TextMuted
			if i == s.theme {
				part.Background(raisedFill(t)).Shadow(0, modeShadowY, modeShadowBlur, 0, modeShadowColor)
				color = t.Text
			} else if part.Hovered() {
				color = t.Text
			}
			part.Children(func() { ui.Icon(c, choice.icon).FontSize(iconSize).TextColor(color) })
		}
	}).OnChange(a.chooseTheme)
}

// fontBox 是带搜索的字体下拉。选项行的高亮换成中性浅底，强调色只留给焦点。
func fontBox(c *ui.Context, label string, value *string, options []string, onChange func()) {
	t := c.Theme()
	local := *t
	local.Accent, local.AccentText = rowFill(t), t.Text
	// 留零时窗口不在前台，MyGo 会把高亮行换成由 AccentText 推出的中灰。
	local.InactiveAccent = local.Accent
	c.SetTheme(&local)
	ui.Combobox(c.Key("settings-"+label), value, options).Label(label).
		Width(comboWidth).FontSize(textSize).OnChange(onChange)
	c.SetTheme(t)
}

// settingRow 画一项设置：左边是名称，右边是控件。
func settingRow(c *ui.Context, name string, control func()) {
	t := c.Theme()
	ui.Row(c).AlignItems(ui.Center).Gap(t.Space(4)).Children(func() {
		ui.Text(c, name).FontSize(textSize).Grow(1).MinWidth(0)
		control()
	})
}

// fontPreview 用当前选的字体排一段正文和一段代码。
func (a *App) fontPreview(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(t.Space(1.5)).Padding(t.Space(3), t.Space(3.5)).Radius(t.Radius).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Text(c, "落霞与孤鹜齐飞 The quick brown fox").FontSize(textSize)
		ui.Text(c, `#let title = "你好，GoTyp" // 0123456789`).
			Font(codeFamily(a.state.Appearance.CodeFont)).FontSize(codeFontSize)
	})
}
