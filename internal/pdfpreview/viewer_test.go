package pdfpreview

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestPDFiumPreview(t *testing.T) {
	loader := testEngine(t)
	updates := make(chan func(), 1)
	v := newViewer(func(fn func()) { updates <- fn }, loader)
	t.Cleanup(func() {
		v.Close()
		select {
		case <-v.Done():
		case <-time.After(5 * time.Second):
			t.Error("查看器关闭后仍有后台任务")
		}
	})
	view := ui.NewTester(func(c *ui.Context) { v.View(c, nil, nil) }, 420, 360)
	view.Frame()
	v.Load(t.Context(), samplePDF(4), func(err error) bool {
		if err != nil {
			t.Error(err)
		}
		return err == nil
	})
	await(t, updates, view, func() bool { return v.document != 0 })
	first, ok := view.Find("第 1 页")
	if !ok || first.W < 300 || len(v.pages) != 4 {
		t.Fatalf("首次加载：页面 %v，页数 %d", first, len(v.pages))
	}
	img := view.Image()
	if pixel := img.RGBAAt(int(first.X+first.W*0.1), int(first.Y+first.H*0.2)); pixel != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("PDF 原色渲染为 %v", pixel)
	}
	frame := v.cache[0].frame
	if len(frame.chars) == 0 || len(frame.links) < 2 {
		t.Fatalf("PDF 缺少文字或链接：%d 字形，%d 链接", len(frame.chars), len(frame.links))
	}
	for _, label := range []string{"缩小", "放大", "适宽"} {
		if _, exists := view.Find(label); exists {
			t.Fatalf("预览中出现了 %s 按钮", label)
		}
	}
	view.ClickAt(first.X+30, first.Y+30)
	view.Key(ui.Shift, ui.KeyEqual)
	if v.zoom != 1.25 {
		t.Fatalf("键盘放大后的缩放为 %v", v.zoom)
	}
	view.Key(0, ui.KeyMinus)
	if v.zoom != 1 {
		t.Fatalf("键盘缩小后的缩放为 %v", v.zoom)
	}
	view.Pinch(first.X+80, first.Y+100, 1.25)
	if v.zoom != 1.25 {
		t.Fatalf("页面上捏合后的缩放为 %v", v.zoom)
	}
	view.Key(0, ui.Key0)
	if !v.viewportInput(ui.Services{})(ui.InputEvent{Kind: ui.InputScroll, Mods: ui.Ctrl, DY: -30, X: 100, Y: 100}) || v.zoom <= 1 {
		t.Fatalf("修饰键滚轮缩放为 %v", v.zoom)
	}
	view.Key(0, ui.Key0)
	if v.zoom != 1 {
		t.Fatalf("键盘适宽后的缩放为 %v", v.zoom)
	}
	v.scroll.X, v.scroll.Y = 0, 0
	view.Frame()
	checkCopy(t, v, view, updates, first, v.layout.pages[0], frame)
	view.Scroll(100, 100, 0, v.layout.pages[2].Y-v.scroll.Y+20)
	if v.anchor().page != 2 {
		t.Fatalf("连续滚动后位于第 %d 页", v.anchor().page+1)
	}
	checkRefresh(t, v, view, updates)
	view.SetSize(600, 380)
	view.Frame()
	if v.width != 600 || v.height <= 0 {
		t.Fatalf("未更新视口尺寸：%v × %v", v.width, v.height)
	}
	checkSupersededLoad(t, v, view, updates)
	checkSharpTiles(t, v, view, updates)
}

func checkCopy(t *testing.T, v *Viewer, view *ui.Tester, updates <-chan func(), visible, page ui.Rect, frame *pageFrame) {
	t.Helper()
	begin, end := frame.chars[0], frame.chars[len(frame.chars)-1]
	x1, y1 := visible.X+(begin.X+begin.W/2)*page.W, visible.Y+(begin.Y+begin.H/2)*page.H
	x2, y2 := visible.X+(end.X+end.W/2)*page.W, visible.Y+(end.Y+end.H/2)*page.H
	view.Press(x1, y1)
	view.Move(x2, y2)
	view.Release(x2, y2)
	view.Key(ui.Cmd, ui.KeyC)
	if !v.hasSelection {
		t.Fatal("拖动没有产生文字选区")
	}
	await(t, updates, view, func() bool { return view.Clipboard() != "" })
	if got := view.Clipboard(); got != "Hello PDF" {
		t.Errorf("复制文字为 %q", got)
	}
	view.SetClipboard("")
	view.RightClickAt(x1, y1)
	if err := view.ChooseMenuItem("复制"); err != nil {
		t.Fatal(err)
	}
	await(t, updates, view, func() bool { return view.Clipboard() == "Hello PDF" })
	var internal pageLink
	for _, link := range frame.links {
		if link.page == 1 {
			internal = link
		}
	}
	if internal.bounds.W == 0 {
		t.Fatal("未找到页内链接")
	}
	view.ClickAt(visible.X+(internal.bounds.X+internal.bounds.W/2)*page.W, visible.Y+(internal.bounds.Y+internal.bounds.H/2)*page.H)
	if v.anchor().page != 1 {
		t.Fatalf("点击页内链接后位于第 %d 页", v.anchor().page+1)
	}
	if v.anchor().fraction < 0.2 {
		t.Fatal("目的地在页内，跳转却停在页顶")
	}
}

