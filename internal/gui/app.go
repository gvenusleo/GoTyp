// Package gui 组织 GoTyp 的 Native UI、编辑会话和后台任务。
package gui

import (
	"context"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	"github.com/egoist/mygo/ui"

	"gotyp/internal/tinymist"
	"gotyp/internal/vault"
)

const (
	modeEdit = iota
	modeSplit
	modePreview
)

var modeNames = []string{"edit", "split", "preview"}

// App 的全部字段都只在主线程读写，后台任务通过 Window.Update 应用结果。
type App struct {
	ctx       context.Context
	cancel    context.CancelFunc
	publishFn func(func())
	win       *mygo.Window
	services  ui.Services

	statePath    string
	state        vault.State
	stateSaving  bool
	statePending bool

	vault       *vault.Vault
	tree        *vault.Tree
	watchCancel context.CancelFunc
	// lsp 是当前仓库的 tinymist 客户端，lspQueue 按顺序发送它的打开和关闭，见 complete.go。
	lsp         *tinymist.Client
	lspQueue    *serialQueue
	scanRunning bool
	scanAgain   bool

	outline     ui.OutlineState[string]
	chosen      ui.Selection[string]
	selectedRow int

	sidebarWidth float32
	// sidebarShown 是侧栏展开程度：1 完全展开，0 完全收起，动画中取中间值。
	sidebarShown  float32
	editorPercent float32
	sidebarHidden bool
	mode          int
	lineNumbers   bool
	current       *session
	// viewing 是当前打开的图片或无法以文本打开的文件，与 current 至多一个不为空。
	viewing *viewer

	// focus 只在会话内生效，不写入工作区状态。
	focus bool

	// busy 表示正在切换、加载或改动文件，期间不能开始新的切换。
	busy         bool
	openingVault bool
	closing      bool
	quitting     bool
	// discarding 表示用户已确认放弃无法保存的修改，关闭和退出不再拦截。
	discarding bool
	generation uint64
	menuReady  bool

	palette  paletteState
	rename   renameState
	settings settingsState
	themes   themeCache

	toast string
	err   string
}

func newApp() *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{ctx: ctx, cancel: cancel, sidebarWidth: sidebarDefault, sidebarShown: 1, editorPercent: 50, mode: modeSplit, lineNumbers: true, selectedRow: -1}
}

// Run 启动 GoTyp 桌面应用。
func Run() error {
	mygo.App.SetLanguages("zh-Hans")
	extendToolPath()
	a := newApp()
	if dir, err := os.UserConfigDir(); err == nil {
		a.statePath = filepath.Join(dir, "GoTyp", "state.json")
		// 状态文件很小，在窗口出现前同步读取，避免启动时先闪一下空状态。
		a.state, _ = vault.LoadState(a.statePath)
	}
	// 在 Run 之前设置，窗口一出现就是保存的外观。
	mygo.Theme.SetSource(themeSource(a.state.Appearance.Theme))
	mygo.App.OnBeforeQuit(a.beforeQuit)
	mygo.App.OnQuit(a.cancel)
	mygo.App.WhenReady(func() {
		a.setMenu()
		a.menuReady = true
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title: "GoTyp", Width: 1200, Height: 800,
			MinWidth: 720, MinHeight: 480, StateKey: "main",
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 16, Y: (headerHeight - 14) / 2},
			Content:              ui.View(a.view),
		})
		a.win.OnClose(a.beforeClose)
		if len(a.state.Recent) > 0 {
			a.startOpenVault(a.state.Recent[0])
		}
	})
	return mygo.App.Run()
}

