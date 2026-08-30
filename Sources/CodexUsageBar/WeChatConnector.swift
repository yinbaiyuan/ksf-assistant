import CodexUsageCore
import CryptoKit
import Foundation
import Security

private enum WeChatSecret: String {
    case bundle = "credential-bundle-v1"
    case botToken = "bot-token"
    case stateKey = "state-key"
}

private struct WeChatKeychain {
    private let service = "com.lawis.codexusagebar.wechat"

    func read(_ secret: WeChatSecret) throws -> Data? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: secret.rawValue,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data else {
            throw KeychainError(status: status)
        }
        return data
    }

    func write(_ data: Data, for secret: WeChatSecret) throws {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: secret.rawValue,
        ]
        // Preserve the existing ACL when rotating a secret. In particular, do not
        // overwrite the user's persisted “Always Allow” decision on every write.
        let updateStatus = SecItemUpdate(
            query as CFDictionary,
            [kSecValueData as String: data] as CFDictionary
        )
        if updateStatus == errSecItemNotFound {
            var item = query
            item[kSecValueData as String] = data
            item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
            item[kSecAttrAccess as String] = try currentApplicationAccess()
            let addStatus = SecItemAdd(item as CFDictionary, nil)
            guard addStatus == errSecSuccess else { throw KeychainError(status: addStatus) }
        } else if updateStatus != errSecSuccess {
            throw KeychainError(status: updateStatus)
        }
    }

    private func currentApplicationAccess() throws -> SecAccess {
        guard let executablePath = Bundle.main.executablePath else {
            throw KeychainError(status: errSecParam)
        }
        var trustedApplication: SecTrustedApplication?
        let trustedStatus = SecTrustedApplicationCreateFromPath(
            executablePath,
            &trustedApplication
        )
        guard trustedStatus == errSecSuccess, let trustedApplication else {
            throw KeychainError(status: trustedStatus)
        }
        var access: SecAccess?
        let accessStatus = SecAccessCreate(
            "Codex Usage Bar 微信连接" as CFString,
            [trustedApplication] as CFArray,
            &access
        )
        guard accessStatus == errSecSuccess, let access else {
            throw KeychainError(status: accessStatus)
        }
        return access
    }

    func delete(_ secret: WeChatSecret) throws {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: secret.rawValue,
        ]
        let status = SecItemDelete(query as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError(status: status)
        }
    }

    private struct KeychainError: LocalizedError {
        let status: OSStatus
        var errorDescription: String? { "安全存储操作失败（\(status)）。" }
    }
}

private struct WeChatPersistentState: Codable {
    var version = 1
    var credentials: WeChatCredentials
    var cursor = ""
    var contextToken: String?
    var lastOwnerMessageAt: Date?
    var reminderSentForMessageAt: Date?
    var commands: [InboundCommand] = []
    var processedMessageKeys: [String] = []
}

private struct WeChatSecretBundle: Codable {
    var botToken: String?
    var stateKey: Data?
}

private final class EncryptedWeChatStateStore {
    private let keychain = WeChatKeychain()
    private let fileURL: URL
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()
    private var cachedSecrets: WeChatSecretBundle?

    init(fileManager: FileManager = .default) {
        let support = (try? fileManager.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )) ?? fileManager.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support")
        let directory = support.appendingPathComponent("Codex Usage Bar", isDirectory: true)
        try? fileManager.createDirectory(at: directory, withIntermediateDirectories: true)
        fileURL = directory.appendingPathComponent("wechat-state-v1.enc")
    }

    func load() throws -> WeChatPersistentState? {
        guard FileManager.default.fileExists(atPath: fileURL.path) else { return nil }
        guard let keyData = try secrets().stateKey else { throw StoreError.missingKey }
        let sealed = try AES.GCM.SealedBox(combined: Data(contentsOf: fileURL))
        let clear = try AES.GCM.open(sealed, using: SymmetricKey(data: keyData))
        let state = try decoder.decode(WeChatPersistentState.self, from: clear)
        guard state.version == 1 else { throw StoreError.unsupportedVersion }
        return state
    }

    func save(_ state: WeChatPersistentState) throws {
        var secretBundle = try secrets()
        if secretBundle.stateKey == nil {
            secretBundle.stateKey = Data((0 ..< 32).map { _ in UInt8.random(in: .min ... .max) })
            try persist(secretBundle)
        }
        guard let keyData = secretBundle.stateKey else { throw StoreError.missingKey }
        let clear = try encoder.encode(state)
        let sealed = try AES.GCM.seal(clear, using: SymmetricKey(data: keyData))
        guard let combined = sealed.combined else { throw StoreError.encryptionFailed }
        try combined.write(to: fileURL, options: [.atomic, .completeFileProtectionUnlessOpen])
    }

    func readToken() throws -> String? {
        try secrets().botToken
    }

    func saveToken(_ token: String) throws {
        var secretBundle = try secrets()
        secretBundle.botToken = token
        try persist(secretBundle)
    }

    func deleteAll() throws {
        try? FileManager.default.removeItem(at: fileURL)
        try keychain.delete(.bundle)
        try? keychain.delete(.botToken)
        try? keychain.delete(.stateKey)
        cachedSecrets = nil
    }

    private func secrets() throws -> WeChatSecretBundle {
        if let cachedSecrets { return cachedSecrets }
        if let data = try keychain.read(.bundle) {
            let secretBundle = try decoder.decode(WeChatSecretBundle.self, from: data)
            cachedSecrets = secretBundle
            return secretBundle
        }

        let token = try keychain.read(.botToken).flatMap { String(data: $0, encoding: .utf8) }
        let stateKey = try keychain.read(.stateKey)
        let secretBundle = WeChatSecretBundle(botToken: token, stateKey: stateKey)
        if token != nil || stateKey != nil {
            try persist(secretBundle)
            try? keychain.delete(.botToken)
            try? keychain.delete(.stateKey)
        } else {
            cachedSecrets = secretBundle
        }
        return secretBundle
    }

    private func persist(_ secretBundle: WeChatSecretBundle) throws {
        try keychain.write(try encoder.encode(secretBundle), for: .bundle)
        cachedSecrets = secretBundle
    }

    private enum StoreError: LocalizedError {
        case missingKey
        case unsupportedVersion
        case encryptionFailed

        var errorDescription: String? {
            switch self {
            case .missingKey: return "微信加密密钥缺失，请重新连接。"
            case .unsupportedVersion: return "微信本地数据版本不兼容，请重新连接。"
            case .encryptionFailed: return "无法加密微信本地数据。"
            }
        }
    }
}

