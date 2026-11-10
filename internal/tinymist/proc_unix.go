//go:build unix

package tinymist

import "syscall"

// processAlive 用 0 号信号探测进程是否存在；没有权限发送信号说明进程存在但属于他人。
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
