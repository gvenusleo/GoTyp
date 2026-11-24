//go:build (!darwin && !linux && !windows) || (!amd64 && !arm64)

package pdfpreview

import "errors"

type cLong = uint64

func openLibrary(string) (library, error) { return library{}, errors.ErrUnsupported }
