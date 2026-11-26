package tinymist

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// tempPrefix 是 GoTyp 临时目录的前缀，后面跟创建者的进程号。
const tempPrefix = "gotyp-"

var cleanTemp = sync.OnceFunc(func() { cleanStaleTemp(os.TempDir()) })

var cleanPreviewCache = sync.OnceFunc(func() {
	if cache, err := os.UserCacheDir(); err == nil {
		cleanStaleTemp(filepath.Join(cache, "GoTyp", "preview"))
	}
})

// makeTemp 创建带进程号的临时目录。第一次调用时顺带清理已退出进程留下的目录：
// 应用退出或崩溃时后台任务来不及删除自己的目录，进程号让多个实例不会误删彼此的目录。
func makeTemp(kind string) (string, error) {
	cleanTemp()
	return os.MkdirTemp("", tempPattern(kind))
}

func makePreviewTemp() (string, error) {
	cleanTemp()
	cleanPreviewCache()
	// 规范化系统临时目录，避免 /var 与 /private/var 等别名影响回执校验。
	base, err := filepath.EvalSymlinks(os.TempDir())
	if err == nil && !hasOutputVariables(base) {
		return os.MkdirTemp(base, tempPattern("preview"))
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	base = filepath.Join(cache, "GoTyp", "preview")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	if hasOutputVariables(base) {
		return "", errors.New("预览临时目录包含 tinymist 路径模板变量")
	}
	return os.MkdirTemp(base, tempPattern("preview"))
}

func hasOutputVariables(path string) bool {
	return strings.Contains(path, "$root") || strings.Contains(path, "$dir") || strings.Contains(path, "$name")
}

func tempPattern(kind string) string {
	return fmt.Sprintf("%s%s-%d-*", tempPrefix, kind, os.Getpid())
}

func cleanStaleTemp(base string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, entry := range entries {
		parts := strings.SplitN(entry.Name(), "-", 4)
		if !entry.IsDir() || len(parts) != 4 || parts[0]+"-" != tempPrefix {
			continue
		}
		pid, err := strconv.Atoi(parts[2])
		if err != nil || pid == os.Getpid() || processAlive(pid) {
			continue
		}
		os.RemoveAll(filepath.Join(base, entry.Name()))
	}
}
