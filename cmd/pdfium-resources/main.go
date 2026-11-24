package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const release = "https://github.com/bblanchon/pdfium-binaries/releases/download/chromium/8179/"

type archive struct{ name, digest, library, libraryDigest string }

var archives = map[string]archive{
	"darwin/amd64":  {"mac-x64", "b29bbfe37efcad5b443fa3556c0485ec96fc1992d5b19d84df49d48fbed1d0d2", "lib/libpdfium.dylib", "8e88cab152d9ea18c1d59135ecfca2e3235ddb896c1c564cd720f9a6f08c67fc"},
	"darwin/arm64":  {"mac-arm64", "44b7c6f55dd33454d0909728bd0adf6c86a8e141b36f07a6b6f50e228e1dfb46", "lib/libpdfium.dylib", "fa68216ccec6dbdd3b9f46d7a8c921ebfdf942dd542c8f816738b87fcd0018b8"},
	"linux/amd64":   {"linux-x64", "b4766a6bf2856efbcc6c3cc7d6ae9022bdc6bb36ddc1acdd92d1f1873f773f4b", "lib/libpdfium.so", "2132a56ef078b6f27ba38849ac7ea13cac80f808e9f054dc5a8450075397a393"},
	"linux/arm64":   {"linux-arm64", "18bb6d568257b28d14f6c3b8bfb48caf73d4615b319ca4d43b424c2a03431d9f", "lib/libpdfium.so", "c7a2be4bf1a1aa601a60ff951c1fc2db9a0840d0c5cfd83cdc389b34ed16424d"},
	"windows/amd64": {"win-x64", "836a77c9053f624839c66010cddea7b675628a36b8831d2330178bb169d241b6", "bin/pdfium.dll", "b9324fe77556753e24443b8ff1d03b39a3aa01ccd6e56e7debb82922438e8965"},
	"windows/arm64": {"win-arm64", "9c659bf0cd9bdefcbf7ec0e4b1675c2961e0fdfd4ce097b411efd9742832fe06", "bin/pdfium.dll", "c46794087ccaa8023e7960743482820140341bf3a7a1650dec33d2af0619a114"},
}

func main() {
	platform := flag.String("platform", runtime.GOOS+"/"+runtime.GOARCH, "目标平台，格式为 GOOS/GOARCH，多个目标用逗号分隔")
	flag.Parse()
	for target := range strings.SplitSeq(*platform, ",") {
		spec, ok := archives[target]
		if !ok {
			log.Fatalf("PDFium 暂不支持 %s", target)
		}
		if err := prepare(target, spec); err != nil {
			log.Fatalf("准备 %s 的 PDFium 失败：%v", target, err)
		}
	}
}

func prepare(target string, spec archive) error {
	parent := filepath.Join("resources", strings.Replace(target, "/", "-", 1))
	output := filepath.Join(parent, "pdfium")
	if prepared(output, spec) {
		return nil
	}
	data, err := download(spec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".pdfium-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	next := filepath.Join(stage, "next")
	if err := extract(next, spec.library, data); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(next, archiveMarker), []byte(spec.digest), 0o644); err != nil {
		return err
	}
	if !prepared(next, spec) {
		return fmt.Errorf("PDFium 动态库的 SHA-256 校验失败")
	}
	return install(next, output, spec)
}

const archiveMarker = ".archive-sha256"

func prepared(dir string, spec archive) bool {
	digest, err := os.ReadFile(filepath.Join(dir, archiveMarker))
	if err != nil || string(digest) != spec.digest {
		return false
	}
	if checkFiles(dir, spec.library) != nil {
		return false
	}
	path := filepath.Join(dir, filepath.Base(spec.library))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, io.LimitReader(file, 32<<20))
	return err == nil && fmt.Sprintf("%x", hash.Sum(nil)) == spec.libraryDigest
}

func install(next, output string, spec archive) error {
	if err := os.Rename(next, output); err == nil || prepared(output, spec) {
		return nil
	}
	// 保留上一份目录，换入失败时可以恢复。
	previous := filepath.Join(filepath.Dir(next), "previous")
	moveErr := os.Rename(output, previous)
	if moveErr != nil && !os.IsNotExist(moveErr) {
		return moveErr
	}
	if err := os.Rename(next, output); err != nil && !prepared(output, spec) {
		if moveErr == nil {
			return errors.Join(err, os.Rename(previous, output))
		}
		return err
	}
	return nil
}

func download(spec archive) ([]byte, error) {
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(release + "pdfium-" + spec.name + ".tgz")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != spec.digest {
		return nil, fmt.Errorf("PDFium 归档的 SHA-256 校验失败")
	}
	return data, nil
}

func extract(dir, library string, data []byte) error {
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer compressed.Close()
	files := tar.NewReader(compressed)
	for {
		entry, err := files.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := entry.Name
		if entry.Typeflag != tar.TypeReg || !fs.ValidPath(name) || strings.Contains(name, `\`) {
			continue
		}
		switch {
		case name == library:
			name = filepath.Base(name)
		case name != "LICENSE" && !strings.HasPrefix(name, "licenses/"):
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		body, err := io.ReadAll(files)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return err
		}
	}
	return checkFiles(dir, library)
}

func checkFiles(dir, library string) error {
	for _, name := range []string{filepath.Base(library), "LICENSE", "licenses"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}
