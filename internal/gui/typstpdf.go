package gui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/gvenusleo/mygo-pdf/pdf"

	"gotyp/internal/tinymist"
)

// typstPDF 保留最近显示的编译产物，查看器被回收后只留下临时文件。
type typstPDF struct {
	path    string
	size    int64
	readers sync.WaitGroup
}

// typstPDFReader 在 PDFium 的工作线程首次读取时打开文件。
// PDFium 串行读取，释放源后才从后台关闭句柄。
type typstPDFReader struct {
	path string
	file *os.File
}

func (r *typstPDFReader) ReadAt(data []byte, offset int64) (int, error) {
	if r.file == nil {
		file, err := os.Open(r.path)
		if err != nil {
			return 0, err
		}
		r.file = file
	}
	return r.file.ReadAt(data, offset)
}

func (r *typstPDFReader) Close() error {
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}

func (a *App) loadPreviewFile(p *pdfPreview, ctx context.Context, file *tinymist.PreviewFile, finished func(pdf.State)) {
	artifact := &typstPDF{path: file.File.Name(), size: file.Size}
	if !a.loadTypstPDF(p, ctx, artifact, file.File, file.File.Close, finished) {
		a.discardTypstPDF(artifact)
	}
}

func (a *App) loadTypstPDF(p *pdfPreview, ctx context.Context, artifact *typstPDF, source io.ReaderAt, closeSource func() error, finished func(pdf.State)) bool {
	request, released := p.LoadReaderAt(ctx, source, artifact.size, pdf.LoadOptions{Mode: pdf.RefreshDocument})
	artifact.readers.Go(func() {
		<-released
		_ = closeSource()
	})
	if request == 0 {
		return false
	}
	a.discardTypstPDF(p.pendingFile)
	p.pendingFile = nil
	if artifact != p.file {
		p.pendingFile = artifact
	}
	p.request = request
	p.loaded = func(state pdf.State) {
		if p.pendingFile == artifact {
			p.pendingFile = nil
			if state.DisplayedGeneration == request {
				a.discardTypstPDF(p.file)
				p.file = artifact
			} else {
				a.discardTypstPDF(artifact)
			}
		}
		finished(state)
	}
	return true
}

func (a *App) restoreTypstPDF(tab *fileTab) {
	p, s := tab.pdf, tab.session
	reader := &typstPDFReader{path: p.file.path}
	p.restoring = a.loadTypstPDF(p, a.ctx, p.file, reader, reader.Close, func(state pdf.State) {
		p.restoring, s.previewStarting = false, false
		s.previewReady = state.Err == nil
		if !s.previewReady {
			a.discardTypstPDF(p.file)
			p.file = nil
			if s.previewResult != nil && s.previewResult.err == "" {
				s.previewResult = nil
			}
		}
		a.schedulePreview(s)
	})
	s.previewStarting = p.restoring
}

func (a *App) discardTypstPDF(artifact *typstPDF) {
	if artifact != nil {
		a.previewFiles.Go(func() {
			artifact.readers.Wait()
			_ = os.RemoveAll(filepath.Dir(artifact.path))
		})
	}
}
