// swift-tools-version: 5.8

import PackageDescription

let package = Package(
    name: "CodexUsageBar",
    platforms: [
        .macOS(.v13),
    ],
    products: [
        .library(name: "CodexUsageCore", targets: ["CodexUsageCore"]),
        .executable(name: "CodexUsageBar", targets: ["CodexUsageBar"]),
    ],
    targets: [
        .target(name: "CodexUsageCore"),
        .executableTarget(
            name: "CodexUsageBar",
            dependencies: ["CodexUsageCore"]
        ),
        .testTarget(
            name: "CodexUsageCoreTests",
            dependencies: ["CodexUsageCore"]
        ),
    ]
)

