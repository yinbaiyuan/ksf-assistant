package tokens

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"ksfassistant/core/internal/domain"
)

var sessionIDSuffix = regexp.MustCompile(`(?i)([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

type Reader struct {
	Roots []string
}

func DefaultRoots(home string) []string {
	root := filepath.Join(home, ".codex")
	return []string{filepath.Join(root, "sessions"), filepath.Join(root, "archived_sessions")}
}

func (reader Reader) ReadDay(day time.Time) (*domain.DailyUsageBucket, error) {
	values, err := reader.ReadHistory(day, 1)
	if err != nil || len(values) == 0 {
		return nil, err
	}
	return &values[0], nil
}

func (reader Reader) ReadHistory(through time.Time, dayCount int) ([]domain.DailyUsageBucket, error) {
	if dayCount <= 0 {
		return []domain.DailyUsageBucket{}, nil
	}
	location := through.Location()
	latest := startOfDay(through)
	earliest := latest.AddDate(0, 0, -(dayCount - 1))
	end := latest.AddDate(0, 0, 1)
	files, foundRoot, err := reader.sessionFiles()
	if err != nil {
		return nil, err
	}
	if !foundRoot {
		return nil, os.ErrNotExist
	}
	byDate := map[string]*accumulator{}
	sources := newTurnCatalog(files)
	for _, path := range files {
		if info, err := os.Stat(path); err != nil || info.ModTime().Before(earliest) {
			continue
		}
		samples, err := tokenSamples(path, sources)
		if err != nil {
			continue
		}
		// A child agent has its own meter. Parentage is for project attribution,
		// never evidence that two cumulative counters are the same meter.
		for _, delta := range deltas(samples) {
			if delta.At.Before(earliest) || !delta.At.Before(end) {
				continue
			}
			key := dateKey(delta.At.In(location))
			value := byDate[key]
			if value == nil {
				value = &accumulator{complete: true}
				byDate[key] = value
			}
			value.add(delta)
		}
	}
	result := make([]domain.DailyUsageBucket, 0, dayCount)
	for index := 0; index < dayCount; index++ {
		day := earliest.AddDate(0, 0, index)
		key := dateKey(day)
		value := byDate[key]
		if value == nil {
			value = &accumulator{complete: true}
		}
		result = append(result, value.bucket(key))
	}
	return result, nil
}

func (reader Reader) sessionFiles() ([]string, bool, error) {
	type candidate struct {
		path string
		size int64
		at   time.Time
	}
	selected := map[string]candidate{}
	foundRoot := false
	for _, root := range reader.Roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		foundRoot = true
		err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() || strings.ToLower(filepath.Ext(path)) != ".jsonl" {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			current := candidate{path: path, size: info.Size(), at: info.ModTime()}
			identity := sessionIdentity(path)
			previous, exists := selected[identity]
			if !exists || current.size > previous.size || (current.size == previous.size && current.at.After(previous.at)) {
				selected[identity] = current
			}
			return nil
		})
		if err != nil {
			return nil, foundRoot, err
		}
	}
	paths := make([]string, 0, len(selected))
	for _, value := range selected {
		paths = append(paths, value.path)
	}
	sort.Strings(paths)
	return paths, foundRoot, nil
}

type usageSample struct {
	Request      bool // An exact per-request usage amount, not a cumulative snapshot.
	Inherited    bool // Copied history: baseline only, never new usage in this file.
	At           time.Time
	Total        int64
	RegularInput *int64
	CachedInput  *int64
	Output       *int64
}

type delta struct {
	At          time.Time
	Total       int64
	Breakdown   *domain.TokenUsageBreakdown
	HadBaseline bool
}

type accumulator struct {
	total    int64
	regular  int64
	cached   int64
	output   int64
	complete bool
}

func (value *accumulator) add(item delta) {
	if item.Total < 0 {
		return
	}
	value.total += item.Total
	if item.Breakdown == nil {
		if item.Total > 0 {
			value.complete = false
		}
		return
	}
	value.regular += item.Breakdown.RegularInputTokens
	value.cached += item.Breakdown.CachedInputTokens
	value.output += item.Breakdown.OutputTokens
}

func (value accumulator) bucket(date string) domain.DailyUsageBucket {
	result := domain.DailyUsageBucket{StartDate: date, Tokens: value.total}
	breakdown := domain.TokenUsageBreakdown{RegularInputTokens: value.regular, CachedInputTokens: value.cached, OutputTokens: value.output}
	if value.complete && breakdown.RegularInputTokens >= 0 && breakdown.CachedInputTokens >= 0 && breakdown.OutputTokens >= 0 && breakdown.TotalTokens() == value.total {
		result.Breakdown = &breakdown
	}
	return result
}

func tokenSamples(path string, catalogs ...*turnCatalog) ([]usageSample, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64*1024)
	result := []usageSample{}
	var sessionID, ownerID, forkID string
	seenRequests := map[string]bool{}
	for {
		line, readErr := reader.ReadBytes('\n')
		// Forks replay old events with NEW timestamps. The first session_meta
		// identifies this file; embedded metadata identifies the copied segment.
		// Modern per-request records also carry an explicit thread_id (session_id
		// is shared across agents and must not be used as a meter identity).
		if len(line) > 0 {
			if strings.Contains(string(line), `"session_meta"`) || strings.Contains(string(line), `"token_usage_record"`) || strings.Contains(string(line), `"turn_context"`) || strings.Contains(string(line), `"task_started"`) {
				var event struct {
					Type      string `json:"type"`
					Timestamp string `json:"timestamp"`
					Payload   struct {
						ID           string     `json:"id"`
						ForkedFromID string     `json:"forked_from_id"`
						TurnID       string     `json:"turn_id"`
						Type         string     `json:"type"`
						ThreadID     string     `json:"thread_id"`
						ResponseID   string     `json:"response_id"`
						Usage        tokenUsage `json:"usage"`
					} `json:"payload"`
				}
				if json.Unmarshal(line, &event) == nil {
					switch event.Type {
					case "session_meta":
						if event.Payload.ID != "" {
							if sessionID == "" {
								sessionID = event.Payload.ID
								forkID = event.Payload.ForkedFromID
								ownerID = sessionID
								// A fork can start with copied counters BEFORE copied metadata.
								if forkID != "" {
									ownerID = forkID
								}
							} else {
								ownerID = event.Payload.ID
							}
						}
					case "turn_context", "event_msg":
						if (event.Type == "turn_context" || event.Payload.Type == "task_started") && event.Payload.TurnID != "" && forkID != "" && len(catalogs) > 0 {
							if turns := catalogs[0].turns(forkID); len(turns) > 0 {
								if turns[event.Payload.TurnID] {
									ownerID = forkID
								} else {
									ownerID = sessionID
								}
							}
						}
					case "token_usage_record":
						if event.Payload.ThreadID != "" {
							if sessionID == "" {
								sessionID = sessionIdentity(path)
							}
							ownerID = event.Payload.ThreadID
							key := ownerID + "\x00" + event.Payload.ResponseID
							if sample, ok := sampleFromUsage(event.Timestamp, event.Payload.Usage); ok && event.Payload.ResponseID != "" && !seenRequests[key] {
								seenRequests[key] = true
								sample.Request = true
								sample.Inherited = ownerID != sessionID
								result = append(result, sample)
							}
						}
					}
				}
			}
			if strings.Contains(string(line), `"token_count"`) {
				if sample, ok := parseSample(line); ok {
					sample.Inherited = sessionID != "" && ownerID != "" && ownerID != sessionID
					result = append(result, sample)
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return result, readErr
		}
	}
	// Preserve stream order for equal timestamps, including replay baselines.
	sort.SliceStable(result, func(i, j int) bool { return result[i].At.Before(result[j].At) })
	return result, nil
}

type tokenUsage struct {
	InputTokens       *int64 `json:"input_tokens"`
	CachedInputTokens *int64 `json:"cached_input_tokens"`
	OutputTokens      *int64 `json:"output_tokens"`
	TotalTokens       *int64 `json:"total_tokens"`
}

func parseSample(line []byte) (usageSample, bool) {
	var envelope struct {
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type string `json:"type"`
			Info struct {
				TotalTokenUsage tokenUsage `json:"total_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &envelope) != nil || envelope.Payload.Type != "token_count" {
		return usageSample{}, false
	}
	return sampleFromUsage(envelope.Timestamp, envelope.Payload.Info.TotalTokenUsage)
}

