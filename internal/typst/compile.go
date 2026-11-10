// Package typst 调用 Typst CLI 编译源码快照，不依赖界面。
package typst

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ErrUnavailable 表示 PATH 中没有 Typst CLI。
var ErrUnavailable = errors.New("未找到 Typst 编译器，请安装 typst 并加入 PATH")

// previewPPI 是预览 PNG 的分辨率。预览页面最宽约 800 DIP，192 ppi 让 A4 页面在 2 倍屏上也足够清晰。
const previewPPI = 192

// Request 描述一篇笔记的不可变源码快照。
type Request struct {
	Root        string
	LogicalPath string
	Source      string
}

// Result 包含编译产物和编译器输出的警告。
type Result struct {
	Files       [][]byte
	Diagnostics string
}

type format uint8

const (
	formatPNG format = iota + 1
	formatPDF
)

// Preview 把源码编译为按页排序的 PNG。
func Preview(ctx context.Context, req Request) (Result, error) {
	return compile(ctx, req, formatPNG)
}

// PDF 把源码编译为一个 PDF 文件。
func PDF(ctx context.Context, req Request) (Result, error) {
	return compile(ctx, req, formatPDF)
}

func compile(ctx context.Context, req Request, out format) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if _, err := exec.LookPath("typst"); err != nil {
		return Result{}, ErrUnavailable
	}
	tmp, err := makeTemp("typst")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)
	root := filepath.Join(tmp, "vault")
	main, err := writeMirror(req.Root, root, req.LogicalPath, req.Source)
	if err != nil {
		return Result{}, err
	}
	outDir := filepath.Join(tmp, "out")
	if err := os.Mkdir(outDir, 0o755); err != nil {
		return Result{}, err
	}
	output := filepath.Join(outDir, "page-{p}.png")
	if out == formatPDF {
		output = filepath.Join(outDir, "document.pdf")
	}
	args := []string{"compile", "--root", root, "--diagnostic-format", "short"}
	if out == formatPNG {
		args = append(args, "--ppi", strconv.Itoa(previewPPI))
	}
	args = append(args, main, output)
	cmd := exec.CommandContext(ctx, "typst", args...)
	// 在镜像根目录运行，错误信息中的路径就是笔记在仓库中的相对路径。
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	diagnostics := strings.TrimSpace(strings.Join([]string{stdout.String(), stderr.String()}, "\n"))
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if err != nil {
		if diagnostics != "" {
			return Result{}, errors.New(diagnostics)
		}
		return Result{}, err
	}
	files, err := readOutputs(outDir, out)
	if err != nil {
		return Result{}, err
	}
	return Result{Files: files, Diagnostics: diagnostics}, nil
}

func readOutputs(dir string, out format) ([][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type outputFile struct {
		name string
		page int
	}
	var names []outputFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if out == formatPDF && entry.Name() == "document.pdf" {
			names = append(names, outputFile{name: entry.Name()})
			continue
		}
		if out == formatPNG && strings.HasPrefix(entry.Name(), "page-") && strings.HasSuffix(entry.Name(), ".png") {
			page, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "page-"), ".png"))
			if err == nil {
				names = append(names, outputFile{name: entry.Name(), page: page})
			}
		}
	}
	sort.Slice(names, func(i, j int) bool { return names[i].page < names[j].page })
	if len(names) == 0 {
		return nil, fmt.Errorf("Typst 没有生成预览文件")
	}
	files := make([][]byte, 0, len(names))
	for _, item := range names {
		data, err := os.ReadFile(filepath.Join(dir, item.name))
		if err != nil {
			return nil, err
		}
		files = append(files, data)
	}
	return files, nil
}
