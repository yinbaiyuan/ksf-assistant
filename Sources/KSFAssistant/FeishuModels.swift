import CryptoKit
import Foundation

struct ToolchainStatus: Decodable {
    struct Skill: Decodable, Identifiable {
        let name: String
        let state: String
        var id: String { name }
    }

    let schemaVersion: Int
    let version: String
    let installed: Bool
    let healthy: Bool
    let skills: [Skill]
    let problemCount: Int
    let installationState: String?
    let installationTitle: String?
    let installationAction: String?
    let installationDetails: [String]?

    var isHealthy: Bool { schemaVersion == 1 && healthy }

    var summary: String {
        if schemaVersion != 1 { return "状态格式不兼容，请更新应用。" }
        if isHealthy { return "官方 CLI \(version) · \(skills.count) 项 Skills 已验证" }
        if installed {
            return "Skills 已安装 \(skills.filter { $0.state == "managed" }.count)/\(skills.count) 项；工具链需要处理。"
        }
        return "尚未安装官方 CLI 与 Skills。"
    }
}

enum FeishuServiceAvailability: Equatable {
    case notConfigured
    case unavailable(String)
    case stopped
    case dryRun
    case ready
}

struct FeishuServiceSnapshot: Equatable {
    let availability: FeishuServiceAvailability
    let targetAliases: [String]
    let taskLinkProtocolVersion: Int
    let taskLinkReady: Bool
    let connectedTaskCount: Int?
    let readinessBlockers: [String]
    let profile: String
    let profileValid: Bool
    let inboundConnection: Bool
    let processRunning: Bool
    let processState: String
    let configured: Bool
    let processPID: Int?
    let restartCount: Int
    let lastError: String?

    init(
        availability: FeishuServiceAvailability,
        targetAliases: [String],
        taskLinkProtocolVersion: Int,
        taskLinkReady: Bool,
        readinessBlockers: [String],
        profile: String = "",
        profileValid: Bool = false,
        inboundConnection: Bool = false,
        processRunning: Bool = false,
        processState: String = "stopped",
        configured: Bool = false,
        processPID: Int? = nil,
        restartCount: Int = 0,
        lastError: String? = nil,
        connectedTaskCount: Int? = nil
    ) {
        self.availability = availability
        self.targetAliases = targetAliases
        self.taskLinkProtocolVersion = taskLinkProtocolVersion
        self.taskLinkReady = taskLinkReady
        self.connectedTaskCount = connectedTaskCount
        self.readinessBlockers = readinessBlockers
        self.profile = profile
        self.profileValid = profileValid
        self.inboundConnection = inboundConnection
        self.processRunning = processRunning
        self.processState = processState
        self.configured = configured
        self.processPID = processPID
        self.restartCount = restartCount
        self.lastError = lastError
    }

    static let notConfigured = FeishuServiceSnapshot(
        availability: .notConfigured,
        targetAliases: [],
        taskLinkProtocolVersion: 0,
        taskLinkReady: false,
        readinessBlockers: []
    )
}

struct FeishuTaskLinkSnapshot: Codable, Equatable {
    struct Controls: Codable, Equatable {
        let canSend: Bool
        let canSteer: Bool
        let canInterrupt: Bool
        let canAnswer: Bool
        let canRelease: Bool
        let acceptsAttachments: Bool
    }

    let taskKey: String
    let title: String
    let projectName: String
    let targetAlias: String
    let linkState: String
    let turnState: String
    let turnOwner: String
    let actionRequired: String
    let controls: Controls
    let createdAt: String
    let updatedAt: String
    let expiresAt: String
    let remainingSeconds: Int
    let hasPendingMessage: Bool
    let detailAvailable: Bool
    let phase: String
    let detailSummary: String

    var presentationState: String {
        if linkState != "active" { return linkState }
        return turnState == "idle" ? "connected" : turnState
    }

    static func taskKey(for threadID: String) -> String {
        SHA256.hash(data: Data(threadID.utf8)).prefix(10).map { String(format: "%02x", $0) }.joined()
    }
}
