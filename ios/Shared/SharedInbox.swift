import Foundation

/// The hand-off slot between the share extension and the app: one pending
/// shared text in the App Group's defaults, taken (and cleared) by the app.
enum SharedInbox {
    static let appGroup = "group.io.github.jcbenitezhe.soundtouchmanager"
    static let urlScheme = "stmanager"
    static let maxChars = 2048
    private static let key = "pendingShare"

    static func put(_ text: String) {
        let trimmed = String(text.trimmingCharacters(in: .whitespacesAndNewlines).prefix(maxChars))
        guard !trimmed.isEmpty else { return }
        UserDefaults(suiteName: appGroup)?.set(trimmed, forKey: key)
    }

    static func take() -> String? {
        guard let defaults = UserDefaults(suiteName: appGroup),
              let text = defaults.string(forKey: key) else { return nil }
        defaults.removeObject(forKey: key)
        return text.isEmpty ? nil : text
    }
}
