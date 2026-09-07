import Foundation
import Darwin
import KSFAssistantCore
import XCTest

final class JSONRPCPipeConnectionTests: XCTestCase {
    private final class Fixture: @unchecked Sendable {
        let requests = Pipe()
        let responses = Pipe()
        lazy var client = JSONRPCPipeConnection(input: requests.fileHandleForWriting, output: responses.fileHandleForReading)

        func readRequests(_ count: Int) throws -> [[String: Any]] {
            var buffer = Data()
            var result: [[String: Any]] = []
            while result.count < count {
                buffer.append(requests.fileHandleForReading.availableData)
                while let newline = buffer.firstIndex(of: 10) {
                    result.append(try XCTUnwrap(JSONSerialization.jsonObject(with: Data(buffer[..<newline])) as? [String: Any]))
                    buffer.removeSubrange(...newline)
                }
            }
            return result
        }

        func reply(_ object: [String: Any]) throws {
            try responses.fileHandleForWriting.write(contentsOf: JSONSerialization.data(withJSONObject: object) + Data([10]))
        }
    }

    func testTaskCardAuthorizationKeepsStructuredChallengeAcrossPipe() async throws {
        let fixture = Fixture()
        let client = fixture.client
        defer { client.close() }
        let server = Task.detached {
            let request = try fixture.readRequests(1)[0]
            try fixture.reply(["jsonrpc": "2.0", "id": request["id"]!, "error": ["code": -32063, "message": "confirmation required", "data": ["status": "authorization_required", "challenge": "fresh", "operation": ["id": "OP-fixture", "capabilityId": "im.sdk.message.send", "status": "awaiting_confirmation"]]]])
        }
        do {
            _ = try await client.request(method: "feishu/taskLink/create", params: [:])
            XCTFail("expected authorization")
        } catch {
            let authorization = try XCTUnwrap(FeishuTaskCardAuthorization.from(error))
            XCTAssertEqual(authorization.operation.id, "OP-fixture")
            XCTAssertEqual(authorization.challenge, "fresh")
            XCTAssertEqual(error.localizedDescription, "confirmation required")
        }
        try await server.value
    }

    func testOverlappingRequestsReceiveOnlyTheirOwnOutOfOrderResponse() async throws {
        let fixture = Fixture()
        let client = fixture.client
        defer { client.close() }
        let server = Task.detached {
            let requests = try fixture.readRequests(3)
            try fixture.reply(["jsonrpc": "2.0", "method": "ignoredNotification", "params": [:]])
            try fixture.reply(["jsonrpc": "2.0", "id": "late-old-request", "result": [:]])
            for request in requests.reversed() {
                try fixture.reply(["jsonrpc": "2.0", "id": request["id"]!, "result": ["method": request["method"]!]])
            }
        }
        async let business = client.request(method: "business/write", params: [:])
        async let poll = client.request(method: "userApproval/poll", params: ["interactive": true])
        async let decide = client.request(method: "userApproval/decide", params: ["id": "test", "approve": false])
        for (data, expected) in try await [(business, "business/write"), (poll, "userApproval/poll"), (decide, "userApproval/decide")] {
            let result = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: String])
            XCTAssertEqual(result["method"], expected)
        }
        try await server.value
    }

    func testPendingRequestFailsOnEOF() async throws {
        let fixture = Fixture()
        let client = fixture.client
        defer { client.close() }
        let server = Task.detached {
            _ = try fixture.readRequests(1)
            try fixture.responses.fileHandleForWriting.close()
        }
        do {
            _ = try await client.request(method: "pending", params: [:], timeout: 2)
            XCTFail("EOF must fail the pending request")
        } catch JSONRPCPipeConnection.Failure.closed {} catch { XCTFail("Unexpected error: \(error)") }
        try await server.value
    }

    func testCloseFailsAllPendingAndForbidsFurtherRequests() async throws {
        let fixture = Fixture()
        let client = fixture.client
        let server = Task.detached {
            _ = try fixture.readRequests(2)
            client.close()
        }
        await withTaskGroup(of: Void.self) { group in
            for method in ["pending-one", "pending-two"] {
                group.addTask {
                    do {
                        _ = try await client.request(method: method, params: [:], timeout: 2)
                        XCTFail("Closed connection must fail")
                    } catch JSONRPCPipeConnection.Failure.closed {} catch { XCTFail("Unexpected error: \(error)") }
                }
            }
        }
        try await server.value
        do {
            _ = try await client.request(method: "late", params: [:])
            XCTFail("Closed connection must not reopen")
        } catch JSONRPCPipeConnection.Failure.closed {} catch { XCTFail("Unexpected error: \(error)") }
    }

    func testTimeoutAndCancellationDoNotPoisonOtherResponses() async throws {
        let fixture = Fixture()
        let client = fixture.client
        defer { client.close() }
        do {
            _ = try await client.request(method: "timeout", params: [:], timeout: 0.03)
            XCTFail("Expected timeout")
        } catch JSONRPCPipeConnection.Failure.timedOut {} catch { XCTFail("Unexpected error: \(error)") }
        let requests = try fixture.readRequests(1)
        try fixture.reply(["jsonrpc": "2.0", "id": requests[0]["id"]!, "result": [:]])
        let task = Task { try await client.request(method: "cancelled", params: [:]) }
        task.cancel()
        do { _ = try await task.value; XCTFail("Expected cancellation") } catch is CancellationError {}
        let server = Task.detached {
            var request = try fixture.readRequests(1)[0]
            if request["method"] as? String == "cancelled" { request = try fixture.readRequests(1)[0] }
            try fixture.reply(["jsonrpc": "2.0", "id": request["id"]!, "result": true])
        }
        let result = try await client.request(method: "healthy", params: [:], timeout: 2)
        XCTAssertEqual(String(data: result, encoding: .utf8), "true")
        try await server.value
    }

    func testTimedOutRequestNeverStartsWritingAfterBackpressureClears() async throws {
        let fixture = Fixture()
        let client = fixture.client
        defer { client.close() }
        let output = fixture.requests.fileHandleForWriting.fileDescriptor
        let input = fixture.requests.fileHandleForReading.fileDescriptor
        _ = fcntl(input, F_SETFL, fcntl(input, F_GETFL) | O_NONBLOCK)
        let filler = [UInt8](repeating: 32, count: 4096)
        var filled = 0
        while true {
            let count = Darwin.write(output, filler, filler.count)
            if count < 0 { XCTAssertEqual(errno, EAGAIN); break }
            filled += count
        }
        XCTAssertGreaterThan(filled, 0)
        do {
            _ = try await client.request(method: "must-not-execute", params: [:], timeout: 0.03)
            XCTFail("Expected timeout while the output pipe is full")
        } catch JSONRPCPipeConnection.Failure.timedOut {} catch { XCTFail("Unexpected error: \(error)") }
        var buffer = [UInt8](repeating: 0, count: 4096)
        var drained = 0
        while drained < filled {
            let count = Darwin.read(input, &buffer, min(buffer.count, filled - drained))
            XCTAssertGreaterThan(count, 0)
            guard count > 0 else { return }
            drained += count
        }
        try await Task.sleep(nanoseconds: 150_000_000)
        XCTAssertEqual(Darwin.read(input, &buffer, buffer.count), -1, "A zero-offset timed-out request must not be sent later")
        XCTAssertEqual(errno, EAGAIN)
    }
}
