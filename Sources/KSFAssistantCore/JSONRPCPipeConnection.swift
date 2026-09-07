import Foundation
import Darwin

public final class JSONRPCPipeConnection: @unchecked Sendable {
    public enum Failure: Error, LocalizedError {
        case closed, invalidResponse, timedOut, remote(String)
        case rpc(code: Int, message: String, data: Data?)

        public var errorDescription: String? {
            switch self {
            case .closed: return "核心服务连接已关闭。"
            case .invalidResponse: return "核心服务响应无效。"
            case .timedOut: return "等待核心服务响应超时。"
            case .remote(let message): return message
            case .rpc(_, let message, _): return message
            }
        }
    }

    private struct Pending {
        let continuation: CheckedContinuation<Data, Error>
        let timeout: DispatchWorkItem
    }

    private let input: FileHandle
    private let output: FileHandle
    private let lock = NSLock()
    private let writer = DispatchQueue(label: "ksfassistant.rpc.writer")
    private var pending: [String: Pending] = [:]
    private var closed = false
    private let limit = 8 * 1_024 * 1_024

    public init(input: FileHandle, output: FileHandle) {
        self.input = input
        self.output = output
        for handle in [input, output] {
            let flags = fcntl(handle.fileDescriptor, F_GETFL)
            _ = fcntl(handle.fileDescriptor, F_SETFL, flags | O_NONBLOCK)
        }
        _ = fcntl(input.fileDescriptor, F_SETNOSIGPIPE, 1)
        DispatchQueue(label: "ksfassistant.rpc.reader").async { self.readLoop() }
    }

    public func request(method: String, params: [String: Any], timeout: TimeInterval = 45) async throws -> Data {
        let id = UUID().uuidString
        let payload = try JSONSerialization.data(withJSONObject: ["jsonrpc": "2.0", "id": id, "method": method, "params": params]) + Data([10])
        guard payload.count <= limit else { throw Failure.invalidResponse }
        return try await withTaskCancellationHandler(operation: {
            try await withCheckedThrowingContinuation { continuation in
                let timer = DispatchWorkItem { [weak self] in self?.finish(id, result: .failure(Failure.timedOut)) }
                lock.lock()
                if closed || Task.isCancelled {
                    lock.unlock()
                    continuation.resume(throwing: Task.isCancelled ? CancellationError() : Failure.closed)
                    return
                }
                pending[id] = Pending(continuation: continuation, timeout: timer)
                lock.unlock()
                DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + timeout, execute: timer)
                writer.async { self.write(payload, id: id) }
            }
        }, onCancel: { self.finish(id, result: .failure(CancellationError())) })
    }

    public func close() {
        failAll(Failure.closed)
    }

    private var isClosed: Bool {
        lock.lock()
        defer { lock.unlock() }
        return closed
    }

    private func write(_ data: Data, id: String) {
        lock.lock()
        let active = pending[id] != nil && !closed
        lock.unlock()
        guard active else { return }
        var offset = 0
        while offset < data.count && !isClosed {
            if offset == 0 {
                lock.lock()
                let active = pending[id] != nil
                lock.unlock()
                if !active { return }
            }
            var descriptor = pollfd(fd: input.fileDescriptor, events: Int16(POLLOUT), revents: 0)
            let ready = Darwin.poll(&descriptor, 1, 100)
            if ready < 0 && errno == EINTR { continue }
            if ready < 0 { failAll(Failure.closed); return }
            if ready == 0 { continue }
            lock.lock()
            guard !closed, offset > 0 || pending[id] != nil else { lock.unlock(); return }
            let count = data.withUnsafeBytes { buffer in
                Darwin.write(input.fileDescriptor, buffer.baseAddress!.advanced(by: offset), data.count - offset)
            }
            let code = errno
            lock.unlock()
            if count < 0 && (code == EINTR || code == EAGAIN) { continue }
            if count <= 0 { failAll(Failure.closed); return }
            offset += count
        }
    }

    private func readLoop() {
        defer {
            try? output.close()
            writer.async { try? self.input.close() }
        }
        var buffer = Data()
        var bytes = [UInt8](repeating: 0, count: 64 * 1_024)
        while !isClosed {
            var descriptor = pollfd(fd: output.fileDescriptor, events: Int16(POLLIN), revents: 0)
            let ready = Darwin.poll(&descriptor, 1, 100)
            if ready < 0 && errno == EINTR { continue }
            if ready < 0 { failAll(Failure.closed); return }
            if ready == 0 { continue }
            let count = Darwin.read(output.fileDescriptor, &bytes, bytes.count)
            if count < 0 && (errno == EINTR || errno == EAGAIN) { continue }
            if count <= 0 { failAll(Failure.closed); return }
            buffer.append(contentsOf: bytes.prefix(count))
            while let newline = buffer.firstIndex(of: 10) {
                let line = Data(buffer[..<newline])
                buffer.removeSubrange(...newline)
                guard line.count <= limit else { failAll(Failure.invalidResponse); return }
                receive(line)
            }
            if buffer.count > limit { failAll(Failure.invalidResponse); return }
        }
    }

    private func receive(_ data: Data) {
        do {
            guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any], object["jsonrpc"] as? String == "2.0" else { throw Failure.invalidResponse }
            guard let id = object["id"] as? String else {
                if object["method"] is String && object["id"] == nil { return }
                throw Failure.invalidResponse
            }
            if let error = object["error"] as? [String: Any] {
                let details = try error["data"].map { try JSONSerialization.data(withJSONObject: $0, options: [.fragmentsAllowed]) }
                finish(id, result: .failure(Failure.rpc(code: error["code"] as? Int ?? -32000, message: error["message"] as? String ?? "核心服务调用失败。", data: details)))
            } else if let result = object["result"] {
                finish(id, result: .success(try JSONSerialization.data(withJSONObject: result, options: [.fragmentsAllowed])))
            } else {
                finish(id, result: .failure(Failure.invalidResponse))
            }
        } catch {
            failAll(Failure.invalidResponse)
        }
    }

    private func finish(_ id: String, result: Result<Data, Error>) {
        lock.lock()
        let entry = pending.removeValue(forKey: id)
        lock.unlock()
        entry?.timeout.cancel()
        entry?.continuation.resume(with: result)
    }

    private func failAll(_ error: Error) {
        lock.lock()
        closed = true
        let entries = Array(pending.values)
        pending.removeAll()
        lock.unlock()
        for entry in entries {
            entry.timeout.cancel()
            entry.continuation.resume(throwing: error)
        }
    }
}
