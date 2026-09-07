package tokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ksfassistant/core/internal/domain"
)

// Shared synthetic protocol fixtures also exercise the retained Swift helper.
// No production task identifiers or conversation contents enter these fixtures.
func TestAccountingContract(t *testing.T) {
	data, err := os.ReadFile("../../../Tests/Fixtures/token-accounting.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string
		Through  string
		Zone     string
		Expected []int64
		Files    []struct {
			ID         string
			ModifiedAt string
			Parent     *string
			Lines      []string
		}
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			root := t.TempDir()
			location, err := time.LoadLocation(tc.Zone)
			if err != nil {
				t.Fatal(err)
			}
			through, err := time.Parse(time.RFC3339, tc.Through)
			if err != nil {
				t.Fatal(err)
			}
			through = through.In(location)
			start := startOfDay(through).AddDate(0, 0, -(len(tc.Expected) - 1))
			threads := []domain.CodexThread{}
			projections := map[string]domain.TaskProjection{}
			for _, file := range tc.Files {
				path := filepath.Join(root, "rollout-"+file.ID+".jsonl")
				writeTokenFixture(t, path, file.Lines)
				if file.ModifiedAt != "" {
					modified, err := time.Parse(time.RFC3339, file.ModifiedAt)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, modified, modified); err != nil {
						t.Fatal(err)
					}
				}
				threads = append(threads, domain.CodexThread{ID: file.ID, ParentThreadID: file.Parent, Path: &path, CreatedAt: start.Unix()})
				if file.Parent == nil {
					projections[file.ID] = domain.TaskProjection{Bindings: []domain.TaskBinding{{ProjectCard: "project", BoundAt: start.Format(time.RFC3339)}}}
				}
			}
			reader := Reader{Roots: []string{root}}
			got, err := reader.ReadHistory(through, len(tc.Expected))
			if err != nil {
				t.Fatal(err)
			}
			var total int64
			for i, expected := range tc.Expected {
				if got[i].Tokens != expected {
					t.Errorf("day %s: got %d, want %d", got[i].StartDate, got[i].Tokens, expected)
				}
				if got[i].Breakdown == nil || got[i].Breakdown.TotalTokens() != expected {
					t.Errorf("day %s: incomplete or inconsistent composition: %#v", got[i].StartDate, got[i].Breakdown)
				}
				day, _ := time.ParseInLocation("2006-01-02", got[i].StartDate, location)
				single, err := reader.ReadDay(day)
				if err != nil || single.Tokens != expected {
					t.Errorf("single-day scan differs from history: %#v, %v", single, err)
				}
				total += expected
			}
			usage := ReadProjectUsage([]string{"project"}, threads, projections, start, through)["project"]
			if usage.CumulativeTokens != total || usage.TodayTokens != tc.Expected[len(tc.Expected)-1] || !usage.IsComplete {
				t.Errorf("project accounting differs from local accounting: %#v, want total %d", usage, total)
			}
		})
	}
}

func TestExactRequestAfterBindingNeedsNoCounterBaseline(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-parent.jsonl")
	writeTokenFixture(t, path, []string{
		sessionMetaLine("parent", ""),
		`{"type":"token_usage_record","timestamp":"2026-09-05T12:00:00Z","payload":{"thread_id":"parent","response_id":"request","usage":{"total_tokens":100}}}`,
	})
	now := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	bound := now.Add(-2 * time.Hour)
	usage := ReadProjectUsage([]string{"project"}, []domain.CodexThread{{ID: "parent", Path: &path, CreatedAt: bound.Add(-time.Hour).Unix()}}, map[string]domain.TaskProjection{
		"parent": {Bindings: []domain.TaskBinding{{ProjectCard: "project", BoundAt: bound.Format(time.RFC3339)}}},
	}, bound, now)["project"]
	if usage.CumulativeTokens != 100 || usage.TodayTokens != 100 || !usage.IsComplete {
		t.Fatalf("exact request was treated as an unknown legacy baseline: %#v", usage)
	}
	day, err := (Reader{Roots: []string{root}}).ReadDay(now)
	if err != nil || day.Tokens != 100 || day.Breakdown != nil {
		t.Fatalf("incomplete request composition must not hide total or fabricate a split: %#v, %v", day, err)
	}
}
