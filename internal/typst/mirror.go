package typst

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// writeMirror 只展开笔记的父目录，其他文件和目录链接到原仓库。
// 这样临时主文件与真实主文件看到相同的相对资源路径，又不需要复制整个仓库。
func writeMirror(realRoot, mirrorRoot, logicalPath, source string) (string, error) {
	if logicalPath == "" || logicalPath != path.Clean(logicalPath) ||
		strings.HasPrefix(logicalPath, "/") || strings.Contains(logicalPath, `\`) {
		return "", fmt.Errorf("无效的 Typst 逻辑路径 %q", logicalPath)
	}
	realRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		return "", err
	}
	written := false
	err = mirrorDir(realRoot, realRoot, mirrorRoot, "", logicalPath, source, &written)
	if err != nil {
		return "", err
	}
	if !written {
		return "", fmt.Errorf("找不到 Typst 主文件 %s", logicalPath)
	}
	return filepath.Join(mirrorRoot, filepath.FromSlash(logicalPath)), nil
}

func mirrorDir(realRoot, realDir, shadowDir, parent, logicalPath, source string, written *bool) error {
	if err := os.MkdirAll(shadowDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(realDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".gotyp" || name == ".git" {
			continue
		}
		id := path.Join(parent, name)
		realPath := filepath.Join(realDir, name)
		mirrorPath := filepath.Join(shadowDir, name)
		switch {
		case id == logicalPath:
			if entry.IsDir() {
				return fmt.Errorf("Typst 主文件是目录 %s", logicalPath)
			}
			if err := os.WriteFile(mirrorPath, []byte(source), 0o644); err != nil {
				return err
			}
			*written = true
		case entry.IsDir() && strings.HasPrefix(logicalPath, id+"/"):
			if err := mirrorDir(realRoot, realPath, mirrorPath, id, logicalPath, source, written); err != nil {
				return err
			}
		default:
			if !insideRoot(realRoot, realPath) {
				continue
			}
			if err := linkOrCopy(realRoot, realPath, mirrorPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func insideRoot(root, name string) bool {
	real, err := filepath.EvalSymlinks(name)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, real)
	return err == nil && filepath.IsLocal(rel)
}

func linkOrCopy(root, source, target string) error {
	if err := os.Symlink(source, target); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrExist) {
		return copyPath(root, source, target)
	}
	return fmt.Errorf("临时镜像已存在 %s", target)
}

func copyPath(root, source, target string) error {
	if !insideRoot(root, source) {
		return nil
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil
	}
	if info.IsDir() {
		if err := os.Mkdir(target, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() == ".gotyp" || entry.Name() == ".git" {
				continue
			}
			if err := copyPath(root, filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}
