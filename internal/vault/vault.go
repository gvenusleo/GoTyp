// Package vault 管理笔记仓库的文件路径和持久化，不依赖界面。
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

// Kind 是笔记的源码格式。
type Kind uint8

const (
	KindMarkdown Kind = iota + 1
	KindTypst
)

var (
	// ErrConflict 表示文件在读取或上次保存之后被其他程序修改。
	ErrConflict = errors.New("文件已被其他程序修改")
	// ErrOutside 表示路径超出仓库范围。
	ErrOutside = errors.New("路径超出笔记仓库")
	// ErrUnsupported 表示文件扩展名不受支持。
	ErrUnsupported = errors.New("不支持的笔记格式")
	// ErrDeleted 表示文件在读取或上次保存之后被删除，它也是一种 ErrConflict。
	ErrDeleted error = deletedError{}
	// ErrNotText 表示文件不是合法的 UTF-8 文本，放进编辑器再保存会改坏原内容。
	ErrNotText = errors.New("不是 UTF-8 文本")
	// ErrTooLarge 表示文件超过了打开的大小上限。
	ErrTooLarge = errors.New("文件太大")
)

// 打开文件的大小上限。文本整篇放进编辑器，图片整份读入后解码。
const (
	MaxTextBytes  = 10 << 20
	MaxImageBytes = 20 << 20
)

type deletedError struct{}

func (deletedError) Error() string        { return "文件已被删除" }
func (deletedError) Is(target error) bool { return target == ErrConflict }

// KindFor 按扩展名识别笔记格式。
func KindFor(name string) (Kind, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return KindMarkdown, true
	case ".typ":
		return KindTypst, true
	default:
		return 0, false
	}
}

// Vault 是一个已经解析到真实路径的仓库。
type Vault struct {
	root string
}

// Open 打开一个已有目录作为仓库。
func Open(root string) (*Vault, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s 不是目录", root)
	}
	return &Vault{root: real}, nil
}

// Root 返回仓库的绝对路径。
func (v *Vault) Root() string { return v.root }

// ID 把用户选择的文件路径转换成仓库内的笔记 ID。
func (v *Vault) ID(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	real := abs
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		real = resolved
	} else if errors.Is(err, fs.ErrNotExist) {
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(abs))
		if parentErr != nil {
			return "", parentErr
		}
		real = filepath.Join(parent, filepath.Base(abs))
	} else {
		return "", err
	}
	rel, err := filepath.Rel(v.root, real)
	if err != nil || !filepath.IsLocal(rel) {
		return "", ErrOutside
	}
	id := filepath.ToSlash(rel)
	if !validID(id) {
		return "", ErrOutside
	}
	return id, nil
}

// Resolve 返回笔记的真实路径，并拒绝越出仓库的符号链接。
func (v *Vault) Resolve(id string) (string, error) {
	if !validID(id) {
		return "", ErrOutside
	}
	name := filepath.Join(v.root, filepath.FromSlash(id))
	real, err := filepath.EvalSymlinks(name)
	if err == nil {
		return v.contained(real)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(name))
	if err != nil {
		return "", err
	}
	if _, err := v.contained(parent); err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(name)), nil
}

func (v *Vault) contained(name string) (string, error) {
	rel, err := filepath.Rel(v.root, name)
	if err != nil || !filepath.IsLocal(rel) {
		return "", ErrOutside
	}
	return name, nil
}

func validID(id string) bool {
	return id != "" && id != "." && id == path.Clean(id) &&
		!strings.HasPrefix(id, "/") && !strings.Contains(id, `\`) &&
		filepath.IsLocal(filepath.FromSlash(id))
}

// Title 从第一个一级标题提取展示标题，没有标题时使用文件名。
func Title(id string, kind Kind, source string) string {
	for line := range strings.Lines(source) {
		line = strings.TrimSpace(line)
		var title string
		switch kind {
		case KindMarkdown:
			if strings.HasPrefix(line, "# ") {
				title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			}
		case KindTypst:
			if strings.HasPrefix(line, "= ") {
				title = strings.TrimSpace(strings.TrimPrefix(line, "= "))
			}
		}
		if title != "" {
			return title
		}
	}
	name := path.Base(id)
	return strings.TrimSuffix(name, path.Ext(name))
}
