//go:build (darwin || linux) && (amd64 || arm64)

package pdfpreview

import "github.com/ebitengine/purego"

type cLong = uint64

func openLibrary(path string) (library, error) {
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return library{}, err
	}
	return library{
		symbol: func(name string, fn any) error {
			address, err := purego.Dlsym(handle, name)
			if err == nil {
				purego.RegisterFunc(fn, address)
			}
			return err
		},
		close: func() { purego.Dlclose(handle) },
	}, nil
}
