package gui

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/typst"
)

const (
	// watchDelay 是常驻进程模式下的防抖时间，增量编译很快，可以比一次性编译短得多。
	watchDelay = 150 * time.Millisecond
	// watchTimeout 是写入快照后等待编译结束的上限，首次编译要加载字体，留得宽一些。
	watchTimeout = 30 * time.Second
	// maxWatchFails 次失败后，这篇笔记退回为每次调用一次 typst compile。
	maxWatchFails = 2
	// busyHint 是编译超过多久才显示进度，避免每次按键都闪一下。
	busyHint = 300 * time.Millisecond
)

// watchJob 是交给常驻进程的一份编辑快照。
type watchJob struct {
	source  string
	version uint64
	tone    pageTone
}

// watchHandle 连接主线程与一篇笔记的后台编译循环。jobs 只保留最新一份快照。
type watchHandle struct {
	jobs   chan watchJob
	cancel context.CancelFunc
}

// sendWatch 把当前快照交给常驻进程，没有进程时先启动。
func (a *App) sendWatch(s *session, now time.Time) {
	job := watchJob{source: s.text(), version: s.observed, tone: a.pageTone()}
	s.previewPending = false
	s.previewEdit = time.Time{}
	s.sentVersion = job.version
	if !s.watchWaiting {
		s.watchWaiting = true
		s.watchSent = now
	}
	if s.watch == nil {
		ctx, cancel := context.WithCancel(a.ctx)
		h := &watchHandle{jobs: make(chan watchJob, 1), cancel: cancel}
		s.watch = h
		req := typst.Request{Root: a.vault.Root(), LogicalPath: s.id, Source: job.source, PPI: a.state.Appearance.PreviewPPI}
		go a.runWatch(ctx, s, h, req, job)
		return
	}
	select {
	case <-s.watch.jobs:
	default:
	}
	s.watch.jobs <- job
}

func (a *App) stopWatch(s *session) {
	if s.watch != nil {
		s.watch.cancel()
		s.watch = nil
	}
	s.watchWaiting = false
}

type watchOutcome struct {
	result typst.WatchResult
	err    error
}

