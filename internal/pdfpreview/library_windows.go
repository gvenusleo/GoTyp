//go:build amd64 || arm64

package pdfpreview

import (
	"github.com/ebitengine/purego"
	"golang.org/x/sys/windows"
)

type cLong = uint32

func openLibrary(path string) (library, error) {
	handle, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return library{}, err
	}
	return library{
		symbol: func(name string, fn any) error {
			address, err := windows.GetProcAddress(handle, name)
			if err == nil {
				purego.RegisterFunc(fn, address)
			}
			return err
		},
		close: func() { windows.FreeLibrary(handle) },
	}, nil
}
