import XCTest
@testable import KSFAssistantCore

final class PricingModelsTests: XCTestCase {
    func testTokenCostFormatterPreservesUsefulSmallValues() {
        XCTAssertEqual(TokenCostFormatter.usd(microUSD: 0), "$0.00")
        XCTAssertEqual(TokenCostFormatter.usd(microUSD: 24_400_000), "$24.40")
        XCTAssertEqual(TokenCostFormatter.usd(microUSD: 200_000), "$0.2000")
        XCTAssertEqual(TokenCostFormatter.usd(microUSD: 2_800), "$0.002800")
    }

    func testPricingModelsDecodeCoreServiceContract() throws {
        let data = #"{"defaultPlanId":"openai:gpt-6-astra","plans":[{"id":"openai:gpt-6-astra","provider":"OpenAI API","model":"GPT-6 Astra","displayName":"OpenAI API · GPT-6 Astra","regularInputMicroUsdPerMillion":10000000,"cachedInputMicroUsdPerMillion":1000000,"outputMicroUsdPerMillion":50000000,"builtIn":true,"sourceUrl":"https://developers.openai.com/api/docs/pricing","verifiedAt":"2026-09-08"}]}"#.data(using: .utf8)!
        let catalog = try JSONDecoder().decode(PricingCatalog.self, from: data)
        XCTAssertEqual(catalog.defaultPlanId, "openai:gpt-6-astra")
        XCTAssertEqual(catalog.plans.first?.regularInputMicroUsdPerMillion, 10_000_000)
        XCTAssertEqual(catalog.plans.first?.cachedInputMicroUsdPerMillion, 1_000_000)
        XCTAssertEqual(catalog.plans.first?.outputMicroUsdPerMillion, 50_000_000)
        XCTAssertTrue(catalog.plans.first?.builtIn == true)
    }

    func testPartialEstimateKeepsCoverageMetadata() throws {
        let data = #"{"planId":"openai:gpt-6-astra","currency":"USD","status":"partial","regularInputMicroUsd":100,"cachedInputMicroUsd":20,"outputMicroUsd":500,"totalMicroUsd":620,"uncoveredTokens":42,"incompleteDayCount":1}"#.data(using: .utf8)!
        let estimate = try JSONDecoder().decode(TokenCostEstimate.self, from: data)
        XCTAssertEqual(estimate.status, .partial)
        XCTAssertEqual(estimate.totalMicroUsd, 620)
        XCTAssertEqual(estimate.uncoveredTokens, 42)
        XCTAssertEqual(estimate.incompleteDayCount, 1)
    }

    func testHistoryComparisonDefaultsMissingPricingFallbackToFalse() throws {
        let data = #"{"days":[],"selectedPlan":null,"localCostSummary":null}"#.data(using: .utf8)!
        let comparison = try JSONDecoder().decode(TokenHistoryComparison.self, from: data)
        XCTAssertFalse(comparison.pricingFallback)
    }
}
