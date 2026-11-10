package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrExists 表示目标名称已被占用。
var ErrExists = errors.New("同名文件已存在")

// CreateUntitled 在目录 dir 中创建一篇空笔记，名称重复时追加序号。
func (v *Vault) CreateUntitled(dir string, kind Kind) (string, FileStat, error) {
	ext := ".md"
	if kind == KindTypst {
		ext = ".typ"
	}
	for i := 1; i < 1000; i++ {
		name := "未命名" + ext
		if i > 1 {
			name = fmt.Sprintf("未命名 %d%s", i, ext)
		}
		id := path.Join(dir, name)
		st, err := v.Create(id)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return id, st, err
	}
	return "", FileStat{}, ErrExists
}

// CreateFolder 在目录 dir 中创建一个新文件夹，名称重复时追加序号。
func (v *Vault) CreateFolder(dir string) (string, error) {
	for i := 1; i < 1000; i++ {
		name := "未命名文件夹"
		if i > 1 {
			name = fmt.Sprintf("未命名文件夹 %d", i)
		}
		id := path.Join(dir, name)
		real, err := v.Resolve(id)
		if err != nil {
			return "", err
		}
		err = os.Mkdir(real, 0o755)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return id, err
	}
	return "", ErrExists
}

// Rename 在同一目录内把 id 改名为 name，返回新 ID。目标已存在时不覆盖。
func (v *Vault) Rename(id, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("无效的名称 %q", name)
	}
	// 以点开头的条目在扫描时被忽略，改成这样的名称会让笔记从侧栏中消失。
	if strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("名称不能以 . 开头")
	}
	from, err := v.entryPath(id)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(from)
	if err != nil {
		return "", err
	}
	parent := path.Dir(id)
	if parent == "." {
		parent = ""
	}
	target := path.Join(parent, name)
	to, err := v.entryPath(target)
	if err != nil {
		return "", err
	}
	// 大小写改名只有在目标确实是同一个文件时才能放行。
	if targetInfo, err := os.Lstat(to); err == nil && !os.SameFile(info, targetInfo) {
		return "", ErrExists
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(from, to); err != nil {
		return "", err
	}
	return filepath.ToSlash(target), nil
}

// entryPath 返回条目本身的路径：只解析父目录，不跟随条目自身的符号链接，
// 这样改名修改的是链接而不是它指向的文件。
func (v *Vault) entryPath(id string) (string, error) {
	if !validID(id) {
		return "", ErrOutside
	}
	name := filepath.Join(v.root, filepath.FromSlash(id))
	parent, err := filepath.EvalSymlinks(filepath.Dir(name))
	if err != nil {
		return "", err
	}
	if parent != v.root {
		if _, err := v.contained(parent); err != nil {
			return "", err
		}
	}
	return filepath.Join(parent, filepath.Base(name)), nil
}
