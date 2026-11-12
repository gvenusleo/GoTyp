# PDFKit 原生预览模块

本模块通过 MyGo 原生模块嵌入 macOS PDFKit。接口和构建方式见 MyGo 的 `docs/native-modules.md`。

- `pdfpreview.go` 是 Go 侧接口；`//mygo:native` 为接口生成构造函数。
- `pdfpreview_native.go` 和 `swift/Sources/Pdfpreview/Pdfpreview.mygo.swift` 由 MyGo 生成，不手工修改。修改接口后运行 `go tool mygo native check internal/pdfpreview`，并保留生成文件。
- Swift 实现放在 `swift/Sources/Pdfpreview/Pdfpreview.swift`，系统框架以外的依赖在 `swift/Package.swift` 声明。
- PDFDocument 在后台解析，PDFView 及其状态只在主线程访问。安装前核对取消状态和加载代次，刷新时保持阅读位置及缩放。修改加载代次之前先检查取消，避免已经取消的旧请求作废有效加载。
- 替换 PDFDocument 并恢复阅读位置后，完成 PDFView 内部布局并提交显示，避免新页面等待延迟绘制时短暂空白；不使用固定延时掩盖刷新。
- PDFView 的 delegate 使用独立对象。PDFView 自身实现了部分 delegate 转发方法，把自身设成 delegate 会递归调用。
- 模块不读取仓库、保存源码或启动编译进程，这些职责由 Go 侧处理。

在项目根目录通过 mise 运行检查：

```sh
mise exec -- go tool mygo native check internal/pdfpreview
swift test --package-path internal/pdfpreview/swift
```