actor WeChatConnector: WeChatMessaging {
    private static let sessionReminderText = "微信会话将在约 1 小时后到期，请回复任意内容，以便继续接收 Codex Usage Bar 消息。"

    nonisolated let stateUpdates: AsyncStream<WeChatConnectionState>
    nonisolated let commandUpdates: AsyncStream<InboundCommand>

    private let stateContinuation: AsyncStream<WeChatConnectionState>.Continuation
    private let commandContinuation: AsyncStream<InboundCommand>.Continuation
    private let api: WeChatILinkClient
    private let store: EncryptedWeChatStateStore
    private var persistentState: WeChatPersistentState?
    private var token: String?
    private var loginTask: Task<Void, Never>?
    private var monitorTask: Task<Void, Never>?
    private var currentState: WeChatConnectionState = .disconnected

    init(api: WeChatILinkClient = WeChatILinkClient()) {
        self.api = api
        store = EncryptedWeChatStateStore()
        var stateSink: AsyncStream<WeChatConnectionState>.Continuation!
        stateUpdates = AsyncStream { stateSink = $0 }
        stateContinuation = stateSink
        var commandSink: AsyncStream<InboundCommand>.Continuation!
        commandUpdates = AsyncStream { commandSink = $0 }
        commandContinuation = commandSink
        stateContinuation.yield(.disconnected)
    }

    deinit {
        loginTask?.cancel()
        monitorTask?.cancel()
        stateContinuation.finish()
        commandContinuation.finish()
    }

    func start() async {
        guard monitorTask == nil else { return }
        do {
            guard let saved = try store.load(), let savedToken = try store.readToken() else {
                publish(.disconnected)
                return
            }
            persistentState = pruned(saved)
            token = savedToken
            if let persistentState { try store.save(persistentState) }
            startMonitor()
        } catch {
            publish(.failed(message: "微信本地数据不可用，请断开后重新连接。"))
        }
    }

    func stop() async {
        loginTask?.cancel()
        loginTask = nil
        monitorTask?.cancel()
        monitorTask = nil
    }

    func beginLogin() async throws -> WeChatLoginChallenge {
        loginTask?.cancel()
        monitorTask?.cancel()
        monitorTask = nil
        let response = try await api.beginLogin()
        let sessionID = UUID()
        publish(.awaitingScan(qrContent: response.qrContent))
        loginTask = Task { [weak self] in
            await self?.runLogin(code: response.code)
        }
        return WeChatLoginChallenge(sessionID: sessionID, qrContent: response.qrContent)
    }

    func send(text: String) async throws {
        guard let persistentState, let token else { throw WeChatMessagingError.notConnected }
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        do {
            try await api.sendMessage(
                baseURL: persistentState.credentials.baseURL,
                token: token,
                to: persistentState.credentials.ownerUserID,
                text: trimmed,
                contextToken: persistentState.contextToken
            )
        } catch WeChatMessagingError.credentialsExpired {
            publish(.credentialsExpired)
            throw WeChatMessagingError.credentialsExpired
        }
    }

    func pendingCommands() async throws -> [InboundCommand] {
        guard let persistentState else { return [] }
        return persistentState.commands.filter { $0.status == .pending }.sorted { $0.receivedAt < $1.receivedAt }
    }

    func markCompleted(id: UUID) async throws {
        guard var state = persistentState,
              let index = state.commands.firstIndex(where: { $0.id == id }) else { return }
        state.commands[index].status = .completed
        state = pruned(state)
        try store.save(state)
        persistentState = state
    }

    func disconnect() async {
        await stop()
        try? store.deleteAll()
        persistentState = nil
        token = nil
        publish(.disconnected)
    }

    private func runLogin(code: String) async {
        publish(.connecting)
        while !Task.isCancelled {
            do {
                let result = try await api.pollLogin(code: code)
                switch result.status {
                case .waiting, .scanned:
                    try await Task.sleep(nanoseconds: 1_000_000_000)
                case .expired:
                    publish(.failed(message: "二维码已过期，请重新连接。"))
                    loginTask = nil
                    return
                case let .confirmed(newToken, credentials):
                    let state = WeChatPersistentState(credentials: credentials)
                    try store.saveToken(newToken)
                    try store.save(state)
                    token = newToken
                    persistentState = state
                    loginTask = nil
                    startMonitor()
                    return
                }
            } catch is CancellationError {
                return
            } catch {
                publish(.failed(message: "微信连接失败，请重新扫码。"))
                loginTask = nil
                return
            }
        }
    }

    private func startMonitor() {
        guard monitorTask == nil, persistentState != nil, token != nil else { return }
        monitorTask = Task { [weak self] in await self?.monitorLoop() }
    }

    private func monitorLoop() async {
        publish(.reconnecting)
        var timeout = 35_000
        var failures = 0
        while !Task.isCancelled {
            guard var state = persistentState, let token else { break }
            do {
                if WeChatSessionReminderPolicy.shouldSend(
                    lastOwnerMessageAt: state.lastOwnerMessageAt,
                    reminderSentForMessageAt: state.reminderSentForMessageAt
                ), let lastOwnerMessageAt = state.lastOwnerMessageAt {
                    try await api.sendMessage(
                        baseURL: state.credentials.baseURL,
                        token: token,
                        to: state.credentials.ownerUserID,
                        text: Self.sessionReminderText,
                        contextToken: state.contextToken
                    )
                    state.reminderSentForMessageAt = lastOwnerMessageAt
                    try store.save(state)
                    persistentState = state
                }
                let batch = try await api.getUpdates(
                    baseURL: state.credentials.baseURL,
                    token: token,
                    cursor: state.cursor,
                    timeoutMilliseconds: timeout
                )
                failures = 0
                timeout = batch.suggestedTimeoutMilliseconds ?? timeout
                state = pruned(state)
                for message in batch.messages where message.senderID == state.credentials.ownerUserID {
                    let key = "\(state.credentials.accountID):\(message.messageID)"
                    guard !state.processedMessageKeys.contains(key) else { continue }
                    state.processedMessageKeys.append(key)
                    if let contextToken = message.contextToken { state.contextToken = contextToken }
                    state.lastOwnerMessageAt = Date()
                    state.reminderSentForMessageAt = nil
                    guard let body = message.body?.trimmingCharacters(in: .whitespacesAndNewlines), !body.isEmpty else {
                        continue
                    }
                    if !WeChatCommandQueuePolicy.canAccept(state.commands) {
                        try? await api.sendMessage(
                            baseURL: state.credentials.baseURL,
                            token: token,
                            to: state.credentials.ownerUserID,
                            text: "待处理命令已满，请稍后再试。",
                            contextToken: message.contextToken
                        )
                        continue
                    }
                    let command = InboundCommand(
                        accountID: state.credentials.accountID,
                        messageID: message.messageID,
                        senderID: message.senderID,
                        body: body
                    )
                    state.commands.append(command)
                    persistentState = state
                    try store.save(state)
                    commandContinuation.yield(command)
                    try? await api.sendMessage(
                        baseURL: state.credentials.baseURL,
                        token: token,
                        to: state.credentials.ownerUserID,
                        text: "已收到，等待处理。",
                        contextToken: message.contextToken
                    )
                }
                state.cursor = batch.cursor
                state.processedMessageKeys = Array(state.processedMessageKeys.suffix(500))
                try store.save(state)
                persistentState = state
                publish(.connected)
            } catch is CancellationError {
                break
            } catch WeChatMessagingError.credentialsExpired {
                publish(.credentialsExpired)
                break
            } catch {
                failures += 1
                publish(.reconnecting)
                let delay: UInt64 = failures >= 3 ? 30_000_000_000 : 2_000_000_000
                if failures >= 3 { failures = 0 }
                do { try await Task.sleep(nanoseconds: delay) } catch { break }
            }
        }
        monitorTask = nil
    }

    private func pruned(_ original: WeChatPersistentState) -> WeChatPersistentState {
        var state = original
        state.commands = WeChatCommandQueuePolicy.pruned(state.commands)
        if state.lastOwnerMessageAt == nil {
            state.lastOwnerMessageAt = state.commands.map(\.receivedAt).max()
        }
        state.processedMessageKeys = Array(state.processedMessageKeys.suffix(500))
        return state
    }

    private func publish(_ state: WeChatConnectionState) {
        guard state != currentState else { return }
        currentState = state
        stateContinuation.yield(state)
    }
}
