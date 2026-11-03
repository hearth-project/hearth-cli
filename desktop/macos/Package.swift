// swift-tools-version:5.9
// The Hearth menu bar app for macOS: the user's machines at a glance, and the
// web console in a window. It reads everything through the `hearth` CLI
// logged in on this Mac (hearth machine list --json, hearth panel --json).
import PackageDescription

let package = Package(
    name: "Hearth",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(
            name: "Hearth",
            path: "Sources/Hearth"
        )
    ]
)
