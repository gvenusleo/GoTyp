package tinymist

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// errPosition 表示坐标与文本快照对不上。调用方丢弃整次结果，不猜测位置。
var errPosition = errors.New("坐标超出文本范围")

// Position 是 LSP 的 UTF-16 行列坐标，行和列都从 0 开始。
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// toPosition 把 UTF-8 字节偏移换算为 UTF-16 行列。编辑器的文本只用 \n 换行。
func toPosition(text string, offset int) (Position, error) {
	if offset < 0 || offset > len(text) || (offset < len(text) && !utf8.RuneStart(text[offset])) {
		return Position{}, errPosition
	}
	before := text[:offset]
	lineStart := strings.LastIndexByte(before, '\n') + 1
	p := Position{Line: strings.Count(before, "\n")}
	for _, r := range before[lineStart:] {
		p.Character += utf16Len(r)
	}
	return p, nil
}

// toOffset 把 UTF-16 行列换算为字节偏移。落在代理对中间或超出行尾都视为错误。
func toOffset(text string, p Position) (int, error) {
	if p.Line < 0 || p.Character < 0 {
		return 0, errPosition
	}
	start := 0
	for range p.Line {
		i := strings.IndexByte(text[start:], '\n')
		if i < 0 {
			return 0, errPosition
		}
		start += i + 1
	}
	line := text[start:]
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	units := 0
	for i, r := range line {
		if units == p.Character {
			return start + i, nil
		}
		units += utf16Len(r)
		if units > p.Character {
			return 0, errPosition
		}
	}
	if units == p.Character {
		return start + len(line), nil
	}
	return 0, errPosition
}

func toRange(text string, r lspRange) (int, int, error) {
	start, err := toOffset(text, r.Start)
	if err != nil {
		return 0, 0, err
	}
	end, err := toOffset(text, r.End)
	if err != nil || end < start {
		return 0, 0, errPosition
	}
	return start, end, nil
}

func utf16Len(r rune) int {
	if r >= 0x10000 && r <= utf8.MaxRune {
		return 2
	}
	return 1
}
