import CodexUsageCore
import Foundation

final class ProcessAppServerTransport: AppServerTransport {
    let lines: AsyncStream<String>

    private let continuation: AsyncStream<String>.Continuation
    private let executableURL: URL
    private let process = Process()
    private let inputPipe = Pipe()
    private let outputPipe = Pipe()
    private let errorPipe = Pipe()
    private let bufferQueue = DispatchQueue(label: AppConfiguration.appServerQueueLabel)
    private let stateLock = NSLock()
    private var buffer = Data()
    private var finished = false

    init(executableURL: URL) {
        self.executableURL = executableURL
        var captured: AsyncStream<String>.Continuation!
        lines = AsyncStream { captured = $0 }
        continuation = captured
    }

    func start() throws {
        process.executableURL = executableURL
        process.arguments = ["app-server", "--listen", "stdio://"]
        process.standardInput = inputPipe
        process.standardOutput = outputPipe
        process.standardError = errorPipe
        process.currentDirectoryURL = FileManager.default.homeDirectoryForCurrentUser

        outputPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            self?.consume(handle.availableData)
        }
        // Drain stderr so the child can never block. Raw output is intentionally not persisted.
        errorPipe.fileHandleForReading.readabilityHandler = { handle in
            _ = handle.availableData
        }
        process.terminationHandler = { [weak self] _ in
            self?.finish()
        }

        try process.run()
    }

    func send(_ line: String) throws {
        guard process.isRunning else {
            throw CodexUsageError.offline("Codex App Server is not running.")
        }
        try inputPipe.fileHandleForWriting.write(contentsOf: Data((line + "\n").utf8))
    }

    func stop() {
        outputPipe.fileHandleForReading.readabilityHandler = nil
        errorPipe.fileHandleForReading.readabilityHandler = nil
        try? inputPipe.fileHandleForWriting.close()
        if process.isRunning {
            process.terminate()
        }
        finish()
    }

    private func consume(_ data: Data) {
        guard !data.isEmpty else {
            finish()
            return
        }

        bufferQueue.async { [weak self] in
            guard let self else { return }
            self.buffer.append(data)
            while let newline = self.buffer.firstIndex(of: 0x0A) {
                let lineData = self.buffer[..<newline]
                self.buffer.removeSubrange(...newline)
                guard var line = String(data: lineData, encoding: .utf8) else { continue }
                if line.last == "\r" { line.removeLast() }
                if !line.isEmpty { self.continuation.yield(line) }
            }
        }
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
