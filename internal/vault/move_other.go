//go:build !darwin && !linux && !windows

package vault

import "errors"

func moveEntry(from, to string) error {
	return errors.ErrUnsupported
}
