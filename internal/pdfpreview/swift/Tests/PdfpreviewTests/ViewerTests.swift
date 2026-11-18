import AppKit
import PDFKit
import XCTest
@testable import Pdfpreview

final class ViewerTests: XCTestCase {
    // load 返回前完成新页面的布局，避免旧页面移除后还等待下一次延迟绘制。
    @MainActor func testReloadCompletesPageLayout() async throws {
        _ = NSApplication.shared
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 700, height: 650),
                              styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        defer { window.close() }
        let viewer = PreviewView()
        window.contentView = viewer
        window.makeKeyAndOrderFront(nil)
        let document = PDFDocument()
        for _ in 0..<3 {
            let page = PDFPage()
            page.setBounds(CGRect(x: 0, y: 0, width: 300, height: 500), for: .mediaBox)
            document.insert(page, at: document.pageCount)
        }
        let data = try XCTUnwrap(document.dataRepresentation())
        try await viewer.load(data: data)
        viewer.autoScales = false
        viewer.scaleFactor = 1.25
        viewer.go(to: try XCTUnwrap(viewer.document?.page(at: 1)))
        let oldPage = viewer.currentPage.flatMap { viewer.document?.index(for: $0) }
        try await viewer.load(data: data)
        let content = try XCTUnwrap(viewer.documentView)
        XCTAssertFalse(content.needsLayout, "新文档布局尚未完成，页面会暂时空白")
        XCTAssertEqual(viewer.scaleFactor, 1.25, accuracy: 0.001)
        XCTAssertFalse(viewer.autoScales)
        XCTAssertEqual(viewer.currentPage.flatMap { viewer.document?.index(for: $0) }, oldPage)
    }

    // 已取消的旧任务开始执行时，不得改变有效加载使用的代次。
    @MainActor func testCanceledLoadDoesNotInvalidateCurrentLoad() async throws {
        _ = NSApplication.shared
        let document = PDFDocument()
        let page = PDFPage()
        page.setBounds(CGRect(x: 0, y: 0, width: 300, height: 500), for: .mediaBox)
        document.insert(page, at: 0)
        let data = try XCTUnwrap(document.dataRepresentation())
        let viewer = PreviewView()
        let valid = Task { @MainActor in try await viewer.load(data: data) }
        let canceled = Task { @MainActor in try await viewer.load(data: data) }
        canceled.cancel()
        do {
            try await valid.value
        } catch {
            XCTFail("有效加载被已取消的任务作废：\(error)")
        }
        do {
            try await canceled.value
            XCTFail("已取消的加载不应成功")
        } catch is CancellationError {
        } catch {
            XCTFail("取消结果错误：\(error)")
        }
        XCTAssertEqual(viewer.document?.pageCount, 1)
    }
}
