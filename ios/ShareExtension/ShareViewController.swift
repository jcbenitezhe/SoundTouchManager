import UIKit
import UniformTypeIdentifiers

/// Receives a link shared from another app (the TuneIn app in practice) and
/// leaves it in the shared App Group. Share extensions cannot launch their
/// host app, so the app picks the link up the next time it becomes active and
/// opens it in the TuneIn tab.
final class ShareViewController: UIViewController {
    private let label = UILabel()

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = UIColor(white: 0, alpha: 0.4)
        label.translatesAutoresizingMaskIntoConstraints = false
        label.textColor = .white
        label.textAlignment = .center
        label.numberOfLines = 0
        label.font = .preferredFont(forTextStyle: .headline)
        view.addSubview(label)
        NSLayoutConstraint.activate([
            label.centerYAnchor.constraint(equalTo: view.centerYAnchor),
            label.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 24),
            label.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -24),
        ])
    }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        extractText { [weak self] text in
            DispatchQueue.main.async { self?.finish(with: text) }
        }
    }

    private func finish(with text: String?) {
        if let text, !text.isEmpty {
            SharedInbox.put(text)
            label.text = NSLocalizedString("share_saved", comment: "Link handed to the app")
        } else {
            label.text = NSLocalizedString("share_nothing", comment: "Shared item had no link")
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { [weak self] in
            self?.extensionContext?.completeRequest(returningItems: nil)
        }
    }

    private func extractText(_ done: @escaping (String?) -> Void) {
        let providers = (extensionContext?.inputItems as? [NSExtensionItem] ?? [])
            .flatMap { $0.attachments ?? [] }
        let urlType = UTType.url.identifier
        let textType = UTType.plainText.identifier
        if let p = providers.first(where: { $0.hasItemConformingToTypeIdentifier(urlType) }) {
            p.loadItem(forTypeIdentifier: urlType) { item, _ in
                done((item as? URL)?.absoluteString ?? (item as? String))
            }
        } else if let p = providers.first(where: { $0.hasItemConformingToTypeIdentifier(textType) }) {
            p.loadItem(forTypeIdentifier: textType) { item, _ in
                done(item as? String)
            }
        } else {
            done(nil)
        }
    }
}
