package vault

import (
	"cmp"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Tree 保存一次仓库扫描的结果。根目录本身不作为列表项，父键为空字符串。
// Files 是全部文件，Titles 只有笔记的标题。
type Tree struct {
	Roots  []string
	Kids   map[string][]string
	Dirs   map[string]bool
	Files  map[string]bool
	Titles map[string]string
}

// Children 返回目录的子项，文件返回 nil。
func (t *Tree) Children(id string) []string {
	if !t.Dirs[id] {
		return nil
	}
	return t.Kids[id]
}

// titleBytes 是扫描时为提取标题读取的字节数，一级标题通常在文件开头。
const titleBytes = 4 << 10

// Scan 扫描目录和其中的全部文件，忽略以点开头的条目，例如 .git 和旧版的 .gotyp。
// 无法读取的子目录直接跳过，一个目录的权限问题不能让整个仓库打不开。
func (v *Vault) Scan() (*Tree, error) {
	t := &Tree{
		Kids:   map[string][]string{"": {}},
		Dirs:   map[string]bool{},
		Files:  map[string]bool{},
		Titles: map[string]string{},
	}
	err := filepath.WalkDir(v.root, func(name string, entry fs.DirEntry, err error) error {
		if name == v.root {
			return err
		}
		if err != nil || strings.HasPrefix(entry.Name(), ".") {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(v.root, name)
		if err != nil {
			return err
		}
		v.scanEntry(t, filepath.ToSlash(rel), entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	t.sort()
	return t, nil
}

// scanEntry 把一个目录或普通文件加入树中，笔记另外读取标题。
func (v *Vault) scanEntry(t *Tree, id string, entry fs.DirEntry) {
	parent := path.Dir(id)
	if parent == "." {
		parent = ""
	}
	if entry.IsDir() {
		t.Dirs[id] = true
		t.Kids[id] = []string{}
		t.Kids[parent] = append(t.Kids[parent], id)
		return
	}
	// Resolve 拒绝指向仓库外部的符号链接；WalkDir 不跟随符号链接目录。
	resolved, err := v.Resolve(id)
	if err != nil {
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	t.Kids[parent] = append(t.Kids[parent], id)
	t.Files[id] = true
	if kind, ok := KindFor(entry.Name()); ok {
		t.Titles[id] = Title(id, kind, readHead(resolved))
	}
}

// sort 让目录排在文件前面，同类按不区分大小写的名称排序。
func (t *Tree) sort() {
	for parent, kids := range t.Kids {
		slices.SortFunc(kids, func(a, b string) int {
			if t.Dirs[a] != t.Dirs[b] {
				if t.Dirs[a] {
					return -1
				}
				return 1
			}
			return cmp.Or(
				strings.Compare(strings.ToLower(path.Base(a)), strings.ToLower(path.Base(b))),
				strings.Compare(a, b),
			)
		})
		t.Kids[parent] = kids
	}
	t.Roots = t.Kids[""]
}

// ReadTitle 重新读取一篇笔记的标题，供文件内容变化后更新快速打开的匹配。
func (v *Vault) ReadTitle(id string) string {
	kind, _ := KindFor(id)
	name, err := v.Resolve(id)
	if err != nil {
		return Title(id, kind, "")
	}
	return Title(id, kind, readHead(name))
}

// readHead 读取文件开头一段内容，失败时返回空字符串，标题回退为文件名。
func readHead(name string) string {
	f, err := os.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, titleBytes)
	n, _ := io.ReadFull(f, buf)
	return string(buf[:n])
}
