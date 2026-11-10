package gui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gotyp/internal/tinymist"
	"gotyp/internal/vault"
)

const (
	// syncDelay 是编辑停顿多久后把全文发给 tinymist。增量编译很快，太长会让预览显得迟钝。
	syncDelay = 150 * time.Millisecond
	// previewTimeout 是启动预览任务的上限，首次编译要加载字体和包，留得宽一些。
	previewTimeout = 30 * time.Second
)

// previewPage 是 Typst 预览区的 WebView。allowed 是当前预览页的地址，导航回调可能不在主线程，
// 所以用原子值传递。
type previewPage struct {
	web     *mygo.WebView
	allowed atomic.Value // string
}

// newPreviewPage 为窗口创建预览 WebView。预览页只显示 tinymist 渲染的结果，
// 离开预览地址的导航一律取消，网页链接交给系统浏览器。
func newPreviewPage(win *mygo.Window) (*previewPage, error) {
	web, err := win.NewWebView(mygo.WebViewOptions{})
	if err != nil {
		return nil, err
	}
	p := &previewPage{web: web}
	p.allowed.Store("")
	page := web.Page()
	page.OnWillNavigate(func(e *mygo.NavigateEvent) {
		if base := p.allowed.Load().(string); base != "" && strings.HasPrefix(e.URL, base) {
			return
		}
		e.PreventDefault()
		openExternal(e.URL)
	})
	page.SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
		openExternal(req.URL)
		return nil
	})
	return p, nil
}

func openExternal(raw string) {
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	switch u.Scheme {
	case "http", "https", "mailto":
		if ui.SafeToOpen(raw) {
			mygo.Shell.OpenExternal(raw)
		}
	}
}

// load 显示 rawURL，空串清空页面，避免切换笔记后短暂露出上一篇的内容。
func (p *previewPage) load(rawURL string) {
	p.allowed.Store(rawURL)
	if rawURL == "" {
		rawURL = "about:blank"
	}
	p.web.Page().LoadURL(rawURL)
}

// previewChanged 在 buffer 版本变化后调用：安排同步，并允许失败的预览在新编辑后重试。
func (a *App) previewChanged(s *session) {
	if a.current != s || s.lspPath == "" {
		return
	}
	s.syncDue = time.Time{}
	if !s.previewMissing {
		s.previewErr = ""
	}
}

// schedulePreview 每帧调用：编辑停顿后同步文本；预览可见时确保有一个配色正确的预览任务。
func (a *App) schedulePreview(c *ui.Context, s *session) {
	if a.current != s || s.lspPath == "" || a.lsp == nil {
		return
	}
	if s.syncedVersion != s.observed {
		if s.syncDue.IsZero() {
			s.syncDue = c.Now().Add(syncDelay)
		}
		if wait := s.syncDue.Sub(c.Now()); wait > 0 {
			c.After(wait)
		} else {
			a.syncLSP(s)
		}
	}
	if a.mode == modeEdit || a.preview == nil {
		return
	}
	invert := a.previewInverted()
	if s.previewTask != "" && s.previewInvert != invert {
		a.stopPreview(s)
	}
	if s.previewTask == "" && !s.previewStarting && s.previewErr == "" {
		a.startPreview(s, invert)
	}
	a.syncPreviewScroll(c, s)
}

// previewInverted 报告预览是否需要反转颜色：只在深色主题下打开了调色时反转。
func (a *App) previewInverted() bool {
	return a.state.Appearance.RecolorPreview && a.themes.app != nil && a.themes.app.Dark
}

// syncLSP 把当前快照交给 tinymist，诊断和预览随之更新。
func (a *App) syncLSP(s *session) {
	client := a.lsp
	doc := s.lspDocument()
	s.syncedVersion, s.syncedText, s.syncDue = doc.Version, doc.Text, time.Time{}
	a.lspQueue.run(func() { client.Open(doc) })
}

// startPreview 在 tinymist 的后台队列中启动预览任务，与文档的打开和关闭保持顺序。
func (a *App) startPreview(s *session, invert bool) {
	client := a.lsp
	a.previewSeq++
	id := fmt.Sprintf("gotyp-%d", a.previewSeq)
	s.previewGen++
	gen := s.previewGen
	s.previewStarting, s.previewInvert = true, invert
	doc := s.lspDocument()
	s.syncedVersion, s.syncedText, s.syncDue = doc.Version, doc.Text, time.Time{}
	a.lspQueue.run(func() {
		ctx, cancel := context.WithTimeout(a.ctx, previewTimeout)
		p, err := client.StartPreview(ctx, doc, id, invert)
		cancel()
		a.publish(func() { a.finishPreview(s, client, gen, p, err) })
	})
}

// finishPreview 只为仍在显示的笔记加载预览；期间切换了笔记、仓库或配色时结束这个过期任务。
func (a *App) finishPreview(s *session, client *tinymist.Client, gen uint64, p tinymist.Preview, err error) bool {
	if a.current != s || s.previewGen != gen || a.lsp != client {
		if err == nil {
			go client.StopPreview(p.ID)
		}
		return false
	}
	s.previewStarting = false
	if err != nil {
		s.previewMissing = errors.Is(err, tinymist.ErrUnavailable)
		s.previewErr = "无法启动 Typst 预览：" + err.Error()
		if s.previewMissing {
			// 找不到命令和反复崩溃后停用都归到这里，编辑和保存不受影响。
			s.previewErr = err.Error()
			a.refreshMenu()
		}
		return false
	}
	stop := make(chan struct{})
	s.previewTask, s.previewURL, s.previewStop = p.ID, p.URL, stop
	s.scrollLine, s.scrollDue = s.ed.CursorPos().Line, time.Time{}
	a.preview.load(p.URL)
	// tinymist 退出时任务随之消失，回到主线程清掉它，下一帧会重启进程和任务。
	go func() {
		select {
		case <-p.Done:
			a.publish(func() { a.previewLost(s, gen) })
		case <-stop:
		case <-a.ctx.Done():
		}
	}()
	return true
}

func (a *App) previewLost(s *session, gen uint64) {
	if s.previewGen != gen || s.previewTask == "" {
		return
	}
	s.previewTask, s.previewURL = "", ""
	if s.previewStop != nil {
		close(s.previewStop)
		s.previewStop = nil
	}
	if a.current == s && a.preview != nil {
		a.preview.load("")
	}
}

// stopPreview 结束笔记的预览任务，并让进行中的启动过期。
func (a *App) stopPreview(s *session) {
	if s == nil {
		return
	}
	s.previewGen++
	s.previewStarting = false
	if s.previewStop != nil {
		close(s.previewStop)
		s.previewStop = nil
	}
	if s.previewTask != "" && a.lsp != nil {
		client, id := a.lsp, s.previewTask
		a.lspQueue.run(func() { client.StopPreview(id) })
	}
	if s.previewURL != "" && a.preview != nil {
		a.preview.load("")
	}
	s.previewTask, s.previewURL = "", ""
}

// resetPreview 清掉笔记的预览和诊断状态，用于改名后按新路径重新开始。
func (s *session) resetPreview() {
	s.previewErr, s.previewMissing, s.previewErrOpen = "", false, false
}

// previewUnavailable 报告预览失败是否因为缺少 tinymist。
func previewUnavailable(s *session) bool {
	return s.kind == vault.KindTypst && s.previewMissing
}
