package gui

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/png"

	"github.com/egoist/mygo/ui"
)

// pageTone 是 Typst 预览页面的配色，零值表示浅色主题，页面原样显示。
// 它在主线程从主题取出，作为值传给后台解码，后台不读取主题。
type pageTone struct {
	dark   bool
	bg, fg [3]uint8
}

func toneOf(t *ui.Theme) pageTone {
	if t == nil || !t.Dark {
		return pageTone{}
	}
	return pageTone{
		dark: true,
		bg:   [3]uint8{t.Background.R, t.Background.G, t.Background.B},
		fg:   [3]uint8{t.Text.R, t.Text.G, t.Text.B},
	}
}

func (a *App) pageTone() pageTone {
	if !a.state.Appearance.RecolorPreview {
		return pageTone{}
	}
	return toneOf(a.themes.app)
}

func decodePages(files [][]byte, tone pageTone) ([]*ui.Bitmap, error) {
	pages := make([]*ui.Bitmap, 0, len(files))
	for _, data := range files {
		page, err := decodePage(data, tone)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	if len(pages) == 0 {
		return nil, errors.New("Typst 没有生成页面")
	}
	return pages, nil
}

// decodePage 解码一页 PNG。深色主题下反转亮度并映射到主题色；作者自己排成深色的页面不动。
func decodePage(data []byte, tone pageTone) (*ui.Bitmap, error) {
	if !tone.dark {
		return ui.DecodeBitmap(data)
	}
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	img, ok := src.(*image.NRGBA)
	if !ok {
		img = image.NewNRGBA(src.Bounds())
		draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	}
	if !darkPage(img) {
		recolor(img, tone)
	}
	return ui.NewBitmap(img), nil
}

// darkPage 抽样估计页面平均亮度，低于一半视为作者自己排的深色版式。
// 透明部分按白纸计算：fill: none 的页面在 PDF 和浅色主题下都是白底，不能当成深色版式。
func darkPage(img *image.NRGBA) bool {
	const stride = 7 * 4
	var sum, n int
	for i := 0; i+3 < len(img.Pix); i += stride {
		p := img.Pix[i : i+4 : i+4]
		sum += 299*overWhite(p[0], p[3]) + 587*overWhite(p[1], p[3]) + 114*overWhite(p[2], p[3])
		n++
	}
	return n > 0 && sum/n < 128*1000
}

// overWhite 返回非预乘的通道值 v 以透明度 a 叠在白底上的结果。
func overWhite(v, a uint8) int {
	return (int(v)*int(a) + 255*(255-int(a))) / 255
}

// recolor 保留色相和饱和度，只反转 HSL 亮度：每个通道加上 255-最大值-最小值即可，
// 再把 0 到 255 映射到主题的页面底色与正文色之间。透明部分先叠在白纸上再调色，
// 否则黑字会直接落在深色画布上。
func recolor(img *image.NRGBA, tone pageTone) {
	var lut [3][256]uint8
	for c := range 3 {
		bg, fg := int(tone.bg[c]), int(tone.fg[c])
		for v := range 256 {
			lut[c][v] = uint8(bg + (fg-bg)*v/255)
		}
	}
	pix := img.Pix
	for i := 0; i+3 < len(pix); i += 4 {
		p := pix[i : i+4 : i+4]
		r, g, b := overWhite(p[0], p[3]), overWhite(p[1], p[3]), overWhite(p[2], p[3])
		shift := 255 - max(r, g, b) - min(r, g, b)
		p[0], p[1], p[2], p[3] = lut[0][r+shift], lut[1][g+shift], lut[2][b+shift], 255
	}
}

// setPreviewPages 换上编译结果中的页面。进行中的调色基于旧页面，一并作废。
func (s *session) setPreviewPages(res previewResult) {
	s.previewPages, s.previewData, s.previewTone = res.pages, res.data, res.tone
	s.recolorGen++
	s.recoloring = false
}

// recolorPreview 在主题与页面配色不一致时，用保留的原始字节在后台重新解码，不重新编译。
func (a *App) recolorPreview(s *session) {
	tone := a.pageTone()
	if len(s.previewData) == 0 || s.previewTone == tone || (s.recoloring && s.recolorTone == tone) {
		return
	}
	s.recolorGen++
	gen, data := s.recolorGen, s.previewData
	s.recoloring, s.recolorTone = true, tone
	go func() {
		pages, err := decodePages(data, tone)
		a.publish(func() { a.finishRecolor(s, gen, tone, pages, err) })
	}()
}

// finishRecolor 只接受当前笔记最新一次调色的结果，且主题在此期间没有再变。
func (a *App) finishRecolor(s *session, gen uint64, tone pageTone, pages []*ui.Bitmap, err error) {
	if s.recolorGen != gen {
		return
	}
	s.recoloring = false
	if a.current != s || a.pageTone() != tone {
		return
	}
	if err != nil {
		// 同一份字节之前解码成功过，失败只可能是内存不足一类的偶发问题；保留旧位图，
		// 并把配色记为已处理，否则每帧都会重新发起调色。
		s.previewTone = tone
		return
	}
	s.previewPages, s.previewTone = pages, tone
}
