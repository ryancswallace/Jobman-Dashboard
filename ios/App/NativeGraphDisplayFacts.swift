#if DEBUG
import SwiftUI

/// Reports the actual SwiftUI environment; it never changes accessibility settings.
struct NativeGraphDisplayFacts: View {
    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reducedMotion
    var body: some View {
        Text("Synthetic display: Dynamic Type \(String(describing: size)); Reduce Motion \(reducedMotion ? "enabled" : "disabled")")
            .font(.caption).accessibilityIdentifier("graphDisplayEnvironment")
    }
}
#endif
