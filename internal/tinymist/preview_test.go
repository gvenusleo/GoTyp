package tinymist

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

type previewOutcome struct {
	file *PreviewFile
	err  error
}

func previewAsync(cl *Client, ctx context.Context, doc Document) <-chan previewOutcome {
	result := make(chan previewOutcome, 1)
	go func() {
		file, err := cl.Preview(ctx, doc)
		result <- previewOutcome{file, err}
	}()
	return result
}

func takePreviewRequest(t *testing.T, f *fakeServer) (message, string) {
	t.Helper()
	var p struct {
		Settings struct {
			Output string `json:"outputPath"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(f.next(t, "workspace/didChangeConfiguration").Params, &p); err != nil {
		t.Fatal(err)
	}
	req := f.next(t, "workspace/executeCommand")
	var command struct {
		Name string `json:"command"`
	}
	if err := json.Unmarshal(req.Params, &command); err != nil || command.Name != "tinymist.exportPdf" || p.Settings.Output == "" {
		t.Fatalf("导出请求无效：%s, %v", req.Params, err)
	}
	return req, p.Settings.Output + ".pdf"
}

func replyPreview(t *testing.T, f *fakeServer, req message, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	f.reply(req.ID, string(result))
}

func TestEditingRequestsAdvanceDuringPreview(t *testing.T) {
	cl, f := startFake(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first := Document{Path: "/v/a.typ", Text: "first", Version: 1, Session: 1}
	pending := previewAsync(cl, ctx, first)
	req, path := takePreviewRequest(t, f)
	fresh := Document{Path: first.Path, Text: "second", Version: 2, Session: 1}
	waiting := previewAsync(cl, ctx, fresh)
	finished := make(chan error, 2)
	go func() { _, err := cl.Complete(ctx, fresh, 1); finished <- err }()
	f.next(t, "textDocument/didChange")
	f.reply(f.next(t, "textDocument/completion").ID, `[]`)
	go func() { _, _, err := cl.Hover(ctx, fresh, 1); finished <- err }()
	f.reply(f.next(t, "textDocument/hover").ID, `null`)
	for range 2 {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case req := <-f.got:
		t.Fatalf("上一份 PDF 还未完成，已发出 %s", req.Method)
	default:
	}
	replyPreview(t, f, req, path, first.Text)
	verifyPreviewFile(t, path, first.Text, <-pending)
	nextReq, nextPath := takePreviewRequest(t, f)
	if path == nextPath {
		t.Fatal("两个快照共用了同一个文件")
	}
	replyPreview(t, f, nextReq, nextPath, fresh.Text)
	verifyPreviewFile(t, nextPath, fresh.Text, <-waiting)
}

func verifyPreviewFile(t *testing.T, path, data string, result previewOutcome) {
	t.Helper()
	if result.err != nil {
		t.Fatal(result.err)
	}
	buf := make([]byte, result.file.Size)
	_, err := result.file.File.ReadAt(buf, 0)
	result.file.Close()
	if err != nil || string(buf) != data {
		t.Fatalf("预览内容 = %q, %v", buf, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("临时目录未清理：%v", err)
	}
}

func TestRefreshReleasesInFlightPreview(t *testing.T) {
	cl, f := startFake(t)
	doc := Document{Path: "/v/a.typ", Text: "= A", Version: 1, Session: 1}
	pending := previewAsync(cl, t.Context(), doc)
	_, path := takePreviewRequest(t, f)
	fresh, server := startFake(t)
	cl.launch = fresh.launch
	server.holdSource = true
	refreshed := make(chan error, 1)
	go func() { refreshed <- cl.Refresh(t.Context(), doc) }()
	read := server.next(t, "textDocument/documentColor")
	select {
	case result := <-pending:
		if !errors.Is(result.err, ErrDisconnected) {
			t.Fatalf("旧预览结果 = %v", result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("新进程等待首次读取时，旧预览未结束")
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("旧预览目录未清理：%v", err)
	}
	server.reply(read.ID, `[]`)
	if err := <-refreshed; err != nil || cl.starts != 1 {
		t.Fatalf("刷新结果 = %v，启动次数 = %d", err, cl.starts)
	}
}

func TestNewPreviewSessionReplacesConnection(t *testing.T) {
	cl, f := startFake(t)
	first := Document{Path: "/v/a.typ", Text: "first", Version: 1, Session: 1}
	pending := previewAsync(cl, t.Context(), first)
	req, path := takePreviewRequest(t, f)
	replyPreview(t, f, req, path, first.Text)
	verifyPreviewFile(t, path, first.Text, <-pending)
	oldConn := cl.conn

	fresh, server := startFake(t)
	cl.launch = fresh.launch
	second := Document{Path: first.Path, Text: "second", Version: 1, Session: 2}
	pending = previewAsync(cl, t.Context(), second)
	req, path = takePreviewRequest(t, server)
	cl.CloseDoc(second.Path, first.Session)
	replyPreview(t, server, req, path, second.Text)
	verifyPreviewFile(t, path, second.Text, <-pending)
	if cl.conn == oldConn || cl.starts != 1 {
		t.Fatal("切换会话未隔离原进程，或消耗了崩溃重启次数")
	}
	if value, ok := cl.docs.Load(second.Path); !ok || value.(*docState).doc != second {
		t.Fatal("旧会话的关闭任务删除了新会话快照")
	}
}

func TestPreviewCancelCleansOutput(t *testing.T) {
	cl, f := startFake(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pending := previewAsync(cl, ctx, Document{Path: "/v/a.typ", Text: "= A", Version: 1})
	_, path := takePreviewRequest(t, f)
	cancel()
	if result := <-pending; !errors.Is(result.err, context.Canceled) || cl.conn != nil {
		t.Fatalf("取消结果 = %v，连接 = %v", result.err, cl.conn)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("取消后临时目录未清理：%v", err)
	}
}

func TestPreviewFromEditor(t *testing.T) {
	if _, err := exec.LookPath("tinymist"); err != nil {
		t.Skip("未安装 tinymist")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cl := New(t.Context(), root)
	defer cl.Close()
	note := filepath.Join(root, "note.typ")
	part := filepath.Join(root, "part.typ")
	if err := os.WriteFile(note, []byte(`#invalid-disk-source()`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, []byte("#let delta = 0pt"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := Document{Path: note, Session: 1}
	for version := range 5 {
		doc.Text = fmt.Sprintf("#import \"part.typ\": delta\n#set page(width: %dpt + delta, height: 200pt)\nPreview", 110+version)
		doc.Version = uint64(version)
		if err := cl.OpenContext(t.Context(), doc); err != nil {
			t.Fatal(err)
		}
	}
	checkPreviewWidth(t, cl, doc, "114")
	replacement := filepath.Join(root, "replacement.typ")
	if err := os.WriteFile(replacement, []byte("#let delta = 3pt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, part); err != nil {
		t.Fatal(err)
	}
	if err := cl.Refresh(t.Context(), doc); err != nil {
		t.Fatal(err)
	}
	checkPreviewWidth(t, cl, doc, "117")
	doc.Text = `#invalid-buffer()`
	doc.Version++
	if failed, err := cl.Preview(t.Context(), doc); !errors.Is(err, ErrCompile) {
		if failed != nil {
			failed.Close()
		}
		t.Fatalf("编译失败结果 = %v", err)
	}
	if after, err := os.ReadFile(note); err != nil || string(after) != `#invalid-disk-source()` {
		t.Fatalf("预览改写了源文件：%s, %v", after, err)
	}
}

func checkPreviewWidth(t *testing.T, cl *Client, doc Document, want string) {
	t.Helper()
	file, err := cl.Preview(t.Context(), doc)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file.File)
	if err != nil {
		t.Fatal(err)
	}
	box := regexp.MustCompile(`/MediaBox\s*\[\s*0\s+0\s+(\d+)(?:\.0+)?\s+200(?:\.0+)?\s*\]`).FindSubmatch(data)
	if len(box) != 2 || string(box[1]) != want {
		t.Fatalf("PDF 页面尺寸 = %q，期望宽度 %s pt", box, want)
	}
}

func TestPreviewUsesSafeOutputDirectory(t *testing.T) {
	if _, err := exec.LookPath("tinymist"); err != nil {
		t.Skip("未安装 tinymist")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	for _, key := range []string{"HOME", "XDG_CACHE_HOME", "LOCALAPPDATA"} {
		t.Setenv(key, cache)
	}
	temp := filepath.Join(root, "$root-$dir-$name")
	if err := os.Mkdir(temp, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, temp)
	}
	cl := New(t.Context(), root)
	defer cl.Close()
	doc := Document{Path: filepath.Join(root, "note.typ"), Text: "= Preview", Session: 1}
	file, err := cl.Preview(t.Context(), doc)
	if err != nil {
		t.Fatal(err)
	}
	output := file.File.Name()
	file.Close()
	if hasOutputVariables(output) {
		t.Fatalf("预览路径仍含模板变量：%s", output)
	}
	if _, err := os.Stat(filepath.Dir(output)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("备用预览目录未清理：%v", err)
	}
	if files, err := os.ReadDir(root); err != nil || len(files) != 1 || files[0].Name() != filepath.Base(temp) {
		t.Fatalf("模板展开在仓库中产生了文件：%v, %v", files, err)
	}
}
