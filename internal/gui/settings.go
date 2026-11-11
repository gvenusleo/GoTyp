package gui

import (
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gotyp/internal/vault"
)

// defaultFont 是字体框里代表「用默认字体」的一项，对应外观设置里的空串。
const defaultFont = "默认"

// themeChoice 是主题分段的一项：图标显示在控件上，名称用于悬停提示与读屏。
type themeChoice struct {
	label  string
	icon   *ui.SVG
	source mygo.ThemeSource
}

// themeChoices 的次序与界面一致，跟随系统在最后。
var themeChoices = []themeChoice{
	{"浅色", iconSun, mygo.ThemeLight},
	{"深色", iconMoon, mygo.ThemeDark},
	{"跟随系统", iconMonitor, mygo.ThemeSystem},
}

// themeSource 把保存的主题换成 MyGo 的取值，空串和不认识的值都跟随系统。
func themeSource(name string) mygo.ThemeSource {
	switch s := mygo.ThemeSource(name); s {
	case mygo.ThemeLight, mygo.ThemeDark:
		return s
	}
	return mygo.ThemeSystem
}

func themeIndex(name string) int {
	source := themeSource(name)
	return slices.IndexFunc(themeChoices, func(c themeChoice) bool { return c.source == source })
}

// uiFamily 是界面字体族：缺的字形退回系统界面字体；空表示系统的。
func uiFamily(name string) string {
	if name == "" {
		return ""
	}
	return name + ", system-ui"
}

// codeFamily 是代码字体族：缺的字形退回系统等宽字体。
func codeFamily(name string) string {
	if name == "" {
		return "monospace"
	}
	return name + ", monospace"
}

// settingsState 是设置面板的状态，各项在打开时取自当前的外观设置。
type settingsState struct {
	open bool
	// gen 是打开的次数，后台读取的字体列表据此作废。
	gen      int
	category int
	theme    int
	// 两个字体框显示的字体名，默认的显示为 defaultFont。
	uiFont, codeFont string
	// 字体框的选项，默认的在最前；系统字体读回之前只有默认的。
	uiFonts, codeFonts []string
	recolor            bool
}

func (a *App) openSettings() {
	s := &a.settings
	ap := a.state.Appearance
	*s = settingsState{
		open: true, gen: s.gen + 1, category: s.category,
		theme:  themeIndex(ap.Theme),
		uiFont: fontLabel(ap.UIFont), codeFont: fontLabel(ap.CodeFont),
		uiFonts: []string{defaultFont}, codeFonts: []string{defaultFont},
		recolor: ap.RecolorPreview,
	}
	a.loadFonts(s.gen)
}

// loadFonts 在后台列出系统字体：界面字体可以选全部，代码字体只列等宽的。
// 第一次列举要几十毫秒，所以不放在主线程。
func (a *App) loadFonts(gen int) {
	go func() {
		var all, mono []string
		for _, f := range ui.FontFamilies() {
			all = append(all, f.Name)
			if f.Monospaced {
				mono = append(mono, f.Name)
			}
		}
		a.publish(func() {
			if s := &a.settings; s.gen == gen {
				s.uiFonts = slices.Concat([]string{defaultFont}, all)
				s.codeFonts = slices.Concat([]string{defaultFont}, mono)
			}
		})
	}()
}

func fontLabel(name string) string {
	if name == "" {
		return defaultFont
	}
	return name
}

func fontSetting(label string) string {
	if label == defaultFont {
		return ""
	}
	return label
}

func (a *App) chooseTheme() {
	ap := a.state.Appearance
	ap.Theme = ""
	if source := themeChoices[a.settings.theme].source; source != mygo.ThemeSystem {
		ap.Theme = string(source)
	}
	a.setAppearance(ap)
}

func (a *App) chooseFonts() {
	ap := a.state.Appearance
	ap.UIFont, ap.CodeFont = fontSetting(a.settings.uiFont), fontSetting(a.settings.codeFont)
	a.setAppearance(ap)
}

func (a *App) choosePreview() {
	ap := a.state.Appearance
	ap.RecolorPreview = a.settings.recolor
	a.setAppearance(ap)
}

// setAppearance 立即生效并保存。界面主题按外观设置缓存（见 applyTheme），下一帧自动重建；
// 编辑器字体存在会话里，这里同步。
func (a *App) setAppearance(ap vault.Appearance) {
	old := a.state.Appearance
	if ap == old {
		return
	}
	a.state.Appearance = ap
	if themeSource(ap.Theme) != themeSource(old.Theme) && a.win != nil {
		// SetSource 会让窗口重新取主题，不在构建视图的过程中调用，放到这一帧之后。
		source := themeSource(ap.Theme)
		a.win.Update(func() { mygo.Theme.SetSource(source) })
	}
	if a.current != nil {
		a.applyEditorPrefs(a.current)
	}
	// 调色开关和主题不用在这里处理：预览任务发现配色不一致后自行重启。
	a.saveState()
}
