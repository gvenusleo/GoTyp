//go:build !unix

package tinymist

// processAlive 在非 Unix 平台上无法可靠探测，保守地认为进程仍在运行，不清理。
func processAlive(int) bool { return true }
