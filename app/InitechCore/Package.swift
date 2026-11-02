// swift-tools-version: 6.0
import PackageDescription

// InitechCore is the app's model logic: it builds and tests on a Mac with
// `swift test`, in seconds, and imports no UIKit or SwiftUI. Its tests never
// open a socket (a census test holds that): a listening test binary is banned
// on the shared Mac, so real HTTP, TLS and the pin stay in the app's tests.
let package = Package(
    name: "InitechCore",
    platforms: [.iOS("26.0"), .macOS("26.0")],
    products: [.library(name: "InitechCore", targets: ["InitechCore"])],
    targets: [
        .target(name: "InitechCore"),
        .testTarget(name: "InitechCoreTests", dependencies: ["InitechCore"]),
    ]
)
