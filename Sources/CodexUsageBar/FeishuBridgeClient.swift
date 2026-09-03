import Darwin
import CryptoKit
import Foundation

enum FeishuBridgeAvailability: Equatable {
    case notConfigured
    case unavailable(String)
    case stopped
    case dryRun
    case ready
}

struct FeishuBridgeSnapshot: Equatable {
    let availability: FeishuBridgeAvailability
    let targetAliases: [String]
    let taskLinkProtocolVersion: Int
    let taskLinkReady: Bool
    let readinessBlockers: [String]
    let profile: String
    let profileValid: Bool
    let inboundConnection: Bool
    let processRunning: Bool

    init(
        availability: FeishuBridgeAvailability,
        targetAliases: [String],
        taskLinkProtocolVersion: Int,
        taskLinkReady: Bool,
        readinessBlockers: [String],
        profile: String = "",
        profileValid: Bool = false,
        inboundConnection: Bool = false,
        processRunning: Bool = false
    ) {
        self.availability = availability
        self.targetAliases = targetAliases
        self.taskLinkProtocolVersion = taskLinkProtocolVersion
        self.taskLinkReady = taskLinkReady
        self.readinessBlockers = readinessBlockers
        self.profile = profile
        self.profileValid = profileValid
        self.inboundConnection = inboundConnection
        self.processRunning = processRunning
    }

    static let notConfigured = FeishuBridgeSnapshot(
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
    let state: String
    let createdAt: String
    let updatedAt: String
    let expiresAt: String
    let remainingSeconds: Int
    let hasPendingMessage: Bool
    let detailAvailable: Bool
    let phase: String
    let detailSummary: String
}

enum FeishuBridgeClientError: Error, LocalizedError, Equatable {
    case invalidRoot(String)
    case unsafeClient
    case nodeMissing
    case commandFailed(String)
    case invalidResponse

    var errorDescription: String? {
        switch self {
        case let .invalidRoot(message): return message
        case .unsafeClient: return "飞书桥客户端文件的所有权或权限不安全。"
        case .nodeMissing: return "未找到 Node.js，无法运行飞书桥客户端。"
        case let .commandFailed(message): return message.isEmpty ? "飞书桥命令执行失败。" : message
        case .invalidResponse: return "飞书桥返回了无法识别的数据。"
        }
    }
}

struct FeishuBridgeClient {
    private let fileManager: FileManager
    private let nodeCandidates: [String]

    init(
        fileManager: FileManager = .default,
        nodeCandidates: [String] = [
            "/opt/homebrew/bin/node",
            "/usr/local/bin/node",
            "/usr/bin/node",
        ]
    ) {
        self.fileManager = fileManager
        self.nodeCandidates = nodeCandidates
    }

    func inspect(rootURL: URL) throws -> FeishuBridgeSnapshot {
        let clientURL = try validatedClient(rootURL: rootURL)
        let statusData = try run(clientURL: clientURL, arguments: ["status"])
        let targetsData = try run(clientURL: clientURL, arguments: ["targets", "list"])
        let protocolData = try run(clientURL: clientURL, arguments: ["task-link", "protocol"])
        let status = try JSONDecoder().decode(StatusResponse.self, from: statusData)
        let targets = try JSONDecoder().decode(TargetsResponse.self, from: targetsData)
        let taskLink = try JSONDecoder().decode(TaskLinkProtocolResponse.self, from: protocolData)

        let availability: FeishuBridgeAvailability
        if !status.outbound.enabled {
            availability = .unavailable("飞书桥尚未启用主动出站。")
        } else if status.outbound.dryRun {
            availability = .dryRun
        } else if !(status.pid.alive || status.launchd.running == true) {
            availability = .stopped
        } else if taskLink.protocol != "codex-feishu-task-link-v1" || taskLink.version < 2 {
            availability = .unavailable("飞书桥任务控制协议需要 v2。")
        } else if !taskLink.readiness.ready {
            availability = .unavailable("飞书桥任务控制尚未就绪：\(taskLink.readiness.blockers.joined(separator: "、"))")
        } else {
            availability = .ready
        }
        return FeishuBridgeSnapshot(
            availability: availability,
            targetAliases: targets.targets.messages.filter { $0.taskLinkEligible }.map(\.alias).sorted(),
            taskLinkProtocolVersion: taskLink.version,
            taskLinkReady: taskLink.readiness.ready,
            readinessBlockers: taskLink.readiness.blockers
        )
    }

