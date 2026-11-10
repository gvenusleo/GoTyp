package gui

import (
	"slices"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui/editor"
)

// textStats 是一篇笔记的统计，按 buffer 版本缓存。
type textStats struct {
	words, chars, lines int
}

// countText 统计字数：汉字、假名和韩文每字计一，其他文字按连续的字母和数字计为一个词。
func countText(text string) textStats {
	st := textStats{lines: strings.Count(text, "\n") + 1}
	inWord := false
	for _, r := range text {
		if !unicode.IsSpace(r) {
			st.chars++
		}
		switch {
		case isCJK(r):
			st.words++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				st.words++
			}
			inWord = true
		default:
			inWord = false
		}
	}
	return st
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// stats 返回当前版本的统计，版本未变时直接复用。
func (s *session) stats() textStats {
	if v := s.ed.Buffer.Version(); v != s.statsVersion || !s.statsReady {
		s.statsCache, s.statsVersion, s.statsReady = countText(s.text()), v, true
	}
	return s.statsCache
}

// selectionCache 缓存选区字数，状态栏每帧都要显示，大选区不能每帧重新统计。
type selectionCache struct {
	version uint64
	sels    []editor.Selection
	count   int
	words   int
}

// selectionStats 返回非空选区的数量和其中的字数，buffer 版本和选区不变时复用上次结果。
func selectionStats(s *session) (count, words int) {
	sels := s.ed.Selections()
	c := &s.selCache
	if c.sels != nil && c.version == s.ed.Buffer.Version() && slices.Equal(c.sels, sels) {
		return c.count, c.words
	}
	for _, sel := range sels {
		if !sel.Empty() {
			count++
		}
	}
	if count > 0 {
		words = countText(s.ed.SelectedText()).words
	}
	*c = selectionCache{version: s.ed.Buffer.Version(), sels: slices.Clone(sels), count: count, words: words}
	return count, words
}

// diagnosticCounts 统计 Typst 短格式诊断中的错误和警告条数。
func diagnosticCounts(text string) (errors, warnings int) {
	for line := range strings.Lines(text) {
		switch {
		case strings.Contains(line, "error:"):
			errors++
		case strings.Contains(line, "warning:"):
			warnings++
		}
	}
	return errors, warnings
}

// typstDiagnostics 返回当前诊断：编译失败时是错误，成功时是警告。
func typstDiagnostics(s *session) (text string, errors, warnings int) {
	if s.previewErr != "" {
		errors, warnings = diagnosticCounts(s.previewErr)
		return s.previewErr, max(errors, 1), warnings
	}
	_, warnings = diagnosticCounts(s.previewDiag)
	return s.previewDiag, 0, warnings
}

// statusDiagnostics 展开或收起诊断面板，纯编辑模式下先切到分屏让面板可见。
func (a *App) statusDiagnostics() {
	s := a.current
	if s == nil {
		return
	}
	if a.mode == modeEdit {
		a.setMode(modeSplit)
		s.previewErrOpen = true
		return
	}
	s.previewErrOpen = !s.previewErrOpen
}

func (a *App) statusGoToLine() {
	if s := a.current; s != nil {
		s.ed.Run(editor.GoToLine)
	}
}
