import Foundation
import XCTest
@testable import KSFAssistantCore

final class LocalTokenPresentationTests: XCTestCase {
    func testLocalOnlySnapshotPreservesDeviceDataWithoutAccountHistory() throws {
        let snapshot = UsageSnapshot(
            buckets: [RateLimitBucket(limitId: "codex", primary: RateLimitWindow(usedPercent: 20))],
            dailyUsageBuckets: [DailyUsageBucket(startDate: "2026-09-09", tokens: 300)],
            localDailyUsage: DailyUsageBucket(startDate: "2026-09-09", tokens: 35),
            localDailyCost: TokenCostEstimate(planId: "fixture", status: .complete),
            rateUpdatedAt: Date(timeIntervalSince1970: 1),
            tokenUpdatedAt: Date(timeIntervalSince1970: 1)
        )
        let cached = try JSONDecoder().decode(UsageSnapshot.self, from: JSONEncoder().encode(snapshot.localOnly))
        XCTAssertNil(cached.headlineRemainingPercent)
        XCTAssertTrue(cached.buckets.isEmpty)
        XCTAssertTrue(cached.dailyUsageBuckets.isEmpty)
        XCTAssertNil(cached.rateUpdatedAt)
        XCTAssertNil(cached.tokenUpdatedAt)
        XCTAssertEqual(cached.localDailyUsage, snapshot.localDailyUsage)
        XCTAssertEqual(cached.localDailyCost, snapshot.localDailyCost)
    }

    func testAccountCacheAndFailedRefreshCannotDisplayUnverifiedQuota() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let store = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/SnapshotStore.swift"))
        let model = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsageViewModel.swift"))
        XCTAssertTrue(store.contains("decode(UsageSnapshot.self, from: data).localOnly"))
        XCTAssertTrue(store.contains("encode(snapshot.localOnly)"))
        XCTAssertFalse(model.contains("status = snapshot?.headlineRemainingPercent == nil ? .offline : .stale"))
        XCTAssertTrue(model.contains("refreshSharedDashboard(forceAccountRefresh: true)"))
    }

    func testAccountRefreshPreservesVisibleQuotaAndUsesOnlyHeaderSpinner() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let model = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsageViewModel.swift"))
        let view = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsagePopoverView.swift"))
        XCTAssertFalse(model.contains("if forceAccountRefresh {\n            snapshot = snapshot?.localOnly"))
        XCTAssertTrue(model.contains("if isRefreshing || status == .loading { return nil }"))
        XCTAssertTrue(view.contains("if viewModel.isRefreshing || viewModel.status == .loading"))
        XCTAssertTrue(view.contains(".accessibilityLabel(\"正在读取 Codex 用量\")"))
    }

    func testCachedCostIsBoundToItsLocalUsageDate() throws {
        let cost = TokenCostEstimate(planId: "fixture", status: .complete, totalMicroUsd: 157_110_000)
        let cached = UsageSnapshot(
            localDailyUsage: DailyUsageBucket(startDate: "2026-09-08", tokens: 120),
            localDailyCost: cost
        )
        let restored = try JSONDecoder().decode(UsageSnapshot.self, from: JSONEncoder().encode(cached))
        XCTAssertEqual(restored.localCost(on: "2026-09-08"), cost)
        XCTAssertNil(restored.localCost(on: "2026-09-09"))
        XCTAssertNil(UsageSnapshot(localDailyCost: cost).localCost(on: "2026-09-09"))
    }

    func testFreshZeroCostIsNotUnavailable() {
        let zeroCost = TokenCostEstimate(planId: "fixture", status: .complete)
        let snapshot = UsageSnapshot(
            localDailyUsage: DailyUsageBucket(startDate: "2026-09-09", tokens: 0),
            localDailyCost: zeroCost
        )
        XCTAssertEqual(snapshot.localCost(on: "2026-09-09"), zeroCost)
        XCTAssertNil(UsageSnapshot(localDailyUsage: snapshot.localDailyUsage).localCost(on: "2026-09-09"))
    }

    func testHomeCostDoesNotReadUndatedSnapshotDirectly() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let view = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsagePopoverView.swift"))
        XCTAssertFalse(view.contains("formattedCost(viewModel.snapshot?.localDailyCost)"))
        XCTAssertTrue(view.contains("formattedCost(viewModel.localTodayCost)"))
    }
}
