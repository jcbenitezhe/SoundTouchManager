import Foundation
import Security

/// The desktop app's Go backend, linked in as a static library
/// (`make ios-backend`). It serves the web UI and its API on loopback.
enum Backend {
    /// Fixed so the page origin, and with it WKWebView's localStorage
    /// (language, text size), stays the same across launches. The backend
    /// falls back to a free port when another app holds this one.
    static let preferredAddress = "127.0.0.1:47811"

    struct StartError: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    /// Shared secret for the API, new on every launch.
    static let token: String = {
        var bytes = [UInt8](repeating: 0, count: 16)
        if SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) != errSecSuccess {
            bytes = (0..<16).map { _ in UInt8.random(in: .min ... .max) }
        }
        return bytes.map { String(format: "%02x", $0) }.joined()
    }()

    /// Starts the backend (only the first call does) and returns the page URL.
    /// Blocks while the backend starts; call it off the main thread.
    static func start() throws -> URL {
        let documents = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0].path
        let args: [UnsafeMutablePointer<CChar>?] = [preferredAddress, token, documents].map { (s: String) in strdup(s) }
        defer { args.forEach { free($0) } }

        guard let raw = STMBridgeStart(args[0], args[1], args[2]) else {
            throw StartError(message: "no reply")
        }
        defer { free(raw) }
        let reply = String(cString: raw)
        if reply.hasPrefix("error:") {
            throw StartError(message: String(reply.dropFirst("error:".count)).trimmingCharacters(in: .whitespaces))
        }
        guard let url = URL(string: "http://\(reply)/?stmToken=\(token)") else {
            throw StartError(message: "bad address \(reply)")
        }
        return url
    }
}
