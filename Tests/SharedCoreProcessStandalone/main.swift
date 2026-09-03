import Foundation

@main
struct SharedCoreProcessStandalone {
    static func main() throws {
        let pipe = Pipe()
        let expected = Data("{\"id\":1}\n".utf8)
        pipe.fileHandleForWriting.write(expected)

        DispatchQueue.global().asyncAfter(deadline: .now() + 2) {
            try? pipe.fileHandleForWriting.close()
        }

        let startedAt = Date()
        let received = try SharedCorePipeIO.readChunk(from: pipe.fileHandleForReading)
        let elapsed = Date().timeIntervalSince(startedAt)
        try? pipe.fileHandleForReading.close()
        try? pipe.fileHandleForWriting.close()

        precondition(received == expected, "共享核心管道必须完整读取当前可用数据")
        precondition(elapsed < 0.75, "共享核心管道不得等待写端关闭")
        print("Shared-core short pipe read passed.")
    }
}