// runWatch 是一篇笔记的后台编译循环：同一时间只有一份快照在等待结果，期间的新快照留到结果之后再写入。
// 页面位图在这里解码，未变化的页面沿用上一次的位图。
func (a *App) runWatch(ctx context.Context, s *session, h *watchHandle, req typst.Request, first watchJob) {
	w, err := typst.StartWatch(ctx, req)
	if err != nil {
		a.publish(func() { a.watchFailed(s, h, err) })
		return
	}
	defer w.Close()
	outcomes := make(chan watchOutcome)
	go func() {
		for {
			r, err := w.Next(ctx)
			select {
			case outcomes <- watchOutcome{r, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	loop := watchLoop{written: first.version, tone: first.tone, waiting: true, timeout: time.After(watchTimeout)}
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-h.jobs:
			loop.queue(job)
		case out := <-outcomes:
			if out.err != nil {
				a.publish(func() { a.watchFailed(s, h, out.err) })
				return
			}
			res := loop.finish(out.result)
			a.publish(func() { a.finishWatch(s, h, res) })
		case <-loop.timeout:
			a.publish(func() { a.watchFailed(s, h, errors.New("Typst 预览进程没有响应")) })
			return
		}
		if err := loop.flush(w); err != nil {
			a.publish(func() { a.watchFailed(s, h, err) })
			return
		}
		if loop.republish {
			res := loop.last
			loop.republish = false
			a.publish(func() { a.finishWatch(s, h, res) })
		}
	}
}

// watchLoop 是 runWatch 的状态，只在后台循环所在的 goroutine 中使用。
type watchLoop struct {
	written   uint64
	waiting   bool
	pending   *watchJob
	timeout   <-chan time.Time
	pages     []*ui.Bitmap
	data      [][]byte
	pageTone  pageTone // pages 解码时用的配色
	tone      pageTone // 最近写入的快照所处的配色
	last      previewResult
	republish bool
}

func (l *watchLoop) queue(job watchJob) {
	l.pending = &job
}

// flush 在没有等待中的编译时写入排队的快照。内容未变时不会触发编译，直接把上一次结果标为这个版本。
func (l *watchLoop) flush(w *typst.Watch) error {
	if l.waiting || l.pending == nil {
		return nil
	}
	job := *l.pending
	l.pending = nil
	l.tone = job.tone
	changed, err := w.Write(job.source)
	if err != nil {
		return err
	}
	if !changed {
		l.written = job.version
		l.last.version = job.version
		l.republish = true
		return nil
	}
	l.written, l.waiting = job.version, true
	l.timeout = time.After(watchTimeout)
	return nil
}

// finish 把一次编译结果合并进当前页面。依赖文件变化引起的编译同样归到最近写入的版本。
func (l *watchLoop) finish(r typst.WatchResult) previewResult {
	l.waiting, l.timeout = false, nil
	res := previewResult{version: l.written, pages: l.pages, data: l.data, tone: l.pageTone, dur: r.Duration, diag: r.Diagnostics, err: r.Err}
	if r.Err == nil {
		old := l.pages
		if l.pageTone != l.tone {
			// 主题变了，旧位图的配色不能再沿用，全部按原始字节重新解码。
			old = nil
		}
		pages, data, err := mergePages(old, l.data, r.Pages, l.tone)
		if err != nil {
			res.err = err
		} else {
			l.pages, l.data, l.pageTone = pages, data, l.tone
			res.pages, res.data, res.tone = pages, data, l.tone
		}
	}
	l.last = res
	return res
}

// mergePages 只解码有新数据的页面，其余沿用旧位图；没有可沿用的位图时用上一次的原始字节重新解码。
func mergePages(old []*ui.Bitmap, oldData, files [][]byte, tone pageTone) ([]*ui.Bitmap, [][]byte, error) {
	pages := make([]*ui.Bitmap, len(files))
	data := make([][]byte, len(files))
	for i, file := range files {
		if file == nil && i < len(oldData) {
			file = oldData[i]
			if i < len(old) {
				pages[i], data[i] = old[i], file
				continue
			}
		}
		if file == nil {
			return nil, nil, errors.New("Typst 预览缺少页面")
		}
		page, err := decodePage(file, tone)
		if err != nil {
			return nil, nil, err
		}
		pages[i], data[i] = page, file
	}
	return pages, data, nil
}

// finishWatch 在主线程应用编译结果，只接受当前笔记、当前进程和当前源版本的结果。
func (a *App) finishWatch(s *session, h *watchHandle, res previewResult) {
	if a.current != s || s.watch != h || res.version != s.ed.Buffer.Version() {
		return
	}
	s.renderAttempted, s.attemptedVersion = true, res.version
	if res.version >= s.sentVersion {
		s.watchWaiting = false
	}
	s.watchFails = 0
	s.previewDur = res.dur
	s.previewMissing = false
	if res.err != nil {
		s.previewErr = res.err.Error()
		return
	}
	s.setPreviewPages(res)
	s.previewErr = ""
	s.previewDiag = res.diag
}

// watchFailed 结束出错的进程。缺少编译器时不再重试；其他错误重试一次，再失败就退回一次性编译。
func (a *App) watchFailed(s *session, h *watchHandle, err error) {
	if s.watch != h {
		return
	}
	a.stopWatch(s)
	if errors.Is(err, context.Canceled) {
		return
	}
	s.watchFails++
	if errors.Is(err, typst.ErrUnavailable) {
		s.watchFails = maxWatchFails
		s.previewErr = err.Error()
		s.previewMissing = true
		s.renderAttempted, s.attemptedVersion = true, s.observed
	}
}

// previewBusy 报告是否显示编译进度：常驻进程模式下等待超过 busyHint 才显示。
func previewBusy(c *ui.Context, s *session) bool {
	if s.activeRender != 0 {
		return true
	}
	if !s.watchWaiting {
		return false
	}
	if wait := busyHint - c.Now().Sub(s.watchSent); wait > 0 {
		c.After(wait)
		return false
	}
	return true
}
