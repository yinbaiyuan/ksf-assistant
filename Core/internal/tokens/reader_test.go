package tokens

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReaderUsesPositiveCumulativeDeltasAndSeparatesCachedInput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-12345678-1234-1234-1234-123456789abc.jsonl")
	content := "" +
		`{"timestamp":"2026-09-03T01:00:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":20,"total_tokens":120}}}}` + "\n" +
		`{"timestamp":"2026-09-03T02:00:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":160,"cached_input_tokens":70,"output_tokens":30,"total_tokens":190}}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	values, err := (Reader{Roots: []string{root}}).ReadHistory(day, 1)
	if err != nil {
		t.Fatal(err)
	}
	if values[0].Tokens != 190 || values[0].Breakdown == nil {
		t.Fatalf("unexpected bucket: %#v", values[0])
	}
	if values[0].Breakdown.RegularInputTokens != 90 || values[0].Breakdown.CachedInputTokens != 70 || values[0].Breakdown.OutputTokens != 30 {
		t.Fatalf("unexpected breakdown: %#v", values[0].Breakdown)
	}
}

func TestReaderDeduplicatesActiveAndArchivedCopies(t *testing.T) {
	active := t.TempDir()
	archived := t.TempDir()
	name := "rollout-12345678-1234-1234-1234-123456789abc.jsonl"
	line := []byte(`{"timestamp":"2026-09-03T01:00:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":10}}}}` + "\n")
	if err := os.WriteFile(filepath.Join(active, name), line, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archived, name), append(line, line...), 0o600); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	values, err := (Reader{Roots: []string{active, archived}}).ReadHistory(day, 1)
	if err != nil {
		t.Fatal(err)
	}
	if values[0].Tokens != 10 {
		t.Fatalf("expected one session copy, got %d", values[0].Tokens)
	}
}

func TestReaderCountsMirroredSubagentLineageOnce(t *testing.T) {
	root := t.TempDir()
	rootID := "12345678-1234-1234-1234-123456789abc"
	childID := "22345678-1234-1234-1234-123456789abc"
	independentID := "32345678-1234-1234-1234-123456789abc"
	writeTokenFixture(t, filepath.Join(root, "rollout-"+rootID+".jsonl"), []string{
		sessionMetaLine(rootID, ""),
		tokenLine("2026-09-03T01:00:00Z", 100, 80, 30, 20),
		tokenLine("2026-09-03T02:00:00Z", 200, 160, 60, 40),
		tokenLine("2026-09-03T03:00:00Z", 300, 240, 90, 60),
	})
	writeTokenFixture(t, filepath.Join(root, "rollout-"+childID+".jsonl"), []string{
		sessionMetaLine(childID, rootID),
		tokenLine("2026-09-03T02:00:01Z", 200, 160, 60, 40),
		tokenLine("2026-09-03T03:00:01Z", 300, 240, 90, 60),
		tokenLine("2026-09-03T04:00:00Z", 350, 280, 105, 70),
	})
	writeTokenFixture(t, filepath.Join(root, "rollout-"+independentID+".jsonl"), []string{
		sessionMetaLine(independentID, ""),
		tokenLine("2026-09-03T05:00:00Z", 50, 40, 15, 10),
	})

	day := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	values, err := (Reader{Roots: []string{root}}).ReadHistory(day, 1)
	if err != nil {
		t.Fatal(err)
	}
	if values[0].Tokens != 400 || values[0].Breakdown == nil {
		t.Fatalf("unexpected lineage-aware bucket: %#v", values[0])
	}
	if values[0].Breakdown.RegularInputTokens != 200 || values[0].Breakdown.CachedInputTokens != 120 || values[0].Breakdown.OutputTokens != 80 {
		t.Fatalf("unexpected lineage-aware breakdown: %#v", values[0].Breakdown)
	}
}

func TestReaderUsesLineageBaselineAcrossNaturalDays(t *testing.T) {
	root := t.TempDir()
	rootID := "42345678-1234-1234-1234-123456789abc"
	childID := "52345678-1234-1234-1234-123456789abc"
	writeTokenFixture(t, filepath.Join(root, "rollout-"+rootID+".jsonl"), []string{
		sessionMetaLine(rootID, ""),
		tokenLine("2026-09-02T23:00:00Z", 100, 80, 30, 20),
		tokenLine("2026-09-03T01:00:00Z", 150, 120, 45, 30),
	})
	writeTokenFixture(t, filepath.Join(root, "rollout-"+childID+".jsonl"), []string{
		sessionMetaLine(childID, rootID),
		tokenLine("2026-09-02T23:00:01Z", 100, 80, 30, 20),
		tokenLine("2026-09-03T01:00:01Z", 150, 120, 45, 30),
		tokenLine("2026-09-03T02:00:00Z", 180, 144, 54, 36),
	})

	day := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	values, err := (Reader{Roots: []string{root}}).ReadHistory(day, 1)
	if err != nil {
		t.Fatal(err)
	}
	if values[0].Tokens != 80 || values[0].Breakdown == nil || values[0].Breakdown.TotalTokens() != 80 {
		t.Fatalf("unexpected cross-day lineage bucket: %#v", values[0])
	}
}

func TestReaderPreservesCumulativeBreakdownReclassification(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-62345678-1234-1234-1234-123456789abc.jsonl")
	writeTokenFixture(t, path, []string{
		tokenLine("2026-09-03T01:00:00Z", 60672, 60605, 11008, 67),
		tokenLine("2026-09-03T02:00:00Z", 60726, 60680, 60160, 46),
	})
	day := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	values, err := (Reader{Roots: []string{root}}).ReadHistory(day, 1)
	if err != nil {
		t.Fatal(err)
	}
	if values[0].Breakdown == nil || values[0].Breakdown.RegularInputTokens != 520 || values[0].Breakdown.CachedInputTokens != 60160 || values[0].Breakdown.OutputTokens != 46 {
		t.Fatalf("unexpected reclassified breakdown: %#v", values[0])
	}
}

func TestReaderRestartsBreakdownAtCounterReset(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-82345678-1234-1234-1234-123456789abc.jsonl")
	writeTokenFixture(t, path, []string{
		tokenLine("2026-09-03T01:00:00Z", 100, 80, 30, 20),
		tokenLine("2026-09-03T02:00:00Z", 40, 32, 12, 8),
		tokenLine("2026-09-03T03:00:00Z", 60, 48, 18, 12),
	})
	day := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	values, err := (Reader{Roots: []string{root}}).ReadHistory(day, 1)
	if err != nil {
		t.Fatal(err)
	}
	if values[0].Tokens != 160 || values[0].Breakdown == nil {
		t.Fatalf("unexpected reset bucket: %#v", values[0])
	}
	if values[0].Breakdown.RegularInputTokens != 80 || values[0].Breakdown.CachedInputTokens != 48 || values[0].Breakdown.OutputTokens != 32 {
		t.Fatalf("unexpected reset breakdown: %#v", values[0].Breakdown)
	}
}

func TestSessionMetadataAcceptsTopLevelStringSource(t *testing.T) {
	id := "72345678-1234-1234-1234-123456789abc"
	metadata, ok := parseSessionMetadata([]byte(sessionMetaLine(id, "")))
	if !ok || metadata.ID != id || metadata.ParentID != "" {
		t.Fatalf("unexpected metadata: %#v, ok=%t", metadata, ok)
	}
}

func sessionMetaLine(id, parentID string) string {
	if parentID == "" {
		return fmt.Sprintf(`{"type":"session_meta","payload":{"id":"%s","source":"vscode"}}`, id)
	}
	return fmt.Sprintf(`{"type":"session_meta","payload":{"id":"%s","parent_thread_id":"%s","forked_from_id":"%s","source":{"subagent":{"thread_spawn":{"parent_thread_id":"%s"}}}}}`, id, parentID, parentID, parentID)
}

func tokenLine(timestamp string, total, input, cached, output int64) string {
	return fmt.Sprintf(`{"timestamp":"%s","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"total_tokens":%d}}}}`, timestamp, input, cached, output, total)
}

func writeTokenFixture(t *testing.T, path string, lines []string) {
	t.Helper()
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
