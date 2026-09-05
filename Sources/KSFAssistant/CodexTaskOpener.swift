import AppKit
import KSFAssistantCore
import Foundation

protocol CodexTaskOpening {
    func openTask(id: String) throws
}

enum CodexTaskOpeningError: Error, Equatable, LocalizedError {
    case applicationRejectedURL

    var errorDescription: String? {
        "Codex 未能打开这个任务。"
    }
}

struct WorkspaceCodexTaskOpener: CodexTaskOpening {
    private let openURL: (URL) -> Bool

    init(openURL: @escaping (URL) -> Bool = { NSWorkspace.shared.open($0) }) {
        self.openURL = openURL
    }

    func openTask(id: String) throws {
        let url = try CodexTaskDeepLink.url(for: id)
        guard openURL(url) else { throw CodexTaskOpeningError.applicationRejectedURL }
    }
}

struct ProjectTaskOpenFailure: Equatable {
    let projectID: String
    let message: String
}
