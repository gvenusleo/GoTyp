package gui

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"gotyp/internal/vault"
)

// homeDir 用于把绝对路径缩写成 ~ 开头，只在启动时读取一次。
var homeDir, _ = os.UserHomeDir()

// extendToolPath 把常见的命令安装目录补到 PATH 末尾。从访达或程序坞启动时，进程拿不到 shell 配置的 PATH，
// 找不到 Homebrew、Cargo 或 mise 安装的 typst 和 tinymist。不启动 shell 读取配置，避免拖慢启动或执行用户的脚本。
func extendToolPath() {
	if runtime.GOOS == "windows" {
		return
	}
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if homeDir != "" {
		dirs = append(dirs, filepath.Join(homeDir, ".cargo", "bin"), filepath.Join(homeDir, ".local", "bin"),
			filepath.Join(homeDir, ".local", "share", "mise", "shims"))
	}
	list := filepath.SplitList(os.Getenv("PATH"))
	for _, dir := range dirs {
		if !slices.Contains(list, dir) {
			list = append(list, dir)
		}
	}
	os.Setenv("PATH", strings.Join(list, string(os.PathListSeparator)))
}

func shortPath(name string) string {
	if homeDir != "" && strings.HasPrefix(name, homeDir+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(name, homeDir)
	}
	return name
}

// parentDir 返回 ID 所在目录，仓库根目录为空字符串。
func parentDir(id string) string {
	dir := path.Dir(id)
	if dir == "." {
		return ""
	}
	return dir
}

// itemLabel 返回文件树中显示的名称，即带扩展名的文件名或目录名。
func itemLabel(id string) string {
	return path.Base(id)
}

// files 按文件树顺序列出全部文件。
func files(tree *vault.Tree) []string {
	if tree == nil {
		return nil
	}
	var out []string
	var walk func([]string)
	walk = func(items []string) {
		for _, id := range items {
			if tree.Dirs[id] {
				walk(tree.Children(id))
			} else {
				out = append(out, id)
			}
		}
	}
	walk(tree.Roots)
	return out
}

// affects 报告对 id 的改名或删除是否涉及当前笔记。
func affects(id, current string) bool {
	return current == id || strings.HasPrefix(current, id+"/")
}
