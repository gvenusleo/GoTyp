package gui

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"gotyp/internal/vault"
)

// 布局尺寸集中在这里；view 中的相对间距继续使用主题的 Space。
const (
	// headerHeight 是文档头与侧栏顶部的高度，红绿灯在其中垂直居中，见 app.go。
	headerHeight      = 38
	statusHeight      = 28
	statusItemHeight  = 22
	textWidth         = 720
	pageWidth         = 800
	previewInset      = 20
	previewSideInset  = 28
	emptyInset        = 24
	diagnosticsHeight = 180
	hoverHeight       = 320

	sidebarMin      = 180
	sidebarMax      = 420
	sidebarDefault  = 248
	minContentWidth = 360
	splitMinWidth   = 860
	vaultHeight     = 44
	vaultBadge      = 28

	// 快速打开面板宽 paletteWidth，距窗口上沿 paletteTop，结果最高 paletteHeight，超出滚动。
	paletteWidth  = 560
	paletteHeight = 360
	paletteTop    = 96
	panelRound    = 12
	dialogWidth   = 360
	hintWidth     = 200

	// 设置面板的尺寸：settingsPad 是内容与面板边缘的留白，settingsGap 是各项之间的间距，
	// comboWidth 放得下大多数字体名。
	settingsWidth    = 600
	settingsHeight   = 400
	settingsNavWidth = 168
	settingsPad      = 18
	settingsGap      = 14
	comboWidth       = 220

	statusPositionWidth = 380
	statusStatsWidth    = 460
	statusPagesWidth    = 540

	// barButton 是纯图标按钮的边长，侧栏开关、新建和「更多」等按钮一样大。
	barButton      = 26
	entrySlot      = 20
	iconSize       = 14
	iconSmallSize  = 13
	iconButtonSize = 16
	iconFolderSize = 15
	emptyIconSize  = 28
	spinnerSize    = 10
	unsavedDotSize = 6
	modeTrackInset = 2
	modeShadowY    = 1
	modeShadowBlur = 2

	bannerOpacity      = 0.10
	statusMutedOpacity = 0.8
)

// 字号，单位为 DIP。正文沿用主题字号；文件树和列表略小，状态栏和诊断再小一号。
const (
	textSize       = 13
	detailFontSize = 12
	captionSize    = 10 // 仓库路径这类最次要的附注
	codeFontSize   = 13 // 编辑器与设置里的代码预览
)

var (
	modeShadowColor = ui.RGBA(0, 0, 0, 0.08)
	pageShadowColor = ui.RGBA(0, 0, 0, 0.06)
	// 浮层面板压暗背后的窗口，阴影比卡片重，看得出它浮在最上层。
	backdropColor    = ui.RGBA(0, 0, 0, 0.25)
	panelShadowColor = ui.RGBA(0, 0, 0, 0.25)
)

// toggleInset 是侧栏开关距窗口左边的距离：紧贴红绿灯右侧，侧栏展开和收起时位置不变。
func toggleInset(c *ui.Context) float32 {
	if left := c.TitleBar().Left; left > 0 {
		return left + 6
	}
	return 12
}

// themeCache 保存由系统主题和外观设置派生的应用主题，两者不变时复用同一份。
// 编辑器和 Markdown 按指针判断主题是否变化，每帧换新指针会让它们每帧重算。
type themeCache struct {
	base       ui.Theme
	appearance vault.Appearance
	app        *ui.Theme
	sidebar    *ui.Theme
	code       *editor.Theme
	md         *markdown.Theme
}

// applyTheme 在系统主题上换用中性灰阶、紧凑尺寸和选定的字体，强调色保持系统设置。
func (a *App) applyTheme(c *ui.Context) {
	base, ap := *c.Theme(), a.state.Appearance
	if a.themes.app == nil || a.themes.base != base || a.themes.appearance != ap {
		app := appTheme(base, uiFamily(ap.UIFont))
		code := codeTheme(app)
		a.themes = themeCache{
			base: base, appearance: ap, app: app, sidebar: sidebarTheme(*app),
			code: code, md: mdTheme(app, code, codeFamily(ap.CodeFont)),
		}
	}
	c.SetTheme(a.themes.app)
}

func appTheme(t ui.Theme, font string) *ui.Theme {
	t.Font = font
	if t.Dark {
		t.Background = ui.Hex("#151517")
		t.Surface = ui.Hex("#1c1c1f")
		t.Border = ui.Hex("#2b2b30")
		t.Text = ui.Hex("#e6e6e9")
		t.TextMuted = ui.Hex("#8a8a93")
		t.Scrollbar = ui.RGBA(255, 255, 255, 0.18)
	} else {
		t.Background = ui.Hex("#ffffff")
		t.Surface = ui.Hex("#f7f7f8")
		t.Border = ui.Hex("#e7e7ea")
		t.Text = ui.Hex("#1d1d20")
		t.TextMuted = ui.Hex("#6b6b73")
		t.Scrollbar = ui.RGBA(0, 0, 0, 0.18)
	}
	// 悬停与按下是文字色叠在主区底色上；深色下侧栏比主区亮，叠得太淡就分不开。
	hover, pressed := float32(0.06), float32(0.10)
	if t.Dark {
		hover, pressed = 0.09, 0.13
	}
	t.SurfaceHover = t.Text.Alpha(hover).Over(t.Background)
	t.SurfacePressed = t.Text.Alpha(pressed).Over(t.Background)
	t.Radius = 6
	t.Spacing = 3.5
	t.ScrollbarWidth = 5
	t.TooltipDelay = 600 * time.Millisecond
	return &t
}

