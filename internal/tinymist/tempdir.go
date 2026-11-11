package tinymist

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// tempPrefix 是 GoTyp 临时目录的前缀，后面跟创建者的进程号。
const tempPrefix = "gotyp-"

var cleanOnce sync.Once

// makeTemp 创建带进程号的临时目录。第一次调用时顺带清理已退出进程留下的目录：
// 应用退出或崩溃时后台任务来不及删除自己的目录，进程号让多个实例不会误删彼此的目录。
func makeTemp(kind string) (string, error) {
	cleanOnce.Do(cleanStaleTemp)
	return os.MkdirTemp("", fmt.Sprintf("%s%s-%d-*", tempPrefix, kind, os.Getpid()))
}

func cleanStaleTemp() {
	entries, err := os.ReadDir(os.TempDir())
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
		os.RemoveAll(filepath.Join(os.TempDir(), entry.Name()))
	}
}
