package gui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/editor"
)

// markup 处理标记模式下的一个记号：注释、转义、原始文本、公式、嵌入表达式、强调、标签、引用和链接。
func (lx *typstLexer) markup() {
	if lx.comment() {
		return
	}
	r, c := lx.rest(), lx.line[lx.i]
	switch {
	case c == '\\':
		lx.mark(lx.i, min(lx.i+1+runeLen(r[1:]), len(lx.line)), editor.TokenEscape)
	case strings.HasPrefix(r, "```"):
		lx.st.raw = true
		lx.mark(lx.i, lx.i+3, editor.TokenCode)
		lx.rawBlock()
	case c == '`':
		lx.delimited('`', editor.TokenCode)
	case c == '$':
		lx.st.math = true
		lx.mark(lx.i, lx.i+1, tokenMath)
	case c == '#':
		lx.hash()
	case (c == '(' || c == '[') && lx.i == lx.exprEnd:
		lx.push(c == '[')
		lx.mark(lx.i, lx.i+1, editor.TokenPunctuation)
	case c == ']' && lx.st.depth > 0:
		lx.pop(true)
		lx.mark(lx.i, lx.i+1, editor.TokenPunctuation)
		lx.exprEnd = lx.i
	case (c == '*' || c == '_') && !wordBefore(lx.line, lx.i):
		lx.emphasis(c)
	case c == '<':
		lx.label()
	case c == '@' && identStart(r[1:]):
		lx.mark(lx.i, lx.i+1+identLen(r[1:]), editor.TokenLink)
	case strings.HasPrefix(r, "https://") || strings.HasPrefix(r, "http://"):
		lx.mark(lx.i, lx.i+strings.IndexFunc(r+" ", isURLEnd), editor.TokenLink)
	default:
		lx.i += runeLen(r)
	}
}

// hash 处理 # 开头的嵌入表达式：#函数调用、#变量、#语句以及 #( #{ #[ 块。
func (lx *typstLexer) hash() {
	r := lx.rest()[1:]
	switch {
	case r != "" && strings.ContainsRune("({[", rune(r[0])):
		lx.mark(lx.i, lx.i+1, editor.TokenKeyword)
		lx.exprEnd = lx.i
	case identStart(r):
		word := r[:identLen(r)]
		end := lx.i + 1 + len(word)
		switch kind := identKind(word, lx.line[end:]); kind {
		case editor.TokenKeyword:
			lx.lineCode = lx.st.depth
			lx.mark(lx.i, end, kind)
		default:
			lx.mark(lx.i, end, kind)
			lx.fields()
			lx.exprEnd = lx.i
		}
	default:
		lx.i++
	}
}

// fields 处理 .字段 和 .方法 链。
func (lx *typstLexer) fields() {
	for strings.HasPrefix(lx.rest(), ".") && identStart(lx.rest()[1:]) {
		r := lx.rest()[1:]
		end := lx.i + 1 + identLen(r)
		kind := editor.TokenProperty
		if next := lx.line[end:]; strings.HasPrefix(next, "(") || strings.HasPrefix(next, "[") {
			kind = editor.TokenFunction
		}
		lx.mark(lx.i, lx.i+1, editor.TokenPunctuation)
		lx.mark(lx.i, end, kind)
	}
}

// emphasis 着色同一行内成对的 *粗体* 或 _斜体_，找不到结尾时当作普通字符。
func (lx *typstLexer) emphasis(c byte) {
	for j := lx.i + 1; j < len(lx.line); j++ {
		if lx.line[j] == '\\' {
			j++
			continue
		}
		if lx.line[j] == c && j > lx.i+1 && !wordAfter(lx.line, j+1) {
			kind := editor.TokenStrong
			if c == '_' {
				kind = editor.TokenEmphasis
			}
			lx.mark(lx.i, j+1, kind)
			return
		}
	}
	lx.i++
}

// delimited 着色到同一行的下一个 c，找不到时只跳过开头字符。
func (lx *typstLexer) delimited(c byte, kind editor.TokenKind) {
	if end := strings.IndexByte(lx.rest()[1:], c); end >= 0 {
		lx.mark(lx.i, lx.i+end+2, kind)
		return
	}
	lx.i++
}

// label 着色 <标签>，标签名由字母、数字、- _ . : 组成。
func (lx *typstLexer) label() {
	r := lx.rest()
	end := 1
	for end < len(r) && (isIdentByte(r[end]) || strings.IndexByte("-_.:", r[end]) >= 0) {
		end++
	}
	if end > 1 && end < len(r) && r[end] == '>' {
		lx.mark(lx.i, lx.i+end+1, editor.TokenAttribute)
		return
	}
	lx.i++
}

