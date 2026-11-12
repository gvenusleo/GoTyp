// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Pdfpreview",
    platforms: [.macOS(.v13)],
    products: [.library(name: "Pdfpreview", type: .dynamic, targets: ["Pdfpreview"])],
    dependencies: [],
    targets: [
        .target(name: "Pdfpreview"),
        .testTarget(name: "PdfpreviewTests", dependencies: ["Pdfpreview"]),
    ],
    swiftLanguageModes: [.v5]
)
