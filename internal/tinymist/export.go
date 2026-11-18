package tinymist

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExportPDF 从磁盘编译，独立于预览进程中的未保存文档。
func ExportPDF(ctx context.Context, root, source string) ([]byte, error) {
	dir, err := makeTemp("export")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "export.pdf")
	cmd := exec.CommandContext(ctx, "tinymist", "compile", "--root", root, source, output)
	cmd.Dir = root
	log, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrUnavailable
		}
		if msg := strings.TrimSpace(string(log)); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	return os.ReadFile(output)
}
