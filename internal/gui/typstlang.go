package gui

import (
	"strings"

	"github.com/egoist/mygo/ui/editor"
)

var typstLanguage = &editor.Language{
	Name: "Typst", Extensions: []string{".typ"},
	LineComment: "//", BlockComment: [2]string{"/*", "*/"},
	Brackets: []editor.BracketPair{
		{Open: "(", Close: ")"}, {Open: "[", Close: "]"}, {Open: "{", Close: "}"},
	},
	AutoClose: []editor.BracketPair{
		{Open: "(", Close: ")"}, {Open: "[", Close: "]"}, {Open: "{", Close: "}"}, {Open: `"`, Close: `"`},
	},
	Tokenizer: editor.TokenizerFunc(tokenizeTypst),
}

// tokenMath 是数学公式使用的颜色类别。编辑器没有专门的数学类别，借用与代码和字符串都不同的颜色。
const tokenMath = editor.TokenRegexp

var (
	typstKeywords = map[string]bool{
		"let": true, "set": true, "show": true, "import": true, "include": true, "if": true, "else": true,
		"for": true, "while": true, "in": true, "return": true, "break": true, "continue": true,
		"context": true, "as": true, "not": true, "and": true, "or": true,
	}
	typstConstants = map[string]bool{"none": true, "auto": true, "true": true, "false": true}
)

// typstState 是跨行保留的词法状态。Typst 的标记、代码和数学三种模式可以嵌套，
// 括号栈记录每一层是代码（( 与 {）还是内容（[），决定下一行从哪种模式开始。
type typstState struct {
	comment int  // 嵌套块注释的深度
	raw     bool // 位于 ``` 原始文本块中
	math    bool // 位于跨行的 $ 公式中
	depth   int
	stack   uint64 // 第 i 位为 1 表示第 i 层是内容块
}

const typstMaxDepth = 48

func unpackTypst(s uint64) typstState {
	return typstState{
		comment: int(s & 0xff), raw: s&(1<<8) != 0, math: s&(1<<9) != 0,
		depth: int(s >> 10 & 0x3f), stack: s >> 16,
	}
}

// pack 只保留当前深度以内的栈位，让同样的状态总是得到同一个数，编辑器据此判断后续行是否需要重新着色。
func (st typstState) pack() uint64 {
	stack := st.stack & (1<<st.depth - 1)
	s := uint64(st.comment&0xff) | uint64(st.depth&0x3f)<<10 | stack<<16
	if st.raw {
		s |= 1 << 8
	}
	if st.math {
		s |= 1 << 9
	}
	return s
}

type typstLexer struct {
	line string
	i    int
	st   typstState
	toks []editor.Token
	base int // toks 中属于本行的第一个下标，调用方可能传入已有内容的切片
	// lineCode 是 #let、#set 等语句所在的括号层，本行余下部分在这一层按代码处理；-1 表示没有。
	lineCode int
	// exprEnd 是刚结束的嵌入表达式的位置，紧跟其后的 ( 和 [ 属于这次调用。
	exprEnd int
}

func tokenizeTypst(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
	lx := &typstLexer{line: line, st: unpackTypst(state), toks: tokens, base: len(tokens), lineCode: -1, exprEnd: -1}
	if lx.st.comment == 0 && !lx.st.raw && !lx.st.math && !lx.inCode() && lx.lineStart() {
		return lx.toks, lx.st.pack()
	}
	for lx.i < len(line) {
		switch {
		case lx.st.comment > 0:
			lx.blockComment()
		case lx.st.raw:
			lx.rawBlock()
		case lx.st.math:
			lx.mathSpan()
		case lx.inCode():
			lx.code()
		default:
			lx.markup()
		}
	}
	lx.mark(len(line), len(line), editor.TokenText)
	return lx.toks, lx.st.pack()
}

// mark 把 [start, end) 标为 kind，之前未标记的部分作为普通文本。
func (lx *typstLexer) mark(start, end int, kind editor.TokenKind) {
	lx.emit(start, editor.TokenText)
	lx.emit(end, kind)
	lx.i = max(lx.i, end)
}

