package gui

import (
	"context"
	"hash/maphash"

	"github.com/egoist/mygo/ui"
	"github.com/gvenusleo/mygo-pdf/pdf"
)

type pdfOutlineState struct {
	tree                     ui.OutlineState[int]
	roots                    []int
	keys                     map[pdfOutlineKey]int
	nextID                   int
	selection                int
	displayed, load, request uint64
	page, currentRow         int
	jumpedID                 int // 手动跳转的书签身份，零表示只有页码可用。
	lastHeading              int // 上次显示时用于定位的书签身份。
	cancel                   context.CancelFunc
	result                   pdf.OutlineResult
	rows                     []pdfOutlineRow
	indexes                  map[int]int
}

type pdfOutlineKey struct {
	parent   int
	title    string
	page     int // 零表示只按标题匹配，正数是叶子的页码加一，负数表示分支。
	children uint64
}

var outlineTitleSeed = maphash.MakeSeed()

type pdfOutlineRow struct {
	entry    pdf.OutlineEntry
	id       int
	parent   int
	end      int
	children []int
}

func (p *pdfOutlineState) cancelRead() {
	if p.cancel != nil {
		p.cancel()
	}
	p.cancel, p.request = nil, 0
}

func (preview *pdfPreview) updateOutline(ctx context.Context, state pdf.State) {
	p := &preview.outline
	if p.load != state.RequestGeneration {
		// 新的 PDF 加载会取消组件中尚未完成的大纲请求。
		p.cancelRead()
		p.load = state.RequestGeneration
	}
	p.displayed = state.DisplayedGeneration
	if state.Page != p.page {
		p.page = state.Page
		if p.result.DisplayedGeneration == p.displayed {
			p.updateCurrentRow()
		}
	}
	preview.readOutline(ctx, state)
}

func (preview *pdfPreview) readOutline(ctx context.Context, state pdf.State) {
	p := &preview.outline
	if preview.Viewer == nil || state.Loading || state.DisplayedGeneration == 0 || p.request != 0 ||
		p.result.DisplayedGeneration == state.DisplayedGeneration {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	generation := state.DisplayedGeneration
	request := preview.ReadOutline(ctx, func(result pdf.OutlineResult) {
		if ctx.Err() != nil || p.request != result.RequestGeneration ||
			generation != result.DisplayedGeneration || generation != preview.State().DisplayedGeneration {
			return
		}
		p.cancelRead()
		p.setResult(result)
	})
	if request == 0 {
		cancel()
		return
	}
	p.request, p.cancel = request, cancel
}

func (p *pdfOutlineState) updateCurrentRow() {
	// 下一份 PDF 可能沿用同一页内的阅读位置，优先找回已跳转的书签。
	if index, ok := p.indexes[p.jumpedID]; ok && p.rows[index].entry.Page == p.page {
		p.currentRow = index
		return
	}
	p.jumpedID = 0
	p.currentRow = -1
	bestPage := 0
	for i, row := range p.rows {
		page := row.entry.Page
		if page > bestPage && page <= p.page {
			bestPage, p.currentRow = page, i
		}
	}
}

func (p *pdfOutlineState) setResult(result pdf.OutlineResult) {
	p.result = result
	previous, oldIndexes, oldRows := p.keys, p.indexes, p.rows
	p.keys, p.indexes = make(map[pdfOutlineKey]int), make(map[int]int)
	p.rows = nil
	p.roots = p.indexEntries(result.Entries, -1, 0, previous)
	for _, row := range p.rows {
		oldIndex, existed := oldIndexes[row.id]
		if len(row.children) == 0 {
			p.tree.Open.Remove(row.id)
		} else if !existed || len(oldRows[oldIndex].children) == 0 {
			p.tree.Open.Add(row.id)
		}
	}
	for id := range oldIndexes {
		if _, exists := p.indexes[id]; !exists {
			p.tree.Open.Remove(id)
		}
	}
	p.tree.Generation++
	p.updateCurrentRow()
}

func (p *pdfOutlineState) indexEntries(entries []pdf.OutlineEntry, parent, parentID int, previous map[pdfOutlineKey]int) []int {
	if len(entries) == 0 {
		return nil
	}
	rows := make([]int, 0, len(entries))
	keys := make([]pdfOutlineKey, len(entries))
	counts := make(map[pdfOutlineKey]int)
	for i, entry := range entries {
		keys[i] = outlineEntryKey(parentID, entry)
		counts[pdfOutlineKey{parent: parentID, title: entry.Title}]++
		counts[keys[i]]++
	}
	for i, entry := range entries {
		title := pdfOutlineKey{parent: parentID, title: entry.Title}
		detail := keys[i]
		id := 0
		if counts[title] == 1 {
			id = previous[title]
		}
		if id == 0 && counts[detail] == 1 {
			id = previous[detail]
		}
		if id == 0 {
			p.nextID++
			id = p.nextID
		}
		if counts[title] == 1 {
			p.keys[title] = id
		}
		if counts[detail] == 1 {
			p.keys[detail] = id
		}
		index := len(p.rows)
		p.rows = append(p.rows, pdfOutlineRow{entry: entry, id: id, parent: parent})
		p.indexes[id] = index
		children := p.indexEntries(entry.Children, index, id, previous)
		p.rows[index].children = children
		p.rows[index].end = len(p.rows)
		rows = append(rows, id)
	}
	return rows
}

func outlineEntryKey(parent int, entry pdf.OutlineEntry) pdfOutlineKey {
	if len(entry.Children) == 0 {
		return pdfOutlineKey{parent: parent, title: entry.Title, page: entry.Page + 1}
	}
	// 子标题能区分同名分支，也不会因前面插页而改变。
	var titles maphash.Hash
	titles.SetSeed(outlineTitleSeed)
	maphash.WriteComparable(&titles, len(entry.Children))
	for _, child := range entry.Children {
		maphash.WriteComparable(&titles, child.Title)
	}
	return pdfOutlineKey{parent: parent, title: entry.Title, page: -1, children: titles.Sum64()}
}

func (p *pdfOutlineState) visibleCurrentRow() int {
	current := p.currentRow
	for index := current; index >= 0; {
		index = p.rows[index].parent
		if index >= 0 && !p.tree.Open.Has(p.rows[index].id) {
			current = index
		}
	}
	return current
}

// visibleRowIndex 跳过折叠分支的后代，换算为 Outline 的可见行号。
func (p *pdfOutlineState) visibleRowIndex(index int) int {
	visible := 0
	for i := 0; i < index; visible++ {
		row := p.rows[i]
		if len(row.children) > 0 && !p.tree.Open.Has(row.id) {
			i = row.end
		} else {
			i++
		}
	}
	return visible
}

func (a *App) jumpPDFOutline(preview *pdfPreview, index int) {
	if a.activePDF() != preview {
		return
	}
	p := &preview.outline
	entry := p.rows[index].entry
	if entry.Page == 0 {
		return
	}
	if !preview.GoToOutline(entry) {
		a.notify("无法跳到 PDF 书签")
		return
	}
	p.jumpedID = p.rows[index].id
	p.currentRow = index
}
