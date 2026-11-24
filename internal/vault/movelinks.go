package vault

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// checkMoveLinks 防止跨目录移动让已打开的链接改指其他文件。
func checkMoveLinks(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return checkDirectoryMoveLinks(from)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	link, err := os.Readlink(from)
	if err != nil || filepath.IsAbs(link) {
		return err
	}
	oldDir, newDir := filepath.Dir(from), filepath.Dir(to)
	// 两条路径应直接指向同一位置，避免用仍存在的原链接验证搬走后的目标。
	if filepath.Join(oldDir, link) == filepath.Join(newDir, link) {
		before, err := filepath.EvalSymlinks(from)
		// 保留链接原文中的 ..，因为它前面的路径分量也可能是符号链接。
		after, nextErr := filepath.EvalSymlinks(newDir + string(os.PathSeparator) + link)
		if err == nil && nextErr == nil && before == after {
			return nil
		}
	}
	return fmt.Errorf("移动后无法保持符号链接「%s」的目标", filepath.Base(from))
}

func checkDirectoryMoveLinks(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&os.ModeSymlink == 0 {
			return err
		}
		link, err := root.Readlink(filepath.FromSlash(name))
		if err != nil || filepath.IsAbs(link) {
			return err
		}
		// 整条解析链留在目录内时，挪动这棵子树不会改变相对链接的目标。
		if _, err := root.Stat(filepath.FromSlash(name)); err != nil {
			return fmt.Errorf("移动后无法保持符号链接「%s」的目标", name)
		}
		return nil
	})
}
