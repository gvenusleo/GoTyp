package pdfpreview

import (
	"context"
	"image"
	"runtime"
	"slices"
	"sync"
)

// PDFium 的句柄只由后台任务使用，所有原生调用在进程内串行执行。
var pdfiumMu sync.Mutex

var resourceEngine = sync.OnceValues(func() (*engine, error) {
	path, err := resourceLibrary()
	if err != nil {
		return nil, err
	}
	return loadEngine(path)
})

type rasterKey struct {
	document uint64
	page     int
	size     image.Point
	tile     image.Rectangle
	metadata int
}

func (key rasterKey) region() image.Rectangle {
	if key.tile.Empty() {
		return image.Rectangle{Max: key.size}
	}
	return key.tile
}

type loadJob struct {
	ctx              context.Context
	id               uint64
	data             []byte
	anchor           int
	width, zoom, gap float32
	accept           func(error) bool
}

type copyJob struct {
	ctx      context.Context
	document uint64
	selected textRange
	apply    func(string)
}

type worker struct {
	mu         sync.Mutex
	wake       chan struct{}
	stop       chan struct{}
	done       chan struct{}
	load       *loadJob
	copy       *copyJob
	clear      bool
	pages      []rasterKey
	inFlight   rasterKey
	cancelPage context.CancelFunc
	getEngine  func() (*engine, error)
}

func (w *worker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *worker) submitLoad(job *loadJob) {
	w.mu.Lock()
	w.load = job
	w.mu.Unlock()
	w.signal()
}

func (w *worker) submitPages(pages []rasterKey) {
	w.mu.Lock()
	w.pages = slices.Clone(pages)
	if w.cancelPage != nil && !slices.Contains(pages, w.inFlight) {
		w.cancelPage()
	}
	w.mu.Unlock()
	w.signal()
}

func (w *worker) submitCopy(job copyJob) {
	w.mu.Lock()
	w.copy = &job
	w.mu.Unlock()
	w.signal()
}

func (w *worker) reset() {
	w.mu.Lock()
	w.clear = true
	w.load, w.copy, w.pages = nil, nil, nil
	if w.cancelPage != nil {
		w.cancelPage()
	}
	w.mu.Unlock()
	w.signal()
}

func (w *worker) next() (load *loadJob, copy *copyJob, clear bool, page rasterKey, ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case w.clear:
		w.clear = false
		clear = true
	case w.load != nil:
		load, w.load = w.load, nil
	case w.copy != nil:
		copy, w.copy = w.copy, nil
	case len(w.pages) > 0:
		page, w.pages = w.pages[0], w.pages[1:]
		w.inFlight = page
		ctx, w.cancelPage = context.WithCancel(context.Background())
	}
	return
}

func (w *worker) finishPage(key rasterKey, delivered bool) {
	w.mu.Lock()
	w.cancelPage()
	w.cancelPage = nil
	if delivered {
		w.pages = slices.DeleteFunc(w.pages, func(p rasterKey) bool { return p == key })
	}
	w.mu.Unlock()
}

func (v *Viewer) run() {
	w := v.worker
	defer close(w.done)
	var e *engine
	var doc *document
	defer func() { closeDocument(e, doc) }()
	for {
		select {
		case <-w.stop:
			return
		default:
		}
		load, copy, clear, page, ctx := w.next()
		switch {
		case clear:
			closeDocument(e, doc)
			doc = nil
		case load != nil:
			e, doc = v.replaceDocument(e, doc, load)
		case copy != nil:
			v.readSelection(e, doc, copy)
		case ctx != nil:
			v.renderPage(e, doc, page, ctx)
		default:
			select {
			case <-w.stop:
				return
			case <-w.wake:
			}
		}
	}
}

func (v *Viewer) replaceDocument(e *engine, old *document, job *loadJob) (*engine, *document) {
	if job.ctx.Err() != nil {
		return e, old
	}
	doc, frame, loadedEngine, err := v.worker.prepare(e, job)
	for {
		reply := v.publishLoad(job, doc, frame, err)
		if reply.next.document != 0 {
			pdfiumMu.Lock()
			frame, err = loadedEngine.render(job.ctx, doc, reply.next)
			pdfiumMu.Unlock()
			continue
		}
		if reply.installed {
			closeDocument(loadedEngine, old)
			return loadedEngine, doc
		}
		closeDocument(loadedEngine, doc)
		return loadedEngine, old
	}
}

type loadReply struct {
	installed bool
	next      rasterKey
}

func (v *Viewer) publishLoad(job *loadJob, doc *document, frame *pageFrame, err error) loadReply {
	var reply loadReply
	if !v.deliver(func() bool {
		if job.id != v.loadID || job.ctx.Err() != nil {
			return false
		}
		// 滚动过程中可能已经离开最初请求的页，提交前补渲染当前页。
		if err == nil {
			page := min(v.anchor().page, len(doc.pages)-1)
			if page != frame.key.page {
				layout := newLayout(doc.pages, v.width, v.zoom, v.gap)
				reply.next = layout.preview(doc.id, page, v.zoom)
				return true
			}
		}
		reply.installed = v.finishLoad(job, doc, frame, err)
		return true
	}) {
		return loadReply{}
	}
	return reply
}

func (w *worker) prepare(e *engine, job *loadJob) (*document, *pageFrame, *engine, error) {
	pdfiumMu.Lock()
	defer pdfiumMu.Unlock()
	if e == nil {
		var err error
		e, err = w.getEngine()
		if err != nil {
			return nil, nil, nil, err
		}
	}
	// 获取 PDFium 的最后错误时必须与 LoadMemDocument 在同一线程。
	runtime.LockOSThread()
	doc, err := e.open(job.ctx, job.id, job.data)
	runtime.UnlockOSThread()
	if err != nil {
		return nil, nil, e, err
	}
	page := min(job.anchor, len(doc.pages)-1)
	layout := newLayout(doc.pages, job.width, job.zoom, job.gap)
	frame, err := e.render(job.ctx, doc, layout.preview(doc.id, page, job.zoom))
	return doc, frame, e, err
}

func (v *Viewer) renderPage(e *engine, doc *document, key rasterKey, ctx context.Context) {
	delivered := false
	if doc != nil && doc.id == key.document && ctx.Err() == nil {
		pdfiumMu.Lock()
		frame, err := e.render(ctx, doc, key)
		pdfiumMu.Unlock()
		if ctx.Err() == nil {
			delivered = v.deliver(func() bool { return v.finishPage(key, frame, err) })
		}
	}
	v.worker.finishPage(key, delivered)
}

func (v *Viewer) readSelection(e *engine, doc *document, job *copyJob) {
	if doc == nil || doc.id != job.document {
		return
	}
	pdfiumMu.Lock()
	text := e.copyText(job.ctx, doc, job.selected)
	pdfiumMu.Unlock()
	if text != "" {
		v.deliver(func() bool {
			if v.document != job.document || !v.hasSelection || v.selection != job.selected {
				return false
			}
			job.apply(text)
			return true
		})
	}
}

func closeDocument(e *engine, doc *document) {
	if doc != nil {
		pdfiumMu.Lock()
		e.close(doc)
		pdfiumMu.Unlock()
	}
}

// 等待主线程确认接收后再开始下一份结果，避免位图堆积在窗口队列中。
func (v *Viewer) deliver(apply func() bool) bool {
	accepted := make(chan bool, 1)
	v.publish(func() {
		accepted <- (!v.closed && apply())
	})
	select {
	case ok := <-accepted:
		return ok
	case <-v.worker.stop:
		return false
	}
}
