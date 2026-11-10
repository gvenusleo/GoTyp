package typst

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrWatchExited 表示 typst watch 进程已经退出。
var ErrWatchExited = errors.New("Typst 预览进程已退出")

// typst watch 先打印状态行，再打印诊断，之后没有结束标记。状态行报告有错误或警告时，
// 最多等 diagnosticFirst 拿到第一条诊断，之后以 diagnosticWait 的静默判断这一段结束；
// 编译成功时没有诊断，不必等待。
const (
	diagnosticFirst = 2 * time.Second
	diagnosticWait  = 40 * time.Millisecond
)

// statusLine 匹配一次编译结束时的状态行，例如「[13:34:50] compiled successfully in 25.1 ms」，
// 第二组是可选的耗时。
var statusLine = regexp.MustCompile(`^\[[0-9:]+\] compiled (successfully|with warnings|with errors)(?: in ([0-9.]+ ?(?:ms|s|µs)))?`)

// pageFile 匹配 page-{p}-of-{t}.png 形式的输出文件。
var pageFile = regexp.MustCompile(`^page-(\d+)-of-(\d+)\.png$`)

// WatchResult 是一次编译的结果。Pages 按页序排列，与上一次结果相比没有变化的页面为 nil。
type WatchResult struct {
	Pages       [][]byte
	Diagnostics string
	Duration    time.Duration // 状态行报告的编译耗时，没有报告时为零
	Err         error
}

// Watch 是服务一篇笔记预览的常驻 typst watch 进程。它在临时目录中的镜像仓库里运行，
// 主文件内容由 Write 替换。方法不能并发调用。
type Watch struct {
	tmp     string
	main    string
	out     string
	cancel  context.CancelFunc
	stdin   io.WriteCloser // 守护脚本盯着的管道，关闭即结束进程
	lines   chan string
	exited  chan struct{}
	last    string
	pending string
	stamps  map[string]time.Time
}

// StartWatch 建立镜像仓库并启动 typst watch。进程启动后会立即编译一次。
func StartWatch(ctx context.Context, req Request) (*Watch, error) {
	if _, err := exec.LookPath("typst"); err != nil {
		return nil, ErrUnavailable
	}
	tmp, err := makeTemp("watch")
	if err != nil {
		return nil, err
	}
	w := &Watch{tmp: tmp, out: filepath.Join(tmp, "out"), last: req.Source, stamps: map[string]time.Time{}}
	if err := w.start(ctx, req); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	return w, nil
}

func (w *Watch) start(ctx context.Context, req Request) error {
	root := filepath.Join(w.tmp, "vault")
	main, err := writeMirror(req.Root, root, req.LogicalPath, req.Source)
	if err != nil {
		return err
	}
	w.main = main
	if err := os.Mkdir(w.out, 0o755); err != nil {
		return err
	}
	ctx, w.cancel = context.WithCancel(ctx)
	cmd, stdin, err := watchCommand(ctx, []string{"watch", "--root", root, "--diagnostic-format", "short",
		"--ppi", strconv.Itoa(req.ppi()), main, filepath.Join(w.out, "page-{p}-of-{t}.png")})
	if err != nil {
		w.cancel()
		return err
	}
	w.stdin = stdin
	// 在镜像根目录运行，诊断中的路径就是笔记在仓库中的相对路径。
	cmd.Dir = root
	stderr, err := cmd.StderrPipe()
	if err != nil {
		w.cancel()
		return err
	}
	if err := cmd.Start(); err != nil {
		w.cancel()
		return err
	}
	w.lines = make(chan string, 64)
	w.exited = make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			// 关闭后没有人再读，继续读到结尾让进程退出，但不再发送。
			select {
			case w.lines <- scanner.Text():
			case <-ctx.Done():
			}
		}
		close(w.lines)
		cmd.Wait()
		close(w.exited)
	}()
	return nil
}

// Write 原子替换镜像中的主文件。内容与上一次写入相同时不写，返回 false，此时不会产生新的编译。
func (w *Watch) Write(source string) (bool, error) {
	if source == w.last {
		return false, nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(w.main), ".gotyp-snapshot-*")
	if err != nil {
		return false, err
	}
	_, err = tmp.WriteString(source)
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), w.main)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return false, err
	}
	w.last = source
	return true, nil
}

