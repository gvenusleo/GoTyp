package gui

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"gotyp/internal/vault"
)

// exportTimeout 是导出 PDF 的上限，文档中有死循环时不会一直挂着。
const exportTimeout = 2 * time.Minute

// configurePreview 为每篇笔记建立独立图片缓存，避免不同目录的同名图片串用。
func (a *App) configurePreview(s *session) {
	// 本地笔记不关联 GitHub 仓库，@用户名和 #123 无处可链，按普通文字显示。
	s.md = markdown.Options{Extensions: parser.Default &^ (parser.ExtMentions | parser.ExtReferences)}
	if a.vault != nil {
		if name, err := a.vault.Resolve(s.id); err == nil {
			s.md.LoadImage = markdown.FileImages(filepath.Dir(name))
		}
	}
	s.md.OnLink = func(dest string) { a.openMarkdownLink(s, dest) }
}

func (a *App) exportPDFDialog() {
	s := a.current
	if s == nil || s.kind != vault.KindTypst {
		return
	}
	if !a.typstReady() {
		a.notify("tinymist 不可用，无法导出 PDF")
		return
	}
	if s.exporting {
		a.notify("正在导出 PDF")
		return
	}
	name, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent: a.win, Title: "导出 PDF",
		DefaultPath:       filepath.Join(a.vault.Root(), filepath.FromSlash(strings.TrimSuffix(s.id, path.Ext(s.id))+".pdf")),
		Filters:           []mygo.FileFilter{{Name: "PDF", Extensions: []string{"pdf"}}},
		CreateDirectories: true,
	})
	if err != nil || name == "" {
		return
	}
	client := a.lsp
	doc := s.lspDocument()
	s.exporting = true
	// 导出按当前快照另行编译，不暂停预览。
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, exportTimeout)
		defer cancel()
		data, err := client.ExportPDF(ctx, doc)
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("编译超时")
		}
		if err == nil {
			err = vault.WriteCopy(name, bytes.NewReader(data))
		}
		a.publish(func() {
			s.exporting = false
			if err != nil {
				a.notify("PDF 导出失败：" + err.Error())
			} else {
				a.notify("PDF 已导出")
			}
		})
	}()
}

func (a *App) openMarkdownLink(s *session, dest string) {
	if a.current != s {
		return
	}
	if strings.HasPrefix(dest, "#") {
		s.md.Anchor = dest
		s.md.AnchorVersion++
		return
	}
	u, err := url.Parse(dest)
	if err != nil {
		a.notify("无法打开链接")
		return
	}
	if u.Scheme != "" {
		if ui.SafeToOpen(dest) {
			a.services.OpenURL(dest)
		} else {
			a.notify("不支持此链接")
		}
		return
	}
	if u.Path == "" || strings.HasPrefix(u.Path, "/") {
		a.notify("无法打开笔记链接")
		return
	}
	id := path.Clean(path.Join(path.Dir(s.id), u.Path))
	if id == "." || strings.HasPrefix(id, "../") {
		a.notify("链接超出笔记仓库")
		return
	}
	if _, ok := vault.KindFor(id); !ok {
		a.openVaultFile(id)
		return
	}
	if a.tree == nil {
		return
	}
	if _, ok := a.tree.Titles[id]; !ok {
		a.notify("链接的笔记不存在")
		return
	}
	a.openNote(id)
}

// openVaultFile 用系统默认应用打开仓库内的文件，例如链接的 PDF 或应用内无法显示的文件。路径解析在后台进行。
func (a *App) openVaultFile(id string) {
	v := a.vault
	if v == nil {
		return
	}
	go func() {
		name, err := v.Resolve(id)
		if err == nil {
			var info os.FileInfo
			if info, err = os.Stat(name); err == nil && !info.Mode().IsRegular() {
				err = errors.New("不是文件")
			}
		}
		a.publish(func() {
			if a.vault != v {
				return
			}
			if err != nil {
				a.notify("无法打开文件")
				return
			}
			if err := mygo.Shell.OpenPath(name); err != nil {
				a.notify("无法打开文件：" + err.Error())
			}
		})
	}()
}
