// swift-tools-version: 5.8

import PackageDescription

let package = Package(
    name: "KSFAssistant",
    platforms: [
        .macOS(.v13),
    ],
    products: [
        .library(name: "KSFAssistantCore", targets: ["KSFAssistantCore"]),
        .executable(name: "KSFAssistant", targets: ["KSFAssistant"]),
    ],
    targets: [
        .target(name: "KSFAssistantCore"),
        .executableTarget(
            name: "KSFAssistant",
            dependencies: ["KSFAssistantCore"]
        ),
        .testTarget(
            name: "KSFAssistantCoreTests",
            dependencies: ["KSFAssistantCore"]
        ),
    ]
)
