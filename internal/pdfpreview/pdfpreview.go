// Package pdfpreview 在 MyGo 原生界面中显示 PDFium 渲染的页面。
package pdfpreview

import (
	"container/list"
	"context"
	"slices"

	"github.com/egoist/mygo/ui"
)

const (
	bitmapBudget = 24 << 20
	tileBudget   = 40 << 20
	pageMetadata = 1 << 20
)

type cachedPage struct {
	frame   *pageFrame
	failed  rasterKey
	err     error
	recency *list.Element
}

// Viewer 的界面状态只在主线程读写。后台结果经 publish 回到主线程。
type Viewer struct {
	publish    func(func())
	worker     *worker
	closed     bool
	loadID     uint64
	cancelLoad context.CancelFunc
	document   uint64
	docContext context.Context
	cancelDoc  context.CancelFunc
	pages      []pageSize
	wanted     []rasterKey
	cache      map[int]cachedPage
	recency    list.List
	cacheSize  int
	tiles      map[rasterKey]cachedPage
	tileKeys   []rasterKey
	scale      float64

	scroll        ui.ScrollState
	layout        pageLayout
	width         float32
	height        float32
	windowWidth   float32
	windowHeight  float32
	layoutKey     any
	gap           float32
	zoom          float32
	measured      bool
	waitingBounds bool
	selection     textRange
	pointer       pointerDrag
	hasSelection  bool
}

// NewViewer 启动窗口的后台任务。第一次载入文档时才加载 PDFium。
func NewViewer(publish func(func())) *Viewer {
	return newViewer(publish, resourceEngine)
}

func newViewer(publish func(func()), getEngine func() (*engine, error)) *Viewer {
	v := &Viewer{
		publish: publish, zoom: 1, width: 640, scale: 1,
		cache: make(map[int]cachedPage), tiles: make(map[rasterKey]cachedPage),
		worker: &worker{
			wake: make(chan struct{}, 1), stop: make(chan struct{}),
			done: make(chan struct{}), getEngine: getEngine,
		},
	}
	go v.run()
	return v
}

// SetScale 更新显示器每 DIP 对应的像素数，由窗口事件调用。
func (v *Viewer) SetScale(scale float64) bool {
	scale = max(1, min(4, scale))
	if v.scale == scale {
		return false
	}
	v.scale = scale
	return true
}

// Load 接收调用者转交的不可变 PDF 字节。accept 在主线程再次检查源版本。
func (v *Viewer) Load(ctx context.Context, data []byte, accept func(error) bool) {
	if v.closed || ctx.Err() != nil {
		return
	}
	if v.cancelLoad != nil {
		v.cancelLoad()
	}
	ctx, v.cancelLoad = context.WithCancel(ctx)
	v.loadID++
	v.worker.submitLoad(&loadJob{
		ctx: ctx, id: v.loadID, data: data, accept: accept,
		anchor: v.anchor().page, width: v.width, zoom: v.zoom, gap: v.gap,
	})
}

func (v *Viewer) finishLoad(job *loadJob, doc *document, frame *pageFrame, err error) bool {
	if job.id != v.loadID || job.ctx.Err() != nil {
		return false
	}
	accepted := job.accept(err)
	v.cancelLoad()
	v.cancelLoad = nil
	if err != nil || !accepted {
		return false
	}
	anchor := v.anchor()
	if v.cancelDoc != nil {
		v.cancelDoc()
	}
	v.docContext, v.cancelDoc = context.WithCancel(context.Background())
	v.document, v.pages = doc.id, doc.pages
	v.hasSelection, v.pointer = false, pointerDrag{}
	v.layout = newLayout(v.pages, v.width, v.zoom, v.gap)
	v.scroll.Y = max(0, min(v.layout.offset(anchor), v.layout.height-v.height))
	v.scroll.X = min(v.scroll.X, max(0, v.layout.width-v.width))
	v.setWanted(nil)
	v.clearTiles()
	// 只发布新文档的图像，避免旧画面和新文字、链接混用。
	for page := range v.cache {
		if page != frame.key.page {
			v.removePage(page)
		}
	}
	v.cachePage(frame)
	return true
}

// Clear 取消待显示的文档并释放当前 PDF。
func (v *Viewer) Clear() {
	if v.closed {
		return
	}
	if v.cancelLoad != nil {
		v.cancelLoad()
		v.cancelLoad = nil
	}
	if v.cancelDoc != nil {
		v.cancelDoc()
		v.docContext, v.cancelDoc = nil, nil
	}
	v.loadID++
	v.document, v.pages, v.wanted = 0, nil, nil
	v.layout, v.zoom = pageLayout{}, 1
	v.scroll.X, v.scroll.Y = 0, 0
	v.hasSelection, v.pointer = false, pointerDrag{}
	clear(v.cache)
	v.clearTiles()
	v.recency.Init()
	v.cacheSize = 0
	v.worker.reset()
}

// Close 取消后台任务，立即在主线程返回。
func (v *Viewer) Close() {
	if !v.closed {
		v.Clear()
		v.closed = true
		close(v.worker.stop)
	}
}

// Done 在 PDFium 文档和固定内存释放后关闭。
func (v *Viewer) Done() <-chan struct{} { return v.worker.done }

func (v *Viewer) setWanted(keys []rasterKey) {
	if !slices.Equal(keys, v.wanted) {
		v.wanted = keys
		v.worker.submitPages(keys)
	}
}

func (v *Viewer) clearTiles() {
	clear(v.tiles)
	v.tileKeys = nil
}

func (v *Viewer) finishPage(key rasterKey, frame *pageFrame, err error) bool {
	if v.document != key.document || !slices.Contains(v.wanted, key) {
		return false
	}
	if !key.tile.Empty() {
		entry := v.tiles[key]
		if err == nil {
			frame.installBitmap(entry.frame)
			entry = cachedPage{frame: frame}
		} else {
			entry.failed = key
		}
		v.tiles[key] = entry
		return true
	}
	if err == nil {
		v.cachePage(frame)
	} else {
		entry := v.cache[key.page]
		entry.failed, entry.err = key, err
		v.cache[key.page] = entry
	}
	return true
}

func (v *Viewer) cachePage(frame *pageFrame) {
	page := frame.key.page
	frame.installBitmap(v.cache[page].frame)
	v.removePage(page)
	v.cache[page] = cachedPage{frame: frame, recency: v.recency.PushFront(page)}
	v.cacheSize += frame.frameBytes()
	for v.cacheSize > bitmapBudget {
		v.removePage(v.recency.Back().Value.(int))
	}
}

func (v *Viewer) removePage(page int) {
	entry := v.cache[page]
	delete(v.cache, page)
	if entry.frame != nil {
		v.cacheSize -= entry.frame.frameBytes()
		v.recency.Remove(entry.recency)
	}
}

func (f *pageFrame) installBitmap(old *pageFrame) {
	if old != nil && old.key.region().Size() == f.key.region().Size() {
		// 复用纹理标识，连续更新 PDF 时直接上传新像素。
		old.bitmap.Update(f.pixels)
		f.bitmap = old.bitmap
	} else {
		f.bitmap = ui.NewBitmap(f.pixels)
	}
	f.pixels = nil
}

func (f *pageFrame) frameBytes() int {
	size := f.key.size.X*f.key.size.Y*4 + len(f.chars)*16
	for _, link := range f.links {
		size += link.bytes()
	}
	return size
}