// Next 等待下一次编译结束。依赖文件变化引起的编译同样会在这里返回。
func (w *Watch) Next(ctx context.Context) (WatchResult, error) {
	m, err := w.nextStatus(ctx)
	if err != nil {
		return WatchResult{}, err
	}
	dur, _ := time.ParseDuration(strings.ReplaceAll(m[2], " ", ""))
	diagnostics := ""
	if m[1] != "successfully" {
		diagnostics = w.collectDiagnostics(ctx)
	}
	if m[1] == "with errors" {
		if diagnostics == "" {
			diagnostics = "Typst 编译失败"
		}
		return WatchResult{Diagnostics: diagnostics, Duration: dur, Err: errors.New(diagnostics)}, nil
	}
	pages, err := w.readPages()
	if err != nil {
		return WatchResult{Err: err}, nil
	}
	return WatchResult{Pages: pages, Diagnostics: diagnostics, Duration: dur}, nil
}

// nextStatus 返回下一条状态行的匹配结果：第一组是结果，第二组是耗时。
func (w *Watch) nextStatus(ctx context.Context) ([]string, error) {
	if line := w.pending; line != "" {
		w.pending = ""
		return statusLine.FindStringSubmatch(line), nil
	}
	for {
		select {
		case line, ok := <-w.lines:
			if !ok {
				return nil, ErrWatchExited
			}
			if m := statusLine.FindStringSubmatch(line); m != nil {
				return m, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// collectDiagnostics 读取状态行之后的诊断，遇到下一次编译的状态行时留给下一次 Next。
func (w *Watch) collectDiagnostics(ctx context.Context) string {
	var out []string
	for {
		wait := diagnosticWait
		if len(out) == 0 {
			wait = diagnosticFirst
		}
		select {
		case line, ok := <-w.lines:
			switch {
			case !ok:
				return strings.Join(out, "\n")
			case statusLine.MatchString(line):
				w.pending = line
				return strings.Join(out, "\n")
			case isDiagnostic(line):
				out = append(out, line)
			}
		case <-time.After(wait):
			return strings.Join(out, "\n")
		case <-ctx.Done():
			return strings.Join(out, "\n")
		}
	}
}

// isDiagnostic 排除 typst watch 每次编译都会重复打印的提示行。
func isDiagnostic(line string) bool {
	line = strings.TrimSpace(line)
	return line != "" && !strings.HasPrefix(line, "watching ") && !strings.HasPrefix(line, "writing to ") &&
		!strings.HasSuffix(line, "compiling ...")
}

// readPages 以最新写出的页面文件确定总页数，只读取修改时间变化的页面，并删除总页数不符的旧文件。
func (w *Watch) readPages() ([][]byte, error) {
	entries, err := os.ReadDir(w.out)
	if err != nil {
		return nil, err
	}
	total, newest := 0, time.Time{}
	stamps := map[string]time.Time{}
	for _, entry := range entries {
		m := pageFile.FindStringSubmatch(entry.Name())
		info, err := entry.Info()
		if m == nil || err != nil {
			continue
		}
		stamps[entry.Name()] = info.ModTime()
		if info.ModTime().After(newest) {
			newest = info.ModTime()
			total, _ = strconv.Atoi(m[2])
		}
	}
	if total == 0 {
		return nil, errors.New("Typst 没有生成页面")
	}
	pages := make([][]byte, total)
	for name, stamp := range stamps {
		m := pageFile.FindStringSubmatch(name)
		if count, _ := strconv.Atoi(m[2]); count != total {
			os.Remove(filepath.Join(w.out, name))
			continue
		}
		index, _ := strconv.Atoi(m[1])
		if index < 1 || index > total || w.stamps[name].Equal(stamp) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(w.out, name))
		if err != nil {
			return nil, err
		}
		pages[index-1] = data
	}
	for name := range w.stamps {
		if _, ok := stamps[name]; !ok {
			delete(w.stamps, name)
		}
	}
	for name, stamp := range stamps {
		if m := pageFile.FindStringSubmatch(name); m[2] == strconv.Itoa(total) {
			w.stamps[name] = stamp
		}
	}
	return pages, nil
}

// Close 结束进程并删除临时目录。
func (w *Watch) Close() error {
	w.stdin.Close()
	w.cancel()
	<-w.exited
	if err := os.RemoveAll(w.tmp); err != nil {
		return fmt.Errorf("清理 Typst 临时目录失败：%w", err)
	}
	return nil
}