    func sendTest(rootURL: URL, targetAlias: String) throws {
        guard !targetAlias.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw FeishuBridgeClientError.commandFailed("请先选择飞书目标。")
        }
        let clientURL = try validatedClient(rootURL: rootURL)
        let body = Data("Codex Usage Bar 飞书桥连接测试成功".utf8)
        _ = try run(
            clientURL: clientURL,
            arguments: [
                "send",
                "--target", targetAlias,
                "--format", "text",
                "--content-file", "-",
                "--source", "codex-usage-bar",
                "--reason", "用户在 Codex Usage Bar 中手动发送连接测试消息",
            ],
            input: body
        )
    }

    func taskLinkProtocol(rootURL: URL) throws -> Bool {
        let data = try run(clientURL: try validatedClient(rootURL: rootURL), arguments: ["task-link", "protocol"])
        guard let response = try? JSONDecoder().decode(TaskLinkProtocolResponse.self, from: data) else { return false }
        return response.protocol == "codex-feishu-task-link-v1" && response.version >= 2 && response.readiness.ready
    }

    func listTaskLinks(rootURL: URL) throws -> [FeishuTaskLinkSnapshot] {
        let data = try run(clientURL: try validatedClient(rootURL: rootURL), arguments: ["task-link", "list"])
        return try JSONDecoder().decode(TaskLinkListResponse.self, from: data).links
    }

    func createTaskLink(
        rootURL: URL, threadID: String, title: String,
        projectName: String, targetAlias: String
    ) throws -> FeishuTaskLinkSnapshot {
        let payload: [String: Any] = [
            "threadId": threadID, "title": title,
            "projectName": projectName, "targetAlias": targetAlias,
        ]
        let input = try JSONSerialization.data(withJSONObject: payload)
        let data = try run(
            clientURL: try validatedClient(rootURL: rootURL),
            arguments: ["task-link", "create", "--payload-file", "-"], input: input
        )
        return try JSONDecoder().decode(TaskLinkMutationResponse.self, from: data).link
    }

    func interruptTaskLink(rootURL: URL, threadID: String) throws -> FeishuTaskLinkSnapshot {
        let data = try run(
            clientURL: try validatedClient(rootURL: rootURL),
            arguments: ["task-link", "interrupt", "--task-key", Self.taskKey(threadID)]
        )
        return try JSONDecoder().decode(TaskLinkMutationResponse.self, from: data).link
    }

    func releaseTaskLink(rootURL: URL, threadID: String) throws -> FeishuTaskLinkSnapshot {
        let key = Self.taskKey(threadID)
        let data = try run(
            clientURL: try validatedClient(rootURL: rootURL),
            arguments: ["task-link", "release", "--task-key", key]
        )
        return try JSONDecoder().decode(TaskLinkMutationResponse.self, from: data).link
    }

    static func taskKey(_ threadID: String) -> String {
        SHA256.hash(data: Data(threadID.utf8)).prefix(10).map { String(format: "%02x", $0) }.joined()
    }

    func validate(rootURL: URL) throws {
        _ = try validatedClient(rootURL: rootURL)
    }

