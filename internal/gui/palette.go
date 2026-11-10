package gui

import (
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/tinymist"
	"gotyp/internal/vault"
)

// paletteLimit 是快速打开面板最多列出的结果数。
const paletteLimit = 50

// paletteState 是快速打开面板的状态，结果按查询词和文件树缓存。
type paletteState struct {
	open  bool
	query string
	// index 是高亮的行，键盘与指针共用；moved 表示按键移动了高亮，下一帧要滚到可见。
	index   int
	moved   bool
	pointer [2]float32
	results []string
	forTree *vault.Tree
	forText string
	// headingMode 为真时面板列出当前笔记的标题，结果是 headings 的下标，见 typstedit.go。
	// 用单独的标志而不是看 headings 是否为空：没有标题的笔记也要停留在标题模式。
	headingMode     bool
	headings        []tinymist.Heading
	headingsFor     *session
	headingsVersion uint64
}

// noPointer 是还没见过指针的位置：面板刚打开时指针可能正停在某一行上，不算它动过。
var noPointer = [2]float32{-1, -1}

func (a *App) openPalette() {
	if a.vault == nil {
		return
	}
	a.palette = paletteState{open: true, pointer: noPointer}
}

// pointerTo 记下指针在窗口中的位置，它正在第 i 行上。只有指针真的移动了才高亮这一行，
// 列表在静止的指针下滚动时不抢走键盘选中的行。
func (p *paletteState) pointerTo(at [2]float32, i int) {
	if at == p.pointer {
		return
	}
	if p.pointer != noPointer {
		p.index = i
	}
	p.pointer = at
}

// paletteResults 返回与查询词匹配的文件，标题命中优先于路径命中，连续命中优先于分散命中。
func (a *App) paletteResults() []string {
	p := &a.palette
	if p.forTree == a.tree && p.forText == p.query && p.results != nil {
		return p.results
	}
	p.forTree, p.forText = a.tree, p.query
	query := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, p.query)
	if p.headingMode {
		p.results = headingResults(p.headings, query)
		p.index, p.moved = 0, true
		return p.results
	}
	type match struct {
		id    string
		score int
	}
	var found []match
	for _, id := range files(a.tree) {
		if score, ok := matchScore(strings.ToLower(path.Base(id)), strings.ToLower(id+" "+a.tree.Titles[id]), query); ok {
			// 匹配程度相同时笔记排在其他文件前面。
			score *= 2
			if _, note := a.tree.Titles[id]; !note {
				score++
			}
			found = append(found, match{id, score})
		}
	}
	slices.SortStableFunc(found, func(x, y match) int { return x.score - y.score })
	p.results = make([]string, 0, min(len(found), paletteLimit))
	for _, m := range found[:min(len(found), paletteLimit)] {
		p.results = append(p.results, m.id)
	}
	// 查询变化后回到最佳匹配。
	p.index, p.moved = 0, true
	return p.results
}

// headingResults 返回名字包含查询词的标题下标，保持文档中的顺序，方便按结构浏览。
func headingResults(headings []tinymist.Heading, query string) []string {
	results := []string{}
	for i, h := range headings {
		if query == "" || strings.Contains(strings.ToLower(h.Name), query) || subsequence(strings.ToLower(h.Name), query) {
			results = append(results, strconv.Itoa(i))
		}
	}
	return results
}

// matchScore 越小越靠前：0 文件名包含，1 路径或标题包含，2 按顺序包含全部字符。
func matchScore(name, rest, query string) (int, bool) {
	switch {
	case query == "" || strings.Contains(name, query):
		return 0, true
	case strings.Contains(rest, query):
		return 1, true
	case subsequence(name, query) || subsequence(rest, query):
		return 2, true
	}
	return 0, false
}

func subsequence(s, sub string) bool {
	for _, r := range sub {
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+len(string(r)):]
	}
	return true
}

// paletteKey 处理输入框中的上下键和回车，其余按键交给输入框。
func (a *App) paletteKey(k ui.TextKey) bool {
	// 绑定值要到下一帧才更新，快速输入后立即回车时以输入框中的文字为准。
	a.palette.query = k.Text()
	results := a.paletteResults()
	switch k.Key {
	case ui.KeyDown:
		if len(results) > 0 {
			a.palette.index = (a.palette.index + 1) % len(results)
		}
	case ui.KeyUp:
		if len(results) > 0 {
			a.palette.index = (a.palette.index - 1 + len(results)) % len(results)
		}
	case ui.KeyEnter:
		a.choosePalette()
	default:
		return false
	}
	a.palette.moved = true
	a.services.Invalidate()
	return true
}

func (a *App) choosePalette() {
	results := a.paletteResults()
	a.palette.open = false
	if a.palette.index < 0 || a.palette.index >= len(results) {
		return
	}
	if a.palette.headingMode {
		i, _ := strconv.Atoi(results[a.palette.index])
		a.chooseHeading(i)
		return
	}
	a.openNote(results[a.palette.index])
}
