package vault

import "golang.org/x/sys/unix"

func moveEntry(from, to string) error {
	return unix.RenamexNp(from, to, unix.RENAME_EXCL)
}
