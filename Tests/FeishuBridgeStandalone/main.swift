import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

@main
private enum FeishuBridgeStandaloneTestRunner {
    static func main() {
        do {
            let manager = FileManager.default
            let root = manager.temporaryDirectory.appendingPathComponent("feishu-client-\(UUID().uuidString)")
            let scripts = root.appendingPathComponent("scripts", isDirectory: true)
            try manager.createDirectory(at: scripts, withIntermediateDirectories: true)
            defer { try? manager.removeItem(at: root) }

            let clientScript = scripts.appendingPathComponent("bridge-client.js")
            try Data("// protocol fixture\n".utf8).write(to: clientScript)
            try manager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: clientScript.path)

            let capturedBody = root.appendingPathComponent("captured-body.txt")
            let fakeNode = root.appendingPathComponent("fake-node")
            let executable = """
            #!/bin/sh
            if [ "$2" = "status" ]; then
              printf '%s' '{"launchd":{"loaded":true},"pid":{"alive":true},"outbound":{"enabled":true,"dryRun":false}}'
            elif [ "$2" = "targets" ]; then
              printf '%s' '{"targets":{"messages":[{"alias":"项目群","taskLinkEligible":false},{"alias":"我","taskLinkEligible":true}]}}'
            elif [ "$2" = "send" ]; then
              /bin/cat > '\(capturedBody.path)'
              printf '%s' '{"status":"sent"}'
            elif [ "$2" = "task-link" ] && [ "$3" = "protocol" ]; then
              printf '%s' '{"status":"ok","protocol":"codex-feishu-task-link-v1","version":2,"readiness":{"ready":true,"blockers":[]}}'
            elif [ "$2" = "task-link" ] && [ "$3" = "list" ]; then
              printf '%s' '{"status":"ok","protocol":"codex-feishu-task-link-v1","links":[]}'
            elif [ "$2" = "task-link" ] && [ "$3" = "create" ]; then
              /bin/cat > '\(capturedBody.path)'
              printf '%s' '{"status":"connected","protocol":"codex-feishu-task-link-v1","link":{"taskKey":"0123456789abcdef0123","title":"任务","projectName":"项目","targetAlias":"我","linkState":"active","turnState":"idle","turnOwner":"none","actionRequired":"none","controls":{"canSend":true,"canSteer":false,"canInterrupt":false,"canAnswer":false,"canRelease":true,"acceptsAttachments":true},"state":"connected","createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z","expiresAt":"2026-09-02T00:00:00Z","remainingSeconds":86400,"hasPendingMessage":false,"detailAvailable":false,"phase":"已连接","detailSummary":""}}'
            elif [ "$2" = "task-link" ] && [ "$3" = "release" ]; then
              printf '%s' '{"status":"released","protocol":"codex-feishu-task-link-v1","link":{"taskKey":"0123456789abcdef0123","title":"任务","projectName":"项目","targetAlias":"我","linkState":"released","turnState":"idle","turnOwner":"none","actionRequired":"none","controls":{"canSend":false,"canSteer":false,"canInterrupt":false,"canAnswer":false,"canRelease":false,"acceptsAttachments":false},"state":"released","createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-01T00:00:00Z","expiresAt":"2026-09-02T00:00:00Z","remainingSeconds":0,"hasPendingMessage":false,"detailAvailable":false,"phase":"已连接","detailSummary":""}}'
            else
              printf '%s' '{"status":"error","error":"unexpected command"}' >&2
              exit 1
            fi
            """
            try Data(executable.utf8).write(to: fakeNode)
            try manager.setAttributes([.posixPermissions: 0o700], ofItemAtPath: fakeNode.path)

            let client = FeishuBridgeClient(nodeCandidates: [fakeNode.path])
            let snapshot = try client.inspect(rootURL: root)
            guard snapshot.availability == .ready,
                  snapshot.targetAliases == ["我"],
                  snapshot.taskLinkProtocolVersion == 2,
                  snapshot.taskLinkReady else {
                throw TestFailure(description: "status or sanitized target aliases were not decoded")
            }

            try client.sendTest(rootURL: root, targetAlias: "我")
            let body = try String(contentsOf: capturedBody, encoding: .utf8)
            guard body == "CodexAssistant 飞书桥连接测试成功" else {
                throw TestFailure(description: "test body was not passed over stdin")
            }

            guard try client.taskLinkProtocol(rootURL: root),
                  try client.listTaskLinks(rootURL: root).isEmpty else {
                throw TestFailure(description: "task-link protocol was not detected")
            }
            let link = try client.createTaskLink(
                rootURL: root,
                threadID: "019c1234-abcd-7890-abcd-123456789abc",
                title: "任务",
                projectName: "项目",
                targetAlias: "我"
            )
            let payload = try String(contentsOf: capturedBody, encoding: .utf8)
            guard link.state == "connected",
                  link.linkState == "active",
                  link.turnState == "idle",
                  link.remainingSeconds == 86_400,
                  link.controls.canSend,
                  payload.contains("019c1234-abcd-7890-abcd-123456789abc"),
                  !payload.contains("\"cwd\""),
                  !payload.contains("\"state\""),
                  !payload.contains("--thread") else {
                throw TestFailure(description: "task-link payload did not travel through stdin")
            }
            guard try client.releaseTaskLink(rootURL: root, threadID: "019c1234-abcd-7890-abcd-123456789abc").state == "released" else {
                throw TestFailure(description: "task-link release was not decoded")
            }

			try manager.setAttributes([.posixPermissions: 0o722], ofItemAtPath: fakeNode.path)
            do {
                try client.validate(rootURL: root)
                throw TestFailure(description: "unsafe bridge client permissions were accepted")
            } catch FeishuBridgeClientError.unsafeClient {
                // Expected.
            }

			print("PASS Feishu Bridge status, task links, stdin payloads, and native executable security")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }
}
