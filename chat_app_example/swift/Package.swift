// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "LatchClient",
    platforms: [.macOS(.v13)],
    products: [
        .library(name: "LatchClient", targets: ["LatchClient"])
    ],
    targets: [
        .target(name: "LatchClient"),
        .testTarget(name: "LatchClientTests", dependencies: ["LatchClient"])
    ]
)