func (lx *typstLexer) emit(end int, kind editor.TokenKind) {
	last := 0
	if n := len(lx.toks); n > lx.base {
		last = lx.toks[n-1].End
		if lx.toks[n-1].Kind == kind && end > last {
			lx.toks[n-1].End = end
			return
		}
	}
	if end > last {
		lx.toks = append(lx.toks, editor.Token{End: end, Kind: kind})
	}
}

func (lx *typstLexer) inCode() bool {
	if lx.st.depth == lx.lineCode {
		return true
	}
	return lx.st.depth > 0 && lx.st.stack&(1<<(lx.st.depth-1)) == 0
}

func (lx *typstLexer) push(content bool) {
	if lx.st.depth >= typstMaxDepth {
		return
	}
	if content {
		lx.st.stack |= 1 << lx.st.depth
	} else {
		lx.st.stack &^= 1 << lx.st.depth
	}
	lx.st.depth++
}

// pop 在栈顶是指定种类时出栈，括号不匹配时保持原状，避免一处错误打乱后续所有行。
func (lx *typstLexer) pop(content bool) {
	if lx.st.depth == 0 || (lx.st.stack&(1<<(lx.st.depth-1)) != 0) != content {
		return
	}
	lx.st.depth--
	if lx.lineCode > lx.st.depth {
		lx.lineCode = -1
	}
}

func (lx *typstLexer) rest() string { return lx.line[lx.i:] }

// lineStart 处理只在标记模式行首出现的标题和列表标记，标题整行着色后直接结束。
func (lx *typstLexer) lineStart() bool {
	body := strings.TrimLeft(lx.line, " \t")
	indent := len(lx.line) - len(body)
	if n := len(body) - len(strings.TrimLeft(body, "=")); n > 0 && (n == len(body) || body[n] == ' ') {
		lx.mark(indent, len(lx.line), editor.TokenHeading)
		return true
	}
	if len(body) >= 2 && strings.ContainsRune("-+/", rune(body[0])) && body[1] == ' ' {
		lx.mark(indent, indent+1, editor.TokenPunctuation)
	}
	return false
}

func (lx *typstLexer) blockComment() {
	for lx.i < len(lx.line) && lx.st.comment > 0 {
		switch r := lx.rest(); {
		case strings.HasPrefix(r, "/*"):
			lx.st.comment++
			lx.mark(lx.i, lx.i+2, editor.TokenComment)
		case strings.HasPrefix(r, "*/"):
			lx.st.comment--
			lx.mark(lx.i, lx.i+2, editor.TokenComment)
		default:
			lx.mark(lx.i, lx.i+1, editor.TokenComment)
		}
	}
}

func (lx *typstLexer) rawBlock() {
	if end := strings.Index(lx.rest(), "```"); end >= 0 {
		lx.st.raw = false
		lx.mark(lx.i, lx.i+end+3, editor.TokenCode)
		return
	}
	lx.mark(lx.i, len(lx.line), editor.TokenCode)
}

// mathSpan 着色到下一个未转义的 $，没有时延续到下一行。
func (lx *typstLexer) mathSpan() {
	for j := lx.i; j < len(lx.line); j++ {
		switch lx.line[j] {
		case '\\':
			j++
		case '$':
			lx.st.math = false
			lx.mark(lx.i, j+1, tokenMath)
			return
		}
	}
	lx.mark(lx.i, len(lx.line), tokenMath)
}

// comment 处理两种模式共有的注释开头，返回是否处理了。
func (lx *typstLexer) comment() bool {
	switch r := lx.rest(); {
	case strings.HasPrefix(r, "//"):
		lx.mark(lx.i, len(lx.line), editor.TokenComment)
	case strings.HasPrefix(r, "/*"):
		lx.st.comment = 1
		lx.mark(lx.i, lx.i+2, editor.TokenComment)
	default:
		return false
	}
	return true
}