func sampleFromUsage(timestamp string, usage tokenUsage) (usageSample, bool) {
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil || usage.TotalTokens == nil || *usage.TotalTokens < 0 {
		return usageSample{}, false
	}
	var regular *int64
	if usage.InputTokens != nil && usage.CachedInputTokens != nil {
		value := *usage.InputTokens - *usage.CachedInputTokens
		if value >= 0 {
			regular = &value
		}
	}
	return usageSample{At: at, Total: *usage.TotalTokens, RegularInput: regular, CachedInput: usage.CachedInputTokens, Output: usage.OutputTokens}, true
}

func deltas(samples []usageSample) []delta {
	result := make([]delta, 0, len(samples))
	var previous *usageSample
	usingRequests := false
	for _, sample := range samples {
		if sample.Request {
			if sample.Inherited {
				continue
			}
			usingRequests = true
			var breakdown *domain.TokenUsageBreakdown
			if sample.RegularInput != nil && sample.CachedInput != nil && sample.Output != nil {
				value := domain.TokenUsageBreakdown{RegularInputTokens: *sample.RegularInput, CachedInputTokens: *sample.CachedInput, OutputTokens: *sample.Output}
				if value.TotalTokens() == sample.Total {
					breakdown = &value
				}
			}
			result = append(result, delta{At: sample.At, Total: sample.Total, Breakdown: breakdown, HadBaseline: true})
			continue
		}
		// On upgrade, preserve earlier legacy deltas. From the first own request
		// record onward, snapshots are a second view of requests and can undercount;
		// never add them to request amounts or use them to cap request usage.
		if usingRequests {
			continue
		}

		total := sample.Total
		reset := previous != nil && sample.Total < previous.Total
		if previous != nil && !reset {
			total = sample.Total - previous.Total
		}
		var breakdown *domain.TokenUsageBreakdown
		if sample.RegularInput != nil && sample.CachedInput != nil && sample.Output != nil {
			regular, cached, output := *sample.RegularInput, *sample.CachedInput, *sample.Output
			if previous != nil && !reset && previous.RegularInput != nil && previous.CachedInput != nil && previous.Output != nil {
				regular -= *previous.RegularInput
				cached -= *previous.CachedInput
				output -= *previous.Output
			}
			candidate := domain.TokenUsageBreakdown{RegularInputTokens: regular, CachedInputTokens: cached, OutputTokens: output}
			if candidate.TotalTokens() == total {
				breakdown = &candidate
			}
		}
		if total >= 0 && !sample.Inherited {
			result = append(result, delta{At: sample.At, Total: total, Breakdown: breakdown, HadBaseline: previous != nil})
		}
		copy := sample
		previous = &copy
	}
	return result
}

func sessionIdentity(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if match := sessionIDSuffix.FindStringSubmatch(name); len(match) == 2 {
		return strings.ToLower(match[1])
	}
	return name
}

func startOfDay(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}

func dateKey(value time.Time) string { return value.Format("2006-01-02") }