// sidebarTheme 让侧栏以 Surface 为底，行高略松。Outline 在创建时从主题取色，选中行默认用强调色，
// 这里换成中性浅底、字色不变。InactiveAccent 设成同一个浅底：留零时窗口不在前台，
// MyGo 会把选中行换成由 AccentText 推出的中灰，反而更重。
func sidebarTheme(t ui.Theme) *ui.Theme {
	t.Background = t.Surface
	t.Spacing = 4.5
	t.Accent = selectedFill(&t)
	t.AccentText = t.Text
	t.InactiveAccent = t.Accent
	t.SurfaceHover = rowFill(&t)
	return &t
}

// 铺在底色上的中性浅底，由浅到深：rowFill 是列表行的悬停，buttonFill 是按钮的悬停，
// selectedFill 是选中的行，pressFill 是按下。都是文字色的透明度，主区和侧栏上同样适用。
func rowFill(t *ui.Theme) ui.Color    { return t.Text.Alpha(0.05) }
func buttonFill(t *ui.Theme) ui.Color { return t.Text.Alpha(0.07) }
func pressFill(t *ui.Theme) ui.Color  { return t.Text.Alpha(0.11) }

func selectedFill(t *ui.Theme) ui.Color {
	if t.Dark {
		return t.Text.Alpha(0.10)
	}
	return t.Text.Alpha(0.08)
}

// raisedFill 是分段控件中选中段的底色，比轨道亮一档，深色外观下不能比轨道更暗。
func raisedFill(t *ui.Theme) ui.Color {
	if t.Dark {
		return t.SurfacePressed
	}
	return t.Background
}

// codeTheme 是编辑器与 Markdown 代码块的配色。在 MyGo 默认值上改：底色与主区一致，
// 当前行和行号更淡，语法色取 GitHub 的一组，标题保持正文色加粗，让笔记源码读起来安静。
func codeTheme(t *ui.Theme) *editor.Theme {
	th := editor.DefaultTheme(t)
	th.Background, th.Text = t.Background, t.Text
	th.LineNumber = t.TextMuted.Alpha(0.6)
	th.ActiveLineNumber = t.TextMuted
	th.ActiveLine = t.Text.Alpha(0.03)
	th.IndentGuide = t.Border
	th.Error, th.Warning, th.Hint = t.Danger, t.Warning, t.TextMuted
	tok := [6]ui.Color{
		ui.Hex("#CF222E"), ui.Hex("#0A3069"), ui.Hex("#0550AE"),
		ui.Hex("#8250DF"), ui.Hex("#953800"), ui.Hex("#116329"),
	}
	if t.Dark {
		tok = [6]ui.Color{
			ui.Hex("#FF7B72"), ui.Hex("#A5D6FF"), ui.Hex("#79C0FF"),
			ui.Hex("#D2A8FF"), ui.Hex("#FFA657"), ui.Hex("#7EE787"),
		}
	}
	setTokens(th, t, tok)
	return th
}

// setTokens 给语法色全部显式设色，tok 依次是关键字、字符串、数字、函数、类型与标签的颜色。
func setTokens(th *editor.Theme, t *ui.Theme, tok [6]ui.Color) {
	set := func(color ui.Color, kinds ...editor.TokenKind) {
		for _, k := range kinds {
			th.Tokens[k].Color = color
		}
	}
	set(t.Text, editor.TokenText, editor.TokenVariable, editor.TokenPunctuation,
		editor.TokenEmphasis, editor.TokenStrong, editor.TokenHeading)
	set(tok[0], editor.TokenKeyword, editor.TokenOperator)
	set(tok[1], editor.TokenString, editor.TokenLink, editor.TokenCode)
	set(tok[2], editor.TokenNumber, editor.TokenConstant, editor.TokenProperty,
		editor.TokenAttribute, editor.TokenEscape)
	set(tok[3], editor.TokenFunction, editor.TokenMeta)
	set(tok[4], editor.TokenType)
	set(tok[5], editor.TokenTag, editor.TokenRegexp)
	set(t.TextMuted, editor.TokenComment)
	set(t.Success, editor.TokenInserted)
	set(t.Danger, editor.TokenDeleted, editor.TokenInvalid)
	th.Tokens[editor.TokenEmphasis].Italic = true
	th.Tokens[editor.TokenStrong].Bold = true
	th.Tokens[editor.TokenHeading].Bold = true
}

// mdTheme 是 Markdown 预览与悬停文档的配色：代码块的语法色与编辑器一致，底色沿用 MyGo 的代码框。
func mdTheme(t *ui.Theme, code *editor.Theme, codeFont string) *markdown.Theme {
	th := markdown.DefaultTheme(t)
	th.CodeFont = codeFont
	colors := *code
	colors.Background = th.CodeBlockBackground
	th.CodeColors = &colors
	return th
}

// bannerMotion 让提示行展开出现、收起消失，避免内容突然跳动。
var bannerMotion = ui.ElementTransition{
	Enter: &ui.Motion{Collapse: true}, Exit: &ui.Motion{Collapse: true},
	Duration: 180 * time.Millisecond,
}

// sidebarMotion 是侧栏展开程度从 0 到 1 的动画。
var sidebarMotion = ui.Animation{Duration: 200 * time.Millisecond}

// hoverMotion 让悬停和选中底色淡入淡出。
var hoverMotion = ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond}
