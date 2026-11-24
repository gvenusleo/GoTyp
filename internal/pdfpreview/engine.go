package pdfpreview

import (
	"fmt"
	"path/filepath"
	"runtime"
	"unsafe"

	"github.com/egoist/mygo"
)

// ABI 以 PDFium chromium/8179 的 public 头文件为准。
// unsigned long 在 Unix LP64 上是 64 位，在 Windows LLP64 上是 32 位。
type engine struct {
	initLibrary     func()
	loadDocument    func(*byte, uintptr, *byte) uintptr
	lastError       func() cLong
	closeDocument   func(uintptr)
	pageCount       func(uintptr) int32
	userPermissions func(uintptr) cLong
	pageSize        func(uintptr, int32, *pageSize) int32
	loadPage        func(uintptr, int32) uintptr
	closePage       func(uintptr)
	createBitmap    func(int32, int32, int32, unsafe.Pointer, int32) uintptr
	fillBitmap      func(uintptr, int32, int32, int32, int32, cLong) int32
	destroyBitmap   func(uintptr)
	renderBitmap    func(uintptr, uintptr, int32, int32, int32, int32, int32, int32)
	pageToDevice    func(uintptr, int32, int32, int32, int32, int32, float64, float64, *int32, *int32) int32
	loadText        func(uintptr) uintptr
	closeText       func(uintptr)
	charCount       func(uintptr) int32
	charBox         func(uintptr, int32, *float64, *float64, *float64, *float64) int32
	getText         func(uintptr, int32, int32, *uint16) int32
	nextLink        func(uintptr, *int32, *uintptr) int32
	linkRect        func(uintptr, *pdfRect) int32
	linkDest        func(uintptr, uintptr) uintptr
	linkAction      func(uintptr) uintptr
	actionType      func(uintptr) cLong
	actionDest      func(uintptr, uintptr) uintptr
	actionURI       func(uintptr, uintptr, *byte, cLong) cLong
	destPage        func(uintptr, uintptr) int32
	destView        func(uintptr, *cLong, *float32) cLong
	destLocation    func(uintptr, *int32, *int32, *int32, *float32, *float32, *float32) int32
	pageBounds      func(uintptr, *pdfRect) int32
	loadWebLinks    func(uintptr) uintptr
	closeWebLinks   func(uintptr)
	webLinkCount    func(uintptr) int32
	webLinkURL      func(uintptr, int32, *uint16, int32) int32
	webRectCount    func(uintptr, int32) int32
	webRect         func(uintptr, int32, int32, *float64, *float64, *float64, *float64) int32
}

type library struct {
	symbol func(string, any) error
	close  func()
}

func targetLibrary() (string, error) {
	if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
		switch runtime.GOOS {
		case "darwin":
			return "libpdfium.dylib", nil
		case "linux":
			return "libpdfium.so", nil
		case "windows":
			return "pdfium.dll", nil
		}
	}
	return "", fmt.Errorf("PDFium 暂不支持 %s/%s", runtime.GOOS, runtime.GOARCH)
}

func resourceLibrary() (string, error) {
	name, err := targetLibrary()
	if err != nil {
		return "", err
	}
	dir, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(dir, "pdfium", name))
}

func loadEngine(path string) (*engine, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("PDFium 动态库需要绝对路径")
	}
	lib, err := openLibrary(path)
	if err != nil {
		return nil, fmt.Errorf("无法加载 PDFium：%w", err)
	}
	e := new(engine)
	for _, symbol := range e.symbols() {
		if err := lib.symbol(symbol.name, symbol.fn); err != nil {
			lib.close()
			return nil, fmt.Errorf("PDFium 缺少 %s：%w", symbol.name, err)
		}
	}
	e.initLibrary()
	return e, nil
}

type binding struct {
	name string
	fn   any
}

func (e *engine) symbols() []binding {
	return []binding{
		{"FPDF_InitLibrary", &e.initLibrary},
		{"FPDF_LoadMemDocument64", &e.loadDocument},
		{"FPDF_GetLastError", &e.lastError},
		{"FPDF_CloseDocument", &e.closeDocument},
		{"FPDF_GetPageCount", &e.pageCount},
		{"FPDF_GetDocUserPermissions", &e.userPermissions},
		{"FPDF_GetPageSizeByIndexF", &e.pageSize},
		{"FPDF_LoadPage", &e.loadPage},
		{"FPDF_ClosePage", &e.closePage},
		{"FPDFBitmap_CreateEx", &e.createBitmap},
		{"FPDFBitmap_FillRect", &e.fillBitmap},
		{"FPDFBitmap_Destroy", &e.destroyBitmap},
		{"FPDF_RenderPageBitmap", &e.renderBitmap},
		{"FPDF_PageToDevice", &e.pageToDevice},
		{"FPDFText_LoadPage", &e.loadText},
		{"FPDFText_ClosePage", &e.closeText},
		{"FPDFText_CountChars", &e.charCount},
		{"FPDFText_GetCharBox", &e.charBox},
		{"FPDFText_GetText", &e.getText},
		{"FPDFLink_Enumerate", &e.nextLink},
		{"FPDFLink_GetAnnotRect", &e.linkRect},
		{"FPDFLink_GetDest", &e.linkDest},
		{"FPDFLink_GetAction", &e.linkAction},
		{"FPDFAction_GetType", &e.actionType},
		{"FPDFAction_GetDest", &e.actionDest},
		{"FPDFAction_GetURIPath", &e.actionURI},
		{"FPDFDest_GetDestPageIndex", &e.destPage},
		{"FPDFDest_GetView", &e.destView},
		{"FPDFDest_GetLocationInPage", &e.destLocation},
		{"FPDF_GetPageBoundingBox", &e.pageBounds},
		{"FPDFLink_LoadWebLinks", &e.loadWebLinks},
		{"FPDFLink_CloseWebLinks", &e.closeWebLinks},
		{"FPDFLink_CountWebLinks", &e.webLinkCount},
		{"FPDFLink_GetURL", &e.webLinkURL},
		{"FPDFLink_CountRects", &e.webRectCount},
		{"FPDFLink_GetRect", &e.webRect},
	}
}