func (a *App) setMenu() {
	action := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { fn() }
	}
	chooseMode := func(mode int) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { a.setMode(mode) }
	}
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			mygo.Separator(),
			{Label: "设置…", Accelerator: "CmdOrCtrl+,", Click: action(a.openSettings)},
			mygo.Separator(),
			{Role: mygo.RoleServices},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Label: "文件", Submenu: []*mygo.MenuItem{
			{Label: "新建 Markdown 笔记", Accelerator: "CmdOrCtrl+N", Click: action(func() { a.newNote(vault.KindMarkdown) })},
			{Label: "新建 Typst 笔记", Accelerator: "CmdOrCtrl+Shift+N", Click: action(func() { a.newNote(vault.KindTypst) })},
			{Label: "快速打开…", Accelerator: "CmdOrCtrl+P", Click: action(a.openPalette)},
			{Label: "打开文件夹…", Accelerator: "CmdOrCtrl+O", Click: action(a.openVaultDialog)},
			mygo.Separator(),
			{Label: "保存", Accelerator: "CmdOrCtrl+S", Click: action(a.saveCurrent)},
			{Label: "导出 PDF…", Accelerator: "CmdOrCtrl+E", Click: action(a.exportPDFDialog)},
			mygo.Separator(),
			{Label: "在文件管理器中显示", Click: action(a.revealCurrent)},
			{Label: "复制路径", Accelerator: "CmdOrCtrl+Shift+C", Click: action(a.copyCurrentPath)},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: "视图", Submenu: []*mygo.MenuItem{
			{Label: "编辑", Type: mygo.MenuItemRadio, Accelerator: "CmdOrCtrl+1", Checked: a.mode == modeEdit, Click: chooseMode(modeEdit)},
			{Label: "分屏", Type: mygo.MenuItemRadio, Accelerator: "CmdOrCtrl+2", Checked: a.mode == modeSplit, Click: chooseMode(modeSplit)},
			{Label: "预览", Type: mygo.MenuItemRadio, Accelerator: "CmdOrCtrl+3", Checked: a.mode == modePreview, Click: chooseMode(modePreview)},
			mygo.Separator(),
			{Label: "显示侧栏", Type: mygo.MenuItemCheckbox, Accelerator: "CmdOrCtrl+B", Checked: !a.sidebarHidden, Click: action(a.toggleSidebar)},
			{Label: "显示行号", Type: mygo.MenuItemCheckbox, Checked: a.lineNumbers, Click: action(a.toggleLineNumbers)},
			{Label: "专注模式", Type: mygo.MenuItemCheckbox, Accelerator: "CmdOrCtrl+Alt+F", Checked: a.focus, Click: action(a.toggleFocus)},
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
		}},
		{Role: mygo.RoleWindowMenu},
	}))
}

// refreshMenu 更新原生菜单的勾选状态。测试环境没有安装菜单，直接跳过。
func (a *App) refreshMenu() {
	if a.menuReady {
		a.setMenu()
	}
}

func (a *App) setMode(mode int) {
	if a.mode == mode {
		return
	}
	a.mode = mode
	a.modeChanged()
}

// modeChanged 切到编辑模式时保留常驻编译进程，切回预览时无需重新启动。
func (a *App) modeChanged() {
	a.persistWorkspace()
	a.refreshMenu()
}

func (a *App) toggleFocus() {
	if a.current == nil {
		return
	}
	a.focus = !a.focus
	a.refreshMenu()
}

func (a *App) toggleLineNumbers() {
	a.lineNumbers = !a.lineNumbers
	if a.current != nil {
		a.applyEditorPrefs(a.current)
	}
	a.persistWorkspace()
	a.refreshMenu()
}

func (a *App) toggleSidebar() {
	if a.vault == nil {
		return
	}
	a.sidebarHidden = !a.sidebarHidden
	a.persistWorkspace()
	a.refreshMenu()
}

func (a *App) publish(fn func()) {
	if a.publishFn != nil {
		a.publishFn(fn)
	} else if a.win != nil {
		a.win.Update(fn)
	}
}

func (a *App) notify(message string) {
	a.toast = message
}

func (a *App) beforeClose(e *mygo.CloseEvent) {
	if a.discarding || a.current == nil || (!a.current.dirty() && !a.current.saving) {
		return
	}
	e.PreventDefault()
	if a.closing || a.quitting {
		return
	}
	a.closing = true
	a.switchAfterSave(func() {
		a.closing = false
		a.win.Close()
	})
}

func (a *App) beforeQuit(e *mygo.QuitEvent) {
	if a.discarding || a.current == nil || (!a.current.dirty() && !a.current.saving) {
		return
	}
	e.PreventDefault()
	if a.closing || a.quitting {
		return
	}
	a.quitting = true
	a.switchAfterSave(func() { mygo.App.Quit() })
}
