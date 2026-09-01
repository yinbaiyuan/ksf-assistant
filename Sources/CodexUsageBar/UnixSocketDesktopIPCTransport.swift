import CodexUsageCore
import Darwin
import Foundation

enum DesktopIPCTransportError: Error, LocalizedError {
    case socketMissing
    case invalidOwner
    case insecurePermissions
    case invalidSocketType
    case pathTooLong
    case systemCall(String, Int32)
    case disconnected

    var errorDescription: String? {
        switch self {
        case .socketMissing:
            return "未找到 Codex 桌面 IPC。"
        case .invalidOwner:
            return "Codex 桌面 IPC 不属于当前用户。"
        case .insecurePermissions:
            return "Codex 桌面 IPC 权限不安全。"
        case .invalidSocketType:
            return "Codex 桌面 IPC 路径不是 Unix Socket。"
        case .pathTooLong:
            return "Codex 桌面 IPC 路径过长。"
        case let .systemCall(name, code):
            return "Codex 桌面 IPC \(name) 失败（\(code)）。"
        case .disconnected:
            return "Codex 桌面 IPC 已断开。"
        }
    }
}

final class UnixSocketDesktopIPCTransport: DesktopIPCTransport, @unchecked Sendable {
    let incoming: AsyncStream<Data>

    private let continuation: AsyncStream<Data>.Continuation
    private let socketURL: URL
    private let readQueue = DispatchQueue(label: AppConfiguration.desktopIPCQueueLabel)
    private let stateLock = NSLock()
    private let writeLock = NSLock()
    private var descriptor: Int32 = -1
    private var readSource: DispatchSourceRead?
    private var finished = false

    init(socketURL: URL) {
        self.socketURL = socketURL
        var capturedContinuation: AsyncStream<Data>.Continuation!
        incoming = AsyncStream { capturedContinuation = $0 }
        continuation = capturedContinuation
    }

    func start() throws {
        let path = socketURL.path
        try validateSocket(at: path)

        let socketDescriptor = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard socketDescriptor >= 0 else {
            throw DesktopIPCTransportError.systemCall("socket", errno)
        }

        var noPipe: Int32 = 1
        _ = withUnsafePointer(to: &noPipe) { pointer in
            setsockopt(
                socketDescriptor,
                SOL_SOCKET,
                SO_NOSIGPIPE,
                pointer,
                socklen_t(MemoryLayout<Int32>.size)
            )
        }

        do {
            try connect(socketDescriptor, to: path)
        } catch {
            Darwin.close(socketDescriptor)
            throw error
        }

        stateLock.lock()
        descriptor = socketDescriptor
        stateLock.unlock()

        let source = DispatchSource.makeReadSource(fileDescriptor: socketDescriptor, queue: readQueue)
        source.setEventHandler { [weak self] in self?.readAvailableBytes() }
        readSource = source
        source.resume()
    }

    func send(_ data: Data) throws {
        writeLock.lock()
        defer { writeLock.unlock() }

        let socketDescriptor = currentDescriptor()
        guard socketDescriptor >= 0 else { throw DesktopIPCTransportError.disconnected }

        try data.withUnsafeBytes { rawBuffer in
            guard var baseAddress = rawBuffer.baseAddress else { return }
            var remaining = rawBuffer.count
            while remaining > 0 {
                let written = Darwin.send(socketDescriptor, baseAddress, remaining, 0)
                if written > 0 {
                    remaining -= written
                    baseAddress = baseAddress.advanced(by: written)
                } else if written < 0 && errno == EINTR {
                    continue
                } else {
                    throw DesktopIPCTransportError.systemCall("send", errno)
                }
            }
        }
    }

    func stop() {
        stateLock.lock()
        let socketDescriptor = descriptor
        descriptor = -1
        let source = readSource
        readSource = nil
        stateLock.unlock()

        source?.cancel()
        if socketDescriptor >= 0 {
            Darwin.shutdown(socketDescriptor, SHUT_RDWR)
            Darwin.close(socketDescriptor)
        }
        finish()
    }

    private func validateSocket(at path: String) throws {
        var info = stat()
        guard lstat(path, &info) == 0 else {
            if errno == ENOENT { throw DesktopIPCTransportError.socketMissing }
            throw DesktopIPCTransportError.systemCall("lstat", errno)
        }
        guard info.st_uid == getuid() else { throw DesktopIPCTransportError.invalidOwner }
        guard info.st_mode & S_IFMT == S_IFSOCK else { throw DesktopIPCTransportError.invalidSocketType }
        guard info.st_mode & 0o077 == 0 else { throw DesktopIPCTransportError.insecurePermissions }
    }

    private func connect(_ socketDescriptor: Int32, to path: String) throws {
        var address = sockaddr_un()
        let pathBytes = Array(path.utf8CString)
        let capacity = MemoryLayout.size(ofValue: address.sun_path)
        guard pathBytes.count <= capacity else { throw DesktopIPCTransportError.pathTooLong }

        address.sun_family = sa_family_t(AF_UNIX)
        let addressLength = MemoryLayout<sa_family_t>.size + pathBytes.count
        address.sun_len = UInt8(addressLength)
        withUnsafeMutablePointer(to: &address.sun_path) { pointer in
            pointer.withMemoryRebound(to: CChar.self, capacity: capacity) { destination in
                pathBytes.withUnsafeBufferPointer { source in
                    destination.update(from: source.baseAddress!, count: pathBytes.count)
                }
            }
        }

        let result = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(socketDescriptor, $0, socklen_t(addressLength))
            }
        }
        guard result == 0 else { throw DesktopIPCTransportError.systemCall("connect", errno) }
    }

    private func readAvailableBytes() {
        let socketDescriptor = currentDescriptor()
        guard socketDescriptor >= 0 else { return }
        var bytes = [UInt8](repeating: 0, count: 64 * 1_024)

        let count = Darwin.read(socketDescriptor, &bytes, bytes.count)
        if count > 0 {
            continuation.yield(Data(bytes.prefix(count)))
        } else if count == 0 {
            stop()
        } else if errno != EINTR && errno != EAGAIN && errno != EWOULDBLOCK {
            stop()
        }
    }

    private func currentDescriptor() -> Int32 {
        stateLock.lock()
        defer { stateLock.unlock() }
        return descriptor
    }

    private func finish() {
        stateLock.lock()
        guard !finished else {
            stateLock.unlock()
            return
        }
        finished = true
        stateLock.unlock()
        continuation.finish()
    }
}