// code 处理代码模式下的一个记号。
func (lx *typstLexer) code() {
	if lx.comment() {
		return
	}
	r, c := lx.rest(), lx.line[lx.i]
	switch {
	case c == ' ' || c == '\t':
		lx.i++
	case c == '"':
		lx.str()
	case c >= '0' && c <= '9':
		lx.mark(lx.i, lx.i+numberLen(r), editor.TokenNumber)
	case identStart(r):
		lx.ident()
	case c == '(' || c == '{':
		lx.push(false)
		lx.mark(lx.i, lx.i+1, editor.TokenPunctuation)
	case c == '[':
		lx.push(true)
		lx.mark(lx.i, lx.i+1, editor.TokenPunctuation)
	case c == ')' || c == '}':
		lx.pop(false)
		lx.mark(lx.i, lx.i+1, editor.TokenPunctuation)
		lx.exprEnd = lx.i
	case c == '$':
		lx.st.math = true
		lx.mark(lx.i, lx.i+1, tokenMath)
	case c == ';':
		if lx.lineCode == lx.st.depth {
			lx.lineCode = -1
		}
		lx.mark(lx.i, lx.i+1, editor.TokenPunctuation)
	case strings.IndexByte(",:.#", c) >= 0:
		lx.mark(lx.i, lx.i+1, editor.TokenPunctuation)
	case strings.IndexByte("=+-*/<>!", c) >= 0:
		end := lx.i + 1
		for end < len(lx.line) && strings.IndexByte("=>.", lx.line[end]) >= 0 {
			end++
		}
		lx.mark(lx.i, end, editor.TokenOperator)
	default:
		lx.i += runeLen(r)
	}
}

func (lx *typstLexer) ident() {
	r := lx.rest()
	word := r[:identLen(r)]
	end := lx.i + len(word)
	kind := identKind(word, lx.line[end:])
	if kind == editor.TokenVariable {
		next := strings.TrimLeft(lx.line[end:], " ")
		switch {
		case lx.i > 0 && lx.line[lx.i-1] == '.':
			kind = editor.TokenProperty
		case strings.HasPrefix(next, ":") && lx.st.depth == lx.lineCode:
			kind = editor.TokenFunction // show 规则的选择器，例如 heading:
		case strings.HasPrefix(next, ":"):
			kind = editor.TokenProperty // 具名参数和字典键
		}
	}
	lx.mark(lx.i, end, kind)
}

func (lx *typstLexer) str() {
	for j := lx.i + 1; j < len(lx.line); j++ {
		switch lx.line[j] {
		case '\\':
			j++
		case '"':
			lx.mark(lx.i, j+1, editor.TokenString)
			return
		}
	}
	lx.mark(lx.i, len(lx.line), editor.TokenString)
}

// identKind 按名称和紧随其后的字符判断标识符类别：紧跟 ( 或 [ 的是函数调用。
func identKind(word, next string) editor.TokenKind {
	switch {
	case typstKeywords[word]:
		return editor.TokenKeyword
	case typstConstants[word]:
		return editor.TokenConstant
	case strings.HasPrefix(next, "(") || strings.HasPrefix(next, "["):
		return editor.TokenFunction
	}
	return editor.TokenVariable
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func identStart(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return r == '_' || unicode.IsLetter(r)
}

// identLen 返回标识符长度。Typst 标识符可以包含 -，但不能以 - 结尾。
func identLen(s string) int {
	n := 0
	for n < len(s) {
		r, size := utf8.DecodeRuneInString(s[n:])
		if r != '_' && r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		n += size
	}
	for n > 0 && s[n-1] == '-' {
		n--
	}
	return n
}

// numberLen 返回数字及其单位的长度，例如 12pt、1.5em、50%、1e3。
func numberLen(s string) int {
	n := 0
	for n < len(s) && (s[n] >= '0' && s[n] <= '9' || s[n] == '.' && n+1 < len(s) && s[n+1] >= '0' && s[n+1] <= '9') {
		n++
	}
	for n < len(s) && (s[n] >= 'a' && s[n] <= 'z' || s[n] == '%') {
		n++
	}
	return n
}

func runeLen(s string) int {
	_, size := utf8.DecodeRuneInString(s)
	return max(size, 1)
}

func wordBefore(s string, i int) bool {
	if i == 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func wordAfter(s string, i int) bool {
	r, _ := utf8.DecodeRuneInString(s[i:])
	return i < len(s) && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

func isURLEnd(r rune) bool {
	return unicode.IsSpace(r) || r == ')' || r == ']' || r == '>'
}
