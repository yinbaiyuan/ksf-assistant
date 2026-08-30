import CodexUsageCore
import Darwin
import Foundation

enum KSFBridgeError: Error, LocalizedError {
    case scriptMissing
    case failed(String)
    case invalidResponse

    var errorDescription: String? {
        switch self {
        case .scriptMissing:
            return "KSF 面板桥接脚本不存在。"
        case let .failed(message):
            return message.isEmpty ? "KSF 面板桥接执行失败。" : message
        case .invalidResponse:
            return "KSF 面板桥接返回了无法识别的数据。"
        }
    }
}

struct KSFBridgeClient {
    private let decoder = JSONDecoder()

    func enable(rootURL: URL) throws -> Date {
        let data = try run(rootURL: rootURL, arguments: ["--enable"])
        let response = try decoder.decode(EnableResponse.self, from: data)
        guard response.enabled, let date = Self.parseDate(response.enabledAt) else {
            throw KSFBridgeError.invalidResponse
        }
        return date
    }

    func fetchCatalog(rootURL: URL) throws -> KSFProjectCatalogResponse {
        let data = try run(rootURL: rootURL, arguments: ["--export-catalog"])
        let response = try decoder.decode(KSFProjectCatalogResponse.self, from: data)
        guard response.protocol == "ksf-panel-catalog-v1" else {
            throw KSFBridgeError.invalidResponse
        }
        return response
    }

    func resolve(rootURL: URL, threadIDs: [String]) throws -> KSFTaskProjectionResolution {
        let request = try JSONSerialization.data(withJSONObject: ["threadIds": threadIDs])
        let data = try run(rootURL: rootURL, arguments: ["--resolve-projections"], input: request)
        let response = try decoder.decode(KSFTaskProjectionResolution.self, from: data)
        guard response.protocol == "ksf-task-project-resolution-v1" else {
            throw KSFBridgeError.invalidResponse
        }
        return response
    }

    private func run(rootURL: URL, arguments: [String], input: Data? = nil) throws -> Data {
        let script = rootURL
            .appendingPathComponent(".agents/skills/ksf-load-route-context/scripts/ksf_panel_bridge.rb")
        guard FileManager.default.fileExists(atPath: script.path) else {
            throw KSFBridgeError.scriptMissing
        }
        let attributes = try FileManager.default.attributesOfItem(atPath: script.path)
        let owner = (attributes[.ownerAccountID] as? NSNumber)?.uint32Value
        let permissions = (attributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0
        guard
            attributes[.type] as? FileAttributeType == .typeRegular,
            owner == getuid(),
            permissions & 0o022 == 0
        else {
            throw KSFBridgeError.failed("KSF 面板桥接脚本的所有权或权限不安全。")
        }

        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/ruby")
        process.currentDirectoryURL = rootURL
        process.arguments = [script.path, "--root", rootURL.path] + arguments
        let output = Pipe()
        let errorPipe = Pipe()
        let stdoutCapture = LockedDataBuffer()
        let stderrCapture = LockedDataBuffer()
        output.fileHandleForReading.readabilityHandler = { handle in
            let data = handle.availableData
            if !data.isEmpty { stdoutCapture.append(data) }
        }
        errorPipe.fileHandleForReading.readabilityHandler = { handle in
            let data = handle.availableData
            if !data.isEmpty { stderrCapture.append(data) }
        }
        process.standardOutput = output
        process.standardError = errorPipe
        do {
            if let input {
                let pipe = Pipe()
                process.standardInput = pipe
                try process.run()
                pipe.fileHandleForWriting.write(input)
                try pipe.fileHandleForWriting.close()
            } else {
                try process.run()
            }
            process.waitUntilExit()
        } catch {
            output.fileHandleForReading.readabilityHandler = nil
            errorPipe.fileHandleForReading.readabilityHandler = nil
            throw error
        }
        output.fileHandleForReading.readabilityHandler = nil
        errorPipe.fileHandleForReading.readabilityHandler = nil
        stdoutCapture.append(output.fileHandleForReading.readDataToEndOfFile())
        stderrCapture.append(errorPipe.fileHandleForReading.readDataToEndOfFile())
        let stdout = stdoutCapture.data
        let stderr = stderrCapture.data
        guard process.terminationStatus == 0 else {
            throw KSFBridgeError.failed(String(decoding: stderr, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines))
        }
        guard !stdout.isEmpty else { throw KSFBridgeError.invalidResponse }
        return stdout
    }

    private static func parseDate(_ value: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: value)
    }

    private struct EnableResponse: Decodable {
        let enabled: Bool
        let enabledAt: String
    }
}

private final class LockedDataBuffer: @unchecked Sendable {
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
