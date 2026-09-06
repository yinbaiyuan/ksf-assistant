import Foundation
import KSFAssistantCore
import XCTest

final class TaskRuntimePresentationTests: XCTestCase {
    func testAgentCompletionNeverOverridesObservedRunningClassification() throws {
        let data = Data("""
        {"scope":"project","reportedStatus":"completed","reportFreshness":"stale","routeFreshness":"current","observedStatus":"running","progress":{"summary":"reported only","percent":100}}
        """.utf8)
        let runtime = try JSONDecoder().decode(ProjectTaskRuntime.self, from: data)
        let task = ProjectTaskItem(threadID: "fixture", hostID: "local", classification: .running, taskRuntime: runtime, createdAt: Date(), projectID: "fixture")
        XCTAssertEqual(task.classification, .running)
        XCTAssertEqual(runtime.reportLabel, "Agent 上报已过期")
        XCTAssertEqual(runtime.reportedStatusLabel, "已完成")
        XCTAssertEqual(runtime.routeLabel, "KSF 来源已验证 · 当前有效")
    }

    func testMissingOrStaleRuntimeNeverBecomesVerifiedRoute() throws {
        let task = ProjectTaskItem(threadID: "fixture", hostID: "local", classification: .waiting, createdAt: Date(), projectID: "fixture")
        XCTAssertNil(task.taskRuntime)
        for freshness in ["stale", "unverified", "unavailable", "unknown"] {
            let data = Data("""
            {"scope":"unresolved","reportedStatus":"unknown","reportFreshness":"recent","routeFreshness":"\(freshness)","observedStatus":"waiting"}
            """.utf8)
            let runtime = try JSONDecoder().decode(ProjectTaskRuntime.self, from: data)
            XCTAssertFalse(runtime.routeLabel.contains("当前有效"))
        }
    }
}