    private func validatedClient(rootURL: URL) throws -> URL {
        let root = rootURL.standardizedFileURL
        let rootValues = try root.resourceValues(forKeys: [.isDirectoryKey, .isSymbolicLinkKey])
        guard rootValues.isDirectory == true, rootValues.isSymbolicLink != true else {
            throw FeishuBridgeClientError.invalidRoot("所选目录不是安全的飞书桥工程目录。")
        }
        let client = root.appendingPathComponent("scripts/bridge-client.js").standardizedFileURL
        let prefix = root.path.hasSuffix("/") ? root.path : root.path + "/"
        guard client.path.hasPrefix(prefix), fileManager.fileExists(atPath: client.path) else {
            throw FeishuBridgeClientError.invalidRoot("所选目录缺少 scripts/bridge-client.js。")
        }
        let values = try client.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey])
        let attributes = try fileManager.attributesOfItem(atPath: client.path)
        let owner = (attributes[.ownerAccountID] as? NSNumber)?.uint32Value
        let permissions = (attributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0
        guard values.isRegularFile == true,
              values.isSymbolicLink != true,
              owner == getuid(),
              permissions & 0o022 == 0 else {
            throw FeishuBridgeClientError.unsafeClient
        }
        return client
    }

    private func run(clientURL: URL, arguments: [String], input: Data? = nil) throws -> Data {
        guard let nodePath = nodeCandidates.first(where: { fileManager.isExecutableFile(atPath: $0) }) else {
            throw FeishuBridgeClientError.nodeMissing
        }
        let process = Process()
        process.executableURL = URL(fileURLWithPath: nodePath)
        process.currentDirectoryURL = clientURL.deletingLastPathComponent().deletingLastPathComponent()
        process.arguments = [clientURL.path] + arguments
        let output = Pipe()
        let errors = Pipe()
        let stdout = FeishuLockedDataBuffer()
        let stderr = FeishuLockedDataBuffer()
        output.fileHandleForReading.readabilityHandler = { handle in stdout.append(handle.availableData) }
        errors.fileHandleForReading.readabilityHandler = { handle in stderr.append(handle.availableData) }
        process.standardOutput = output
        process.standardError = errors

        do {
            if let input {
                let stdin = Pipe()
                process.standardInput = stdin
                try process.run()
                stdin.fileHandleForWriting.write(input)
                try stdin.fileHandleForWriting.close()
            } else {
                try process.run()
            }
            process.waitUntilExit()
        } catch {
            output.fileHandleForReading.readabilityHandler = nil
            errors.fileHandleForReading.readabilityHandler = nil
            throw error
        }
        output.fileHandleForReading.readabilityHandler = nil
        errors.fileHandleForReading.readabilityHandler = nil
        stdout.append(output.fileHandleForReading.readDataToEndOfFile())
        stderr.append(errors.fileHandleForReading.readDataToEndOfFile())
        guard process.terminationStatus == 0 else {
            let message = Self.errorMessage(from: stderr.data)
            throw FeishuBridgeClientError.commandFailed(message)
        }
        guard !stdout.data.isEmpty else { throw FeishuBridgeClientError.invalidResponse }
        return stdout.data
    }

    private static func errorMessage(from data: Data) -> String {
        if let response = try? JSONDecoder().decode(ErrorResponse.self, from: data) {
            return response.error
        }
        return String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private struct StatusResponse: Decodable {
        struct Launchd: Decodable { let running: Bool? }
        struct PID: Decodable { let alive: Bool }
        struct Outbound: Decodable { let enabled: Bool; let dryRun: Bool }
        let launchd: Launchd
        let pid: PID
        let outbound: Outbound
    }

    private struct TargetsResponse: Decodable {
        struct Targets: Decodable {
            struct Message: Decodable {
                let alias: String
                let taskLinkEligible: Bool
            }
            let messages: [Message]
        }
        let targets: Targets
    }

    private struct ErrorResponse: Decodable { let error: String }
    private struct TaskLinkProtocolResponse: Decodable {
        struct Readiness: Decodable {
            let ready: Bool
            let blockers: [String]
        }
        let `protocol`: String
        let version: Int
        let readiness: Readiness
    }
    private struct TaskLinkListResponse: Decodable { let links: [FeishuTaskLinkSnapshot] }
    private struct TaskLinkMutationResponse: Decodable { let link: FeishuTaskLinkSnapshot }
}

private final class FeishuLockedDataBuffer: @unchecked Sendable {
    private let lock = NSLock()
    private var storage = Data()
    var data: Data {
        lock.lock()
        defer { lock.unlock() }
        return storage
    }
    func append(_ data: Data) {
        guard !data.isEmpty else { return }
        lock.lock()
        storage.append(data)
        lock.unlock()
    }
}
