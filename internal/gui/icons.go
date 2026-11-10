package gui

import (
	"embed"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
)

// 图标来自 Lucide 图标集（https://lucide.dev，ISC 许可）。
//
//go:embed icons/*.svg
var iconFiles embed.FS

var (
	iconPanelLeft      = icon("panel-left")
	iconPlus           = icon("plus")
	iconSearch         = icon("search")
	iconFolder         = icon("folder")
	iconFolderOpen     = icon("folder-open")
	iconFile           = icon("file")
	iconFileText       = icon("file-text")
	iconFileType       = icon("file-type")
	iconFileCode       = icon("file-code")
	iconFileJSON       = icon("file-json")
	iconFileCog        = icon("file-cog")
	iconFileTerminal   = icon("file-terminal")
	iconFileImage      = icon("file-image")
	iconFileMusic      = icon("file-music")
	iconFileVideo      = icon("file-video-camera")
	iconFileArchive    = icon("file-archive")
	iconFileSheet      = icon("file-spreadsheet")
	iconPencil         = icon("pencil-line")
	iconColumns        = icon("columns-2")
	iconEye            = icon("eye")
	iconEllipsis       = icon("ellipsis")
	iconCopy           = icon("copy")
	iconChevronsUpDown = icon("chevrons-up-down")
	iconChevronUp      = icon("chevron-up")
	iconAlert          = icon("circle-alert")
	iconCheck          = icon("check")
	iconCircleX        = icon("circle-x")
	iconWarning        = icon("triangle-alert")
)

// fileIconExts 把小写扩展名对应到文件图标，没有列出的用 iconFile。只分几大类，
// 让文件树一眼看出笔记、代码、图片和其他资料，不追求每种语言一个图标。
var fileIconExts = map[string]*ui.SVG{}

func init() {
	groups := []struct {
		svg  *ui.SVG
		exts string
	}{
		{iconFileText, ".md .markdown .txt .rst .adoc .org .tex .bib"},
		{iconFileType, ".typ"},
		{iconFileCode, ".go .c .h .cc .cpp .hpp .rs .py .rb .js .mjs .cjs .ts .tsx .jsx .java .kt " +
			".swift .m .cs .php .lua .dart .scala .hs .ex .exs .zig .html .htm .css .scss .vue .svelte .sql"},
		{iconFileJSON, ".json .jsonc .json5 .jsonl"},
		{iconFileCog, ".yaml .yml .toml .ini .conf .cfg .env .xml .plist .properties .lock .mod .sum"},
		{iconFileTerminal, ".sh .bash .zsh .fish .ps1 .bat .cmd"},
		{iconFileImage, ".png .jpg .jpeg .gif .webp .svg .ico .bmp .tif .tiff .heic .heif .avif"},
		{iconFileMusic, ".mp3 .wav .flac .aac .m4a .ogg .opus .aiff"},
		{iconFileVideo, ".mp4 .mov .mkv .webm .avi .m4v"},
		{iconFileArchive, ".zip .tar .gz .tgz .bz2 .xz .7z .rar .zst .dmg .iso"},
		{iconFileSheet, ".csv .tsv .xls .xlsx .numbers .ods"},
	}
	for _, g := range groups {
		for _, ext := range strings.Fields(g.exts) {
			fileIconExts[ext] = g.svg
		}
	}
}

// fileIcon 返回文件 id 的类型图标。
func fileIcon(id string) *ui.SVG {
	if svg, ok := fileIconExts[strings.ToLower(path.Ext(id))]; ok {
		return svg
	}
	return iconFile
}

func icon(name string) *ui.SVG {
	data, err := iconFiles.ReadFile("icons/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(data)
}

// ghostButton 是无边框的方形图标按钮：静止时只显示图标，悬停和按下时出现浅底。
func ghostButton(c *ui.Context, label string, svg *ui.SVG) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Label(label).Tooltip(label).Size(barButton, barButton).Radius(t.Radius).
		Justify(ui.Center).AlignItems(ui.Center).Transition(hoverMotion)
	color := t.TextMuted
	switch {
	case b.Pressed():
		b.Background(pressFill(t))
		color = t.Text
	case b.Hovered():
		b.Background(buttonFill(t))
		color = t.Text
	}
	return b.Children(func() {
		ui.Icon(c, svg).FontSize(iconButtonSize).TextColor(color)
	})
}
