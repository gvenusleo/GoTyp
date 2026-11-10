package gui

import (
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/vault"
)

// statusBarView 是内容区底部的状态栏：左侧是编译状态和后台活动，右侧是编辑位置和统计。
// 保存状态由文件名旁的圆点和编辑区上方的提示行表达，不在这里重复。
// 窄窗口下依次隐藏页数、字数和位置，编译错误始终保留。
func (a *App) statusBarView(c *ui.Context, s *session) {
	t := c.Theme()
	width := a.contentWidth(c)
	ui.Row(c.Key("status")).Height(statusHeight).Shrink(0).Padding(0, t.Space(1.5)).Gap(t.Space(0.5)).
		AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		if s == nil {
			if a.viewing != nil {
				ui.Spacer(c)
				a.viewerStatus(c, a.viewing)
			}
			return
		}
		if s.kind == vault.KindTypst {
			a.compileStatus(c, s)
		}
		if s.exporting {
			statusItem(c, "正在导出 PDF", nil, "正在导出 PDF", t.TextMuted, nil, true)
		}
		ui.Spacer(c)
		if width >= statusPositionWidth {
			a.positionStatus(c, s)
		}
		if width >= statusStatsWidth {
			st := s.stats()
			tip := fmt.Sprintf("%s 个字符 · %s 行 · 约 %d 分钟读完",
				thousands(st.chars), thousands(st.lines), max(1, (st.words+299)/300))
			statusItem(c, tip, nil, thousands(st.words)+" 字", t.TextMuted, nil, false)
		}
		if width >= statusPagesWidth && s.kind == vault.KindTypst && len(s.previewPages) > 0 {
			if z := int(math.Round(float64(a.zoomFactor()) * 100)); z != 100 && a.zoomable() {
				statusItem(c, "恢复 100% ⌘0", nil, strconv.Itoa(z)+"%", t.TextMuted, a.resetPreviewZoom, false)
			}
			statusItem(c, "页数", nil, strconv.Itoa(len(s.previewPages))+" 页", t.TextMuted, nil, false)
		}
	})
}

func (a *App) compileStatus(c *ui.Context, s *session) {
	t := c.Theme()
	_, errors, warnings := typstDiagnostics(s)
	switch {
	case previewUnavailable(s):
		statusItem(c, s.previewErr, iconAlert, "未找到 typst", t.TextMuted, nil, false)
		return
	case previewBusy(c, s):
		statusItem(c, "正在编译", nil, "编译中", t.TextMuted, nil, true)
		return
	}
	if errors > 0 {
		statusItem(c, "查看编译错误", iconCircleX, fmt.Sprintf("%d 个错误", errors), t.Danger, a.statusDiagnostics, false)
	}
	if warnings > 0 {
		statusItem(c, "查看编译警告", iconWarning, fmt.Sprintf("%d 个警告", warnings), t.Warning, a.statusDiagnostics, false)
	}
	if errors == 0 && warnings == 0 && s.previewDur > 0 {
		statusItem(c, "上次编译耗时", iconCheck, formatCompile(s.previewDur), t.TextMuted.Alpha(statusMutedOpacity), nil, false)
	}
}

// positionStatus 没有选区时显示行列，有选区时显示选中字数，多处选区时一并显示处数。
func (a *App) positionStatus(c *ui.Context, s *session) {
	t := c.Theme()
	text := ""
	switch count, words := selectionStats(s); {
	case count > 1:
		text = fmt.Sprintf("已选 %d 处 · %s 字", count, thousands(words))
	case count == 1:
		text = "已选 " + thousands(words) + " 字"
	default:
		pos := s.ed.CursorPos()
		// Pos.Col 是字节数，中文一个字占 3 个字节，状态栏按字符显示。
		line := s.ed.Buffer.Line(pos.Line)
		col := utf8.RuneCountInString(line[:min(pos.Col, len(line))])
		text = fmt.Sprintf("第 %d 行，第 %d 列", pos.Line+1, col+1)
	}
	statusItem(c, "跳到行 ⌃G", nil, text, t.TextMuted, a.statusGoToLine, false)
}

// statusItem 是状态栏中的一项：可点击时是悬停出现浅底的无边框按钮，否则只是文字。
func statusItem(c *ui.Context, tip string, svg *ui.SVG, text string, color ui.Color, onClick func(), busy bool) {
	t := c.Theme()
	content := func() {
		ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
			if busy {
				ui.Spinner(c).Size(spinnerSize, spinnerSize).Label(tip)
			} else if svg != nil {
				ui.Icon(c, svg).FontSize(detailFontSize).TextColor(color)
			}
			ui.Text(c, text).FontSize(detailFontSize).TextColor(color).SingleLine().FontFeatures("tnum")
		})
	}
	if onClick == nil {
		ui.Row(c).Padding(0, t.Space(1.5)).Height(statusItemHeight).AlignItems(ui.Center).Tooltip(tip).Children(content)
		return
	}
	b := ui.ButtonBase(c).Label(text).Tooltip(tip).Padding(0, t.Space(1.5)).Height(statusItemHeight).
		Radius(t.Radius - 2).Transition(hoverMotion)
	if b.Hovered() {
		b.Background(buttonFill(t))
	}
	b.Children(content).OnClick(onClick)
}

// formatCompile 把编译耗时显示为「12 ms」或「1.3 s」。
func formatCompile(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", max(1, d.Milliseconds()))
	}
	return fmt.Sprintf("%.1f s", d.Seconds())
}

// thousands 给数字加千位分隔符。
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
