package tinymist

import "testing"

// 换算错误会把补全插到错误位置，直接改坏笔记。
func TestPositionConversion(t *testing.T) {
	text := "ab\n中文😀x\n\n"
	cases := []struct {
		offset int
		pos    Position
	}{
		{0, Position{0, 0}},
		{2, Position{0, 2}},
		{3, Position{1, 0}},
		{6, Position{1, 1}},  // 中 之后
		{9, Position{1, 2}},  // 文 之后
		{13, Position{1, 4}}, // 😀 占两个 UTF-16 单元
		{14, Position{1, 5}}, // 行尾
		{15, Position{2, 0}}, // 空行
		{16, Position{3, 0}}, // 文本末尾
	}
	for _, tc := range cases {
		got, err := toPosition(text, tc.offset)
		if err != nil || got != tc.pos {
			t.Errorf("toPosition(%d) = %v, %v，期望 %v", tc.offset, got, err, tc.pos)
		}
		off, err := toOffset(text, tc.pos)
		if err != nil || off != tc.offset {
			t.Errorf("toOffset(%v) = %d, %v，期望 %d", tc.pos, off, err, tc.offset)
		}
	}
	for _, p := range []Position{{1, 3}, {0, 3}, {4, 0}, {-1, 0}} {
		if off, err := toOffset(text, p); err == nil {
			t.Errorf("toOffset(%v) = %d，期望报错", p, off)
		}
	}
	for _, off := range []int{4, -1, 17} { // 4 落在“中”的中间
		if p, err := toPosition(text, off); err == nil {
			t.Errorf("toPosition(%d) = %v，期望报错", off, p)
		}
	}
}
