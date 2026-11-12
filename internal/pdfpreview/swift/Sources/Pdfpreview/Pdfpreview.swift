import AppKit
import PDFKit

@MainActor func makeViewer() -> any Viewer { PreviewView() }

// PDFDocument 在后台完成解析，交给主线程后只由 PDFView 使用。
private final class DecodedDocument: @unchecked Sendable {
    let document: PDFDocument
    init(_ document: PDFDocument) { self.document = document }
}

final class PreviewView: PDFView, Viewer {
    private var generation: UInt64 = 0
    // PDFView 自身实现了部分转发方法，不能把自己设成 delegate。
    private let linkDelegate = PreviewLinkDelegate()

    init() {
        super.init(frame: .zero)
        displayMode = .singlePageContinuous
        displayDirection = .vertical
        autoScales = true
        delegate = linkDelegate
    }

    required init?(coder: NSCoder) { fatalError("不支持从归档创建 PDF 预览") }

    func load(data: Data) async throws {
        // 被取消但尚未开始执行的任务不能作废当前的有效加载。
        try Task.checkCancellation()
        generation &+= 1
        let current = generation
        let decoded = await Task.detached(priority: .userInitiated) {
            PDFDocument(data: data).map(DecodedDocument.init)
        }.value
        try Task.checkCancellation()
        guard current == generation else { throw CancellationError() }
        guard let decoded else {
            throw NSError(domain: "GoTyp.PDF", code: 1,
                          userInfo: [NSLocalizedDescriptionKey: "无法读取预览 PDF"])
        }
        let oldPage = currentPage.flatMap { document?.index(for: $0) }
        let oldPoint = currentDestination?.point
        let scaledAutomatically = autoScales
        let oldScale = scaleFactor
        document = decoded.document
        autoScales = scaledAutomatically
        if !scaledAutomatically { scaleFactor = oldScale }
        if let oldPage, decoded.document.pageCount > 0,
           let page = decoded.document.page(at: min(oldPage, decoded.document.pageCount - 1)) {
            if let oldPoint { go(to: PDFDestination(page: page, at: oldPoint)) }
            else { go(to: page) }
        }
        // 替换文档会重建页面视图；立即完成布局并提交显示，避免 PDFKit 延迟约 250 ms 才绘制。
        layoutDocumentView()
        layoutSubtreeIfNeeded()
        documentView?.displayIfNeeded()
    }

    func close() throws { clear() }

    func clear() {
        generation &+= 1
        document = nil
        autoScales = true
    }

    func setBackground(red: Double, green: Double, blue: Double) {
        backgroundColor = NSColor(srgbRed: red, green: green, blue: blue, alpha: 1)
    }

}

@MainActor private final class PreviewLinkDelegate: NSObject, PDFViewDelegate {
    func pdfViewWillClick(onLink sender: PDFView, with url: URL) {
        guard let scheme = url.scheme?.lowercased(),
              ["http", "https", "mailto"].contains(scheme),
              url.user == nil, url.password == nil else { return }
        NSWorkspace.shared.open(url)
    }
}
