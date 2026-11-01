import SwiftUI

/// AboutView shows which build this is, so an error on a device can be matched
/// to the commit it was built from.
struct AboutView: View {
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        List {
            LabeledContent("Version") {
                Text(BuildInfo.current)
                    .font(.body.monospaced())
                    .textSelection(.enabled)
                    .accessibilityIdentifier("about.version")
            }
        }
        .navigationTitle("About")
        .toolbar { Button("Done") { dismiss() } }
    }
}
