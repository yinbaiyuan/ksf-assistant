import Foundation

@main
struct CoreServiceProcessStandalone {
    static func main() throws {
        let pipe = Pipe()
        let expected = Data("{\"id\":1}\n".utf8)
        pipe.fileHandleForWriting.write(expected)

        DispatchQueue.global().asyncAfter(deadline: .now() + 2) {
            try? pipe.fileHandleForWriting.close()
        }

        let startedAt = Date()
        let received = try CoreServicePipeIO.readChunk(from: pipe.fileHandleForReading)
        let elapsed = Date().timeIntervalSince(startedAt)
        try? pipe.fileHandleForReading.close()
        try? pipe.fileHandleForWriting.close()

        precondition(received == expected, "核心服务管道必须完整读取当前可用数据")
        precondition(elapsed < 0.75, "核心服务管道不得等待写端关闭")
        print("Core service short pipe read passed.")
    }
}
