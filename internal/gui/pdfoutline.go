package gui

import (
	"context"

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
	cancel                   context.CancelFunc
	result                   pdf.OutlineResult
	rows                     []pdfOutlineRow
	indexes                  map[int]int
}

type pdfOutlineKey struct {
	parent, occurrence int
	title              string
}

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

func (a *App) updatePDFOutline(state pdf.State) {
	p := &a.pdfOutline
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
	a.readPDFOutline(state)
}

func (a *App) readPDFOutline(state pdf.State) {
	p := &a.pdfOutline
	if a.preview == nil || state.Loading || state.DisplayedGeneration == 0 || p.request != 0 ||
		p.result.DisplayedGeneration == state.DisplayedGeneration {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	tab, generation := a.activeTab, state.DisplayedGeneration
	request := a.preview.ReadOutline(ctx, func(result pdf.OutlineResult) {
		if ctx.Err() != nil || a.activeTab != tab || p.request != result.RequestGeneration ||
			generation != result.DisplayedGeneration || generation != a.preview.State().DisplayedGeneration {
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
	for key, id := range previous {
		if _, exists := p.keys[key]; !exists {
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
	occurrences := make(map[string]int)
	for _, entry := range entries {
		key := pdfOutlineKey{parent: parentID, title: entry.Title, occurrence: occurrences[entry.Title]}
		occurrences[entry.Title]++
		id := previous[key]
		if id == 0 {
			p.nextID++
			id = p.nextID
		}
		p.keys[key] = id
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

func (a *App) jumpPDFOutline(index int) {
	p := &a.pdfOutline
	entry := p.rows[index].entry
	if entry.Page == 0 {
		return
	}
	if !a.preview.GoToOutline(entry) {
		a.notify("无法跳到 PDF 书签")
		return
	}
	p.jumpedID = p.rows[index].id
	p.currentRow = index
}
