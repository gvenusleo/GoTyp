package gui

import (
	"math"

	"github.com/egoist/mygo/ui"

	"gotyp/internal/vault"
)

// Typst 预览缩放只放大已编译的 PNG，不重新编译；上限受 192 ppi 的清晰度限制。
const (
	minPreviewZoom = 0.5
	maxPreviewZoom = 2
	zoomStep       = 0.1
	// wheelZoomRate 把触控板滑动的距离换成缩放倍数；鼠标滚轮一格的距离各平台不同，改为每格一档。
	wheelZoomRate = 0.005
)

// zoomFactor 返回 Typst 预览的缩放，零值表示 100%。
func (a *App) zoomFactor() float32 {
	if a.previewZoom == 0 {
		return 1
	}
	return a.previewZoom
}

func (a *App) setPreviewZoom(z float32) {
	a.previewZoom = min(max(z, minPreviewZoom), maxPreviewZoom)
}

// zoomable 报告当前是否显示着 Typst 页面。没有页面时缩放看不到效果，状态栏也不显示比例，不能悄悄改动。
func (a *App) zoomable() bool {
	s := a.current
	return s != nil && s.kind == vault.KindTypst && a.mode != modeEdit && len(s.previewPages) > 0
}

// stepPreviewZoom 先对齐到 10% 的档位再前进一档，捏合后的比例也能回到整数百分比。
func (a *App) stepPreviewZoom(dir int) {
	if a.zoomable() {
		a.setPreviewZoom(nextZoomStep(a.zoomFactor(), dir))
	}
}

func nextZoomStep(z float32, dir int) float32 {
	steps := float64(z) / zoomStep
	if dir > 0 {
		steps = math.Floor(steps+0.01) + 1
	} else {
		steps = math.Ceil(steps-0.01) - 1
	}
	return float32(steps * zoomStep)
}

func (a *App) resetPreviewZoom() {
	if a.zoomable() {
		a.previewZoom = 1
	}
}

// zoomInput 接管预览区上的捏合和带修饰键的滚轮，其余输入留给列表滚动。
func (a *App) zoomInput(ev ui.InputEvent) bool {
	switch {
	case ev.Kind == ui.InputZoom:
		a.setPreviewZoom(a.zoomFactor() * ev.Scale)
		return true
	case ev.Kind == ui.InputScroll && ev.Mods&(ui.Ctrl|ui.Super) != 0 && ev.DY != 0:
		switch {
		case ev.Precise:
			a.setPreviewZoom(a.zoomFactor() * float32(math.Exp(float64(-ev.DY)*wheelZoomRate)))
		case ev.DY < 0:
			a.setPreviewZoom(nextZoomStep(a.zoomFactor(), 1))
		default:
			a.setPreviewZoom(nextZoomStep(a.zoomFactor(), -1))
		}
		return true
	}
	return false
}
