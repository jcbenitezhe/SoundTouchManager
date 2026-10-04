import UIKit
import WebKit

/// Shows the app's web UI, served by the in-process Go backend.
final class WebViewController: UIViewController, WKNavigationDelegate, WKUIDelegate {
    private var webView: WKWebView!
    private let statusLabel = UILabel()
    private let spinner = UIActivityIndicatorView(style: .large)
    private var pageURL: URL?
    private var uiReady = false
    private var pendingShare: String?

    // Defines window.STMNative before the page runs: it marks the phone build
    // for the frontend (big buttons, no USB) and opens links outside the app.
    private static let nativeHostScript = """
    window.STMNative = {
      openURL: function (u) { window.webkit.messageHandlers.stmOpenURL.postMessage(String(u)); }
    };
    """

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = UIColor(named: "LaunchBackground")

        let config = WKWebViewConfiguration()
        config.websiteDataStore = .default()
        // Local TuneIn playback starts after the station is resolved, past the tap.
        config.mediaTypesRequiringUserActionForPlayback = []
        config.userContentController.addUserScript(
            WKUserScript(source: Self.nativeHostScript, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        config.userContentController.add(OpenURLHandler(), name: "stmOpenURL")

        webView = WKWebView(frame: .zero, configuration: config)
        webView.navigationDelegate = self
        webView.uiDelegate = self
        webView.isOpaque = false
        webView.backgroundColor = .clear
        webView.scrollView.backgroundColor = .clear
        webView.allowsBackForwardNavigationGestures = true
        webView.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(webView)

        spinner.translatesAutoresizingMaskIntoConstraints = false
        spinner.startAnimating()
        view.addSubview(spinner)

        statusLabel.translatesAutoresizingMaskIntoConstraints = false
        statusLabel.numberOfLines = 0
        statusLabel.textAlignment = .center
        statusLabel.font = .preferredFont(forTextStyle: .title3)
        statusLabel.adjustsFontForContentSizeCategory = true
        statusLabel.text = NSLocalizedString("starting", comment: "Shown while the backend starts")
        view.addSubview(statusLabel)

        let safe = view.safeAreaLayoutGuide
        NSLayoutConstraint.activate([
            webView.topAnchor.constraint(equalTo: safe.topAnchor),
            webView.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            webView.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            webView.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            spinner.centerXAnchor.constraint(equalTo: view.centerXAnchor),
            spinner.centerYAnchor.constraint(equalTo: view.centerYAnchor, constant: -30),
            statusLabel.topAnchor.constraint(equalTo: spinner.bottomAnchor, constant: 20),
            statusLabel.leadingAnchor.constraint(equalTo: safe.leadingAnchor, constant: 24),
            statusLabel.trailingAnchor.constraint(equalTo: safe.trailingAnchor, constant: -24),
        ])

        NotificationCenter.default.addObserver(
            self, selector: #selector(willEnterForeground),
            name: UIApplication.willEnterForegroundNotification, object: nil)
        NotificationCenter.default.addObserver(
            self, selector: #selector(checkSharedInbox),
            name: UIApplication.didBecomeActiveNotification, object: nil)

        startBackend()
    }

    private func startBackend() {
        DispatchQueue.global(qos: .userInitiated).async {
            let result = Result { try Backend.start() }
            DispatchQueue.main.async { self.backendStarted(result) }
        }
    }

    private func backendStarted(_ result: Result<URL, Error>) {
        switch result {
        case .success(let url):
            pageURL = url
            webView.load(URLRequest(url: url))
        case .failure(let error):
            spinner.stopAnimating()
            statusLabel.text = String(
                format: NSLocalizedString("backend_failed", comment: "Backend start error"),
                error.localizedDescription)
        }
    }

    private func setLoading(_ loading: Bool) {
        spinner.isHidden = !loading
        statusLabel.isHidden = !loading
    }

    @objc private func willEnterForeground() {
        // The web content process may have been dropped while suspended.
        if let url = pageURL, webView.url == nil {
            webView.load(URLRequest(url: url))
        }
    }

    // MARK: - Shared links

    @objc func checkSharedInbox() {
        if let text = SharedInbox.take() { pendingShare = text }
        deliverShare()
    }

    // window.stmHandleShare is defined once main.js has run, which can trail
    // didFinish by a moment; the snippet waits up to ten seconds for it.
    private func deliverShare() {
        guard uiReady, let text = pendingShare else { return }
        pendingShare = nil
        webView.callAsyncJavaScript(
            """
            for (let n = 0; n < 40 && typeof window.stmHandleShare !== 'function'; n++) {
              await new Promise(r => setTimeout(r, 250));
            }
            if (typeof window.stmHandleShare === 'function') window.stmHandleShare(text);
            """,
            arguments: ["text": text], in: nil, in: .page, completionHandler: nil)
    }

    // MARK: - WKNavigationDelegate

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        setLoading(false)
        uiReady = webView.url.map(isOwnPage) ?? false
        deliverShare()
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        uiReady = false
        if let url = pageURL {
            webView.load(URLRequest(url: url))
        }
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
    ) {
        guard let url = navigationAction.request.url else {
            decisionHandler(.cancel)
            return
        }
        if isOwnPage(url) || ["about", "blob", "data"].contains(url.scheme?.lowercased() ?? "") {
            decisionHandler(.allow)
            return
        }
        openExternally(url)
        decisionHandler(.cancel)
    }

    // MARK: - WKUIDelegate

    // window.open and target="_blank" links leave the app.
    func webView(
        _ webView: WKWebView,
        createWebViewWith configuration: WKWebViewConfiguration,
        for navigationAction: WKNavigationAction,
        windowFeatures: WKWindowFeatures
    ) -> WKWebView? {
        if let url = navigationAction.request.url {
            openExternally(url)
        }
        return nil
    }

    func webView(
        _ webView: WKWebView,
        runJavaScriptAlertPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping () -> Void
    ) {
        let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: "OK", style: .default) { _ in completionHandler() })
        present(alert, animated: true)
    }

    func webView(
        _ webView: WKWebView,
        runJavaScriptConfirmPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping (Bool) -> Void
    ) {
        let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(
            title: NSLocalizedString("cancel", comment: ""), style: .cancel) { _ in completionHandler(false) })
        alert.addAction(UIAlertAction(title: "OK", style: .default) { _ in completionHandler(true) })
        present(alert, animated: true)
    }

    // MARK: - Helpers

    private func isOwnPage(_ url: URL) -> Bool {
        guard let page = pageURL else { return false }
        return url.scheme == page.scheme && url.host == page.host && url.port == page.port
    }

    private func openExternally(_ url: URL) {
        guard ["http", "https", "mailto"].contains(url.scheme?.lowercased() ?? "") else { return }
        UIApplication.shared.open(url)
    }
}

/// Receives STMNative.openURL. Kept separate from the controller because
/// WKUserContentController holds its handlers strongly.
private final class OpenURLHandler: NSObject, WKScriptMessageHandler {
    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        guard let text = message.body as? String, let url = URL(string: text),
              ["http", "https", "mailto"].contains(url.scheme?.lowercased() ?? "") else { return }
        UIApplication.shared.open(url)
    }
}
