package vault

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

// FileStat 是读取或成功保存时的文件状态。FileInfo 只在比较时使用，不进入持久化格式。
type FileStat struct {
	exists  bool
	info    os.FileInfo
	modTime time.Time
	size    int64
}

func newFileStat(info os.FileInfo) FileStat {
	return FileStat{exists: true, info: info, modTime: info.ModTime(), size: info.Size()}
}

func (s FileStat) same(info os.FileInfo) bool {
	return s.exists && info != nil && os.SameFile(s.info, info) &&
		s.modTime.Equal(info.ModTime()) && s.size == info.Size()
}

// Read 以文本读取一个文件，并返回后续保存需要核对的文件状态。超过 MaxTextBytes 时返回
// ErrTooLarge，不是合法 UTF-8 或含有 NUL 时返回 ErrNotText：这样的内容进了编辑器，
// 保存时会被改写，原文件就坏了。
func (v *Vault) Read(id string) (string, FileStat, error) {
	data, st, err := v.ReadBytes(id, MaxTextBytes)
	if err != nil {
		return "", FileStat{}, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", FileStat{}, ErrNotText
	}
	return string(data), st, nil
}

// ReadBytes 读取一个普通文件的全部内容，超过 limit 字节时返回 ErrTooLarge。
// 读取期间文件被改动时返回 ErrConflict，调用方不会拿到半新半旧的内容。
func (v *Vault) ReadBytes(id string, limit int64) ([]byte, FileStat, error) {
	name, err := v.Resolve(id)
	if err != nil {
		return nil, FileStat{}, err
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, FileStat{}, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, FileStat{}, err
	}
	if !before.Mode().IsRegular() {
		return nil, FileStat{}, fmt.Errorf("%s 不是普通文件", id)
	}
	if before.Size() > limit {
		return nil, FileStat{}, ErrTooLarge
	}
	// 文件可能在 Stat 之后长大，读取本身也要有上限。
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, FileStat{}, err
	}
	if int64(len(data)) > limit {
		return nil, FileStat{}, ErrTooLarge
	}
	after, err := f.Stat()
	if err != nil {
		return nil, FileStat{}, err
	}
	if !newFileStat(before).same(after) {
		return nil, FileStat{}, ErrConflict
	}
	return data, newFileStat(after), nil
}

// Create 创建一篇空笔记。已有文件不会被截断。
func (v *Vault) Create(id string) (FileStat, error) {
	if _, ok := KindFor(id); !ok {
		return FileStat{}, ErrUnsupported
	}
	name, err := v.Resolve(id)
	if err != nil {
		return FileStat{}, err
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return FileStat{}, err
	}
	if err := f.Close(); err != nil {
		return FileStat{}, err
	}
	info, err := os.Stat(name)
	if err != nil {
		return FileStat{}, err
	}
	return newFileStat(info), nil
}

// Write 在文件状态未变化时原子保存内容。
func (v *Vault) Write(id string, expected FileStat, content io.WriterTo) (FileStat, error) {
	name, err := v.Resolve(id)
	if err != nil {
		return FileStat{}, err
	}
	if err := checkExpected(name, expected); err != nil {
		return FileStat{}, err
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(name); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return FileStat{}, err
	}
	if err := writeAtomic(name, mode, content, func() error {
		return checkExpected(name, expected)
	}); err != nil {
		return FileStat{}, err
	}
	info, err := os.Stat(name)
	if err != nil {
		return FileStat{}, err
	}
	return newFileStat(info), nil
}

// WriteCopy 把内容原子写到用户明确选择的副本路径。
func WriteCopy(name string, content io.WriterTo) error {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(name); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return writeAtomic(name, mode, content, nil)
}

func checkExpected(name string, expected FileStat) error {
	info, err := os.Stat(name)
	if !expected.exists {
		switch {
		case err == nil:
			return ErrConflict
		case errors.Is(err, fs.ErrNotExist):
			return nil
		default:
			return err
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return ErrDeleted
	}
	if err != nil {
		return err
	}
	if !expected.same(info) {
		return ErrConflict
	}
	return nil
}

// writeAtomic 在同一目录写临时文件，beforeRename 在替换前再次核对写冲突。
func writeAtomic(name string, mode fs.FileMode, content io.WriterTo, beforeRename func() error) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), ".gotyp-save-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = content.WriteTo(tmp); err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	err = errors.Join(err, tmp.Close())
	if err == nil && beforeRename != nil {
		err = beforeRename()
	}
	if err == nil {
		err = os.Rename(tmpName, name)
	}
	if err == nil {
		syncDir(filepath.Dir(name))
	}
	return err
}

// syncDir 让改名写入目录项，断电后也能保留。不支持目录同步的平台忽略错误，改名本身已经完成。
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// Check 报告笔记在磁盘上是否仍是 expected 记录的版本：未变化返回 nil，
// 被修改返回 ErrConflict，被删除返回 ErrDeleted。
func (v *Vault) Check(id string, expected FileStat) error {
	name, err := v.Resolve(id)
	if err != nil {
		return err
	}
	return checkExpected(name, expected)
}

// CurrentStat 返回笔记此刻的文件状态，文件不存在时返回零值。
// 用户明确选择覆盖外部版本或重新创建已删除的文件时，以它作为下一次写入的预期状态。
func (v *Vault) CurrentStat(id string) (FileStat, error) {
	name, err := v.Resolve(id)
	if err != nil {
		return FileStat{}, err
	}
	info, err := os.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return FileStat{}, nil
	}
	if err != nil {
		return FileStat{}, err
	}
	if !info.Mode().IsRegular() {
		return FileStat{}, fmt.Errorf("%s 不是普通文件", id)
	}
	return newFileStat(info), nil
}