func checkRefresh(t *testing.T, v *Viewer, view *ui.Tester, updates <-chan func()) {
	t.Helper()
	v.setZoom(1.25, 0, 0)
	v.scroll.Y = v.layout.offset(readingPosition{page: 1, fraction: 0.4})
	view.Frame()
	before, id := v.anchor(), v.document
	v.Load(t.Context(), samplePDF(3), func(err error) bool { return err == nil })
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	v.Load(canceled, samplePDF(1), func(error) bool {
		t.Error("已取消的任务进入安装回调")
		return true
	})
	await(t, updates, view, func() bool { return v.document != id })
	if after := v.anchor(); after.page != before.page || after.fraction < 0.39 || after.fraction > 0.41 || v.zoom != 1.25 {
		t.Fatalf("刷新丢失阅读位置：刷新前 %+v，刷新后 %+v，缩放 %v", before, after, v.zoom)
	}
	id = v.document
	failed := false
	v.Load(t.Context(), []byte("invalid"), func(err error) bool {
		failed = true
		if err == nil {
			t.Error("损坏的 PDF 加载成功")
		}
		return false
	})
	await(t, updates, view, func() bool { return failed })
	if v.document != id || len(v.pages) != 3 {
		t.Fatal("损坏的 PDF 替换了成功预览")
	}
	v.Clear()
	if len(v.pages) != 0 || len(v.cache) != 0 || v.scroll.Y != 0 {
		t.Fatal("切换后保留了旧文档状态")
	}
}

func checkSharpTiles(t *testing.T, v *Viewer, view *ui.Tester, updates <-chan func()) {
	t.Helper()
	v.SetScale(2)
	v.setZoom(4, 0, 0)
	v.scroll.X, v.scroll.Y = 200, 30
	view.Frame()
	before := bytes.Clone(view.Image().Pix)
	if len(v.tileKeys) == 0 {
		t.Fatal("高倍率下没有请求高清分块")
	}
	key := v.tileKeys[0]
	if got, want := float32(key.size.X), 2*v.layout.pages[key.page].W; got < want || got > want+1 {
		t.Fatalf("高倍率下渲染宽度为 %v，预期至少 %v 像素", got, want)
	}
	await(t, updates, view, func() bool {
		for _, key := range v.tileKeys {
			if v.tiles[key].frame == nil {
				return false
			}
		}
		return true
	})
	w, h := v.tiles[key].frame.bitmap.Size()
	if w != key.tile.Dx() || h != key.tile.Dy() {
		t.Fatalf("分块尺寸为 %d × %d", w, h)
	}
	if got := view.Image().RGBAAt(100, 60); got != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("偏移后的分块未对齐页面，颜色为 %v", got)
	}
	if bytes.Equal(before, view.Image().Pix) {
		t.Fatal("高清分块完成后，屏幕仍显示原来的整页回退图")
	}
	previous := v.document
	changed := bytes.ReplaceAll(samplePDF(2), []byte("1 0 0 rg"), []byte("0 1 0 rg"))
	v.Load(t.Context(), changed, func(err error) bool { return err == nil })
	await(t, updates, view, func() bool { return v.document != previous })
	if got := view.Image().RGBAAt(100, 60); got != (color.RGBA{0, 255, 0, 255}) {
		t.Fatalf("新文档提交后仍显示上一代分块，颜色为 %v", got)
	}
}

func TestRefreshRendersVisiblePages(t *testing.T) {
	updates := make(chan func(), 1)
	v := newViewer(func(fn func()) { updates <- fn }, testEngine(t))
	t.Cleanup(func() { v.Close(); <-v.Done() })
	view := ui.NewTester(func(c *ui.Context) { v.View(c, nil, nil) }, 420, 700)
	view.Frame()
	data := samplePDF(6)
	load := func(data []byte) {
		previous := v.document
		v.Load(t.Context(), data, func(err error) bool {
			if err != nil {
				t.Error(err)
			}
			return err == nil
		})
		await(t, updates, view, func() bool { return v.document != previous })
	}
	load(data)
	await(t, updates, view, func() bool { return v.cache[1].frame != nil })
	second, ok := view.Find("第 2 页")
	if !ok || second.Y+40 >= v.height {
		t.Fatal("测试视口未显示第二页")
	}
	green := color.RGBA{0, 255, 0, 255}
	load(bytes.ReplaceAll(data, []byte("1 0 0 rg"), []byte("0 1 0 rg")))
	if got := view.Image().RGBAAt(int(second.X+20), int(second.Y+40)); got != green {
		t.Errorf("刷新提交后的第二页像素为 %v，预期为 %v", got, green)
	}

	// 位图已排队时仍可滚动，提交必须覆盖新的视口。
	await(t, updates, view, func() bool { return len(v.wanted) == 0 })
	previous := v.document
	v.Load(t.Context(), data, func(err error) bool { return err == nil })
	select {
	case apply := <-updates:
		v.scroll.Y = v.layout.pages[3].Y
		view.Frame()
		apply()
		view.Frame()
	case <-time.After(5 * time.Second):
		t.Fatal("没有收到刷新结果")
	}
	await(t, updates, view, func() bool { return v.document != previous })
	first, last := v.visiblePages()
	assertVisibleFrames(t, v, first, last)

	v.scroll.Y = v.layout.pages[4].Y
	view.Frame()
	if v.anchor().page < 2 {
		t.Fatal("测试视口未滚到即将移除的页面")
	}
	load(samplePDF(2))
	first, last = v.visiblePages()
	assertVisibleFrames(t, v, first, last)
}

