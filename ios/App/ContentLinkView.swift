import DashboardCore
import SwiftUI

struct ContentLinkView: View {
    @Environment(DashboardStore.self) private var store
    @State private var text = ""
    @State private var error: String?
    var body: some View {
        Form {
            Section("Dashboard link") {
                Text("Paste a job or inbox link from this Dashboard. You must be signed in and still have permission to view it.").font(.footnote)
                Text(store.connectedOrigin ?? "No Dashboard connected").font(.caption).textSelection(.enabled)
                TextField("https://dashboard…/deployments/…", text: $text, axis: .vertical)
                    .textInputAutocapitalization(.never).autocorrectionDisabled().keyboardType(.URL)
                    .accessibilityIdentifier("canonicalDashboardLink")
                Button("Open Dashboard link") {
                    if store.openCanonicalLink(text) { text = ""; error = nil }
                    else { error = "Use the full job or inbox HTTPS link from this Dashboard. Remove anything starting with ? or #, including sign-in tokens." }
                }.disabled(text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                Button("Clear link") { text = ""; error = nil }.disabled(text.isEmpty)
                if let error { ErrorMessage(error: error) }
            }
        }.navigationTitle("Open a link")
            .onDisappear { text = ""; error = nil }
            .onChange(of: store.active) { _, value in if !value { text = ""; error = nil } }
    }
}
