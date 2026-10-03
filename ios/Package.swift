// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "DashboardCore",
    platforms: [.iOS(.v18), .macOS(.v14)],
    products: [.library(name: "DashboardCore", targets: ["DashboardCore"])],
    targets: [
        .target(name: "DashboardCore"),
        .testTarget(name: "DashboardCoreTests", dependencies: ["DashboardCore"], resources: [.copy("Fixtures")])
    ]
)