func assertVisibleFrames(t *testing.T, v *Viewer, first, last int) {
	t.Helper()
	for page := first; page <= last; page++ {
		frame := v.cache[page].frame
		if frame == nil || frame.key.document != v.document {
			t.Errorf("第 %d 页缺少新文档的位图", page+1)
		}
	}
}

func TestVisiblePageRequestsSettle(t *testing.T) {
	v := &Viewer{
		worker: &worker{wake: make(chan struct{}, 1)}, document: 1,
		width: 3800, height: 2200, gap: 12, zoom: 1, scale: 1,
		cache: make(map[int]cachedPage), tiles: make(map[rasterKey]cachedPage),
	}
	v.pages = make([]pageSize, 10)
	for i := range v.pages {
		v.pages[i] = pageSize{3800, 180}
	}
	v.layout = newLayout(v.pages, v.width, v.zoom, v.gap)
	first, last := v.visiblePages()
	for range 60 {
		v.requestVisible(first, last)
		if len(v.wanted) == 0 {
			if len(v.tiles) == 0 || len(v.cache) != len(v.pages) {
				t.Fatal("未完成可见分块及整页回退图")
			}
			return
		}
		key := v.wanted[0]
		v.finishPage(key, &pageFrame{key: key, pixels: image.NewRGBA(image.Rectangle{Max: key.region().Size()})}, nil)
	}
	t.Fatal("可见页仍在反复重绘")
}

func checkSupersededLoad(t *testing.T, v *Viewer, view *ui.Tester, updates <-chan func()) {
	t.Helper()
	v.Load(t.Context(), samplePDF(5), func(error) bool {
		t.Error("上一笔记的 PDF 在切换后进入安装回调")
		return true
	})
	var oldUpdate func()
	select {
	case oldUpdate = <-updates:
	case <-time.After(10 * time.Second):
		t.Fatal("旧文档未完成后台渲染")
	}
	v.Clear()
	v.Load(t.Context(), samplePDF(2), func(err error) bool { return err == nil })
	oldUpdate()
	if v.document != 0 {
		t.Fatal("旧文档在切换后替换了预览")
	}
	await(t, updates, view, func() bool { return v.document != 0 })
	if len(v.pages) != 2 {
		t.Fatalf("切换后显示了 %d 页", len(v.pages))
	}
}

func await(t *testing.T, updates <-chan func(), view *ui.Tester, until func() bool) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for !until() {
		select {
		case update := <-updates:
			update()
			view.Frame()
		case <-timeout:
			t.Fatal("PDFium 结果未返回")
		}
	}
}

func testEngine(t *testing.T) func() (*engine, error) {
	t.Helper()
	name, err := targetLibrary()
	if err != nil {
		t.Skip(err)
	}
	path := os.Getenv("GOTYP_TEST_PDFIUM")
	if path == "" {
		path, err = filepath.Abs(filepath.Join("..", "..", "resources", runtime.GOOS+"-"+runtime.GOARCH, "pdfium", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Skip("本机尚未准备 PDFium 构建资源")
		}
	}
	return func() (*engine, error) { return loadEngine(path) }
}

func samplePDF(count int) []byte {
	var kids strings.Builder
	for i := range count {
		fmt.Fprintf(&kids, "%d 0 R ", 5+i)
	}
	stream := "1 0 0 rg 0 0 60 430 re f\n0 0 0 rg BT /F1 22 Tf 85 380 Td (Hello PDF) Tj ET\n"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", count, kids.String()),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream),
	}
	for i := range count {
		page := "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 430] /Resources << /Font << /F1 3 0 R >> >> /Contents 4 0 R"
		if i == 0 && count >= 2 {
			page += fmt.Sprintf(" /Annots [%d 0 R %d 0 R]", 5+count, 6+count)
		}
		objects = append(objects, page+" >>")
	}
	objects = append(objects,
		"<< /Type /Annot /Subtype /Link /Rect [120 325 212 355] /A << /S /URI /URI (https://example.com/) >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [80 325 115 355] /Dest [6 0 R /XYZ 0 300 null] >>",
	)
	var pdf strings.Builder
	pdf.WriteString("%PDF-1.7\n")
	positions := make([]int, len(objects))
	for i, object := range objects {
		positions[i] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range positions {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return []byte(pdf.String())
}
