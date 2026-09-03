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

	"codexusagebar/core/internal/domain"
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
	files, foundRoot, err := reader.sessionFiles(earliest)
	if err != nil {
		return nil, err
	}
	if !foundRoot {
		return nil, os.ErrNotExist
	}
	byDate := map[string]*accumulator{}
	records := make([]sessionRecord, 0, len(files))
	for _, path := range files {
		samples, err := tokenSamples(path)
		if err != nil {
			continue
		}
		records = append(records, sessionRecord{
			identity: sessionIdentity(path),
			metadata: sessionMetadataFor(path),
			samples:  samples,
		})
	}
	for _, group := range sessionLineages(records) {
		for _, delta := range lineageDeltas(group) {
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

func (reader Reader) sessionFiles(earliest time.Time) ([]string, bool, error) {
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
			if err != nil || info.ModTime().Before(earliest) {
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

type cumulative struct {
	At           time.Time
	Total        int64
	RegularInput *int64
	CachedInput  *int64
	Output       *int64
}

type sessionMetadata struct {
	ID       string
	ParentID string
}

type sessionRecord struct {
	identity string
	metadata sessionMetadata
	samples  []cumulative
}

type delta struct {
	At        time.Time
	Total     int64
	Breakdown *domain.TokenUsageBreakdown
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

func tokenSamples(path string) ([]cumulative, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64*1024)
	result := []cumulative{}
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && strings.Contains(string(line), `"token_count"`) {
			if sample, ok := parseSample(line); ok {
				result = append(result, sample)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return result, readErr
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].At.Before(result[j].At) })
	return result, nil
}

func sessionMetadataFor(path string) sessionMetadata {
	file, err := os.Open(path)
	if err != nil {
		return sessionMetadata{}
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 16*1024)
	for lineIndex := 0; lineIndex < 32; lineIndex++ {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && strings.Contains(string(line), `"session_meta"`) {
			if value, ok := parseSessionMetadata(line); ok {
				return value
			}
		}
		if readErr != nil {
			break
		}
	}
	return sessionMetadata{}
}

func parseSessionMetadata(line []byte) (sessionMetadata, bool) {
	var envelope struct {
		Type    string `json:"type"`
		Payload struct {
			ID             string          `json:"id"`
			ParentThreadID string          `json:"parent_thread_id"`
			ForkedFromID   string          `json:"forked_from_id"`
			Source         json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &envelope) != nil || envelope.Type != "session_meta" || envelope.Payload.ID == "" {
		return sessionMetadata{}, false
	}
	parentID := envelope.Payload.ParentThreadID
	if parentID == "" {
		parentID = envelope.Payload.ForkedFromID
	}
	if parentID == "" {
		var source struct {
			Subagent struct {
				ThreadSpawn struct {
					ParentThreadID string `json:"parent_thread_id"`
				} `json:"thread_spawn"`
			} `json:"subagent"`
		}
		if json.Unmarshal(envelope.Payload.Source, &source) == nil {
			parentID = source.Subagent.ThreadSpawn.ParentThreadID
		}
	}
	return sessionMetadata{ID: envelope.Payload.ID, ParentID: parentID}, true
}

func sessionLineages(records []sessionRecord) [][]sessionRecord {
	metadataByID := make(map[string]sessionMetadata, len(records))
	for _, record := range records {
		if record.metadata.ID != "" {
			metadataByID[record.metadata.ID] = record.metadata
		}
	}
	groups := map[string][]sessionRecord{}
	keys := []string{}
	for _, record := range records {
		key := record.identity
		if record.metadata.ID != "" {
			key = lineageRoot(record.metadata.ID, metadataByID)
		}
		if _, exists := groups[key]; !exists {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], record)
	}
	sort.Strings(keys)
	result := make([][]sessionRecord, 0, len(keys))
	for _, key := range keys {
		result = append(result, groups[key])
	}
	return result
}

func lineageRoot(id string, metadataByID map[string]sessionMetadata) string {
	current := id
	seen := map[string]bool{}
	for current != "" && !seen[current] {
		seen[current] = true
		metadata, exists := metadataByID[current]
		if !exists || metadata.ParentID == "" {
			return current
		}
		current = metadata.ParentID
	}
	return id
}

func lineageDeltas(records []sessionRecord) []delta {
	if len(records) == 1 {
		return deltas(records[0].samples)
	}
	merged := []cumulative{}
	for _, record := range records {
		merged = append(merged, record.samples...)
	}
	sort.SliceStable(merged, func(left, right int) bool {
		if merged[left].At.Equal(merged[right].At) {
			return merged[left].Total < merged[right].Total
		}
		return merged[left].At.Before(merged[right].At)
	})
	envelope := make([]cumulative, 0, len(merged))
	var maximum int64 = -1
	for _, sample := range merged {
		if sample.Total < maximum {
			continue
		}
		if sample.Total == maximum && len(envelope) > 0 && sameCumulative(envelope[len(envelope)-1], sample) {
			continue
		}
		envelope = append(envelope, sample)
		if sample.Total > maximum {
			maximum = sample.Total
		}
	}
	return deltas(envelope)
}

func sameCumulative(left, right cumulative) bool {
	return left.Total == right.Total && sameOptionalInt64(left.RegularInput, right.RegularInput) && sameOptionalInt64(left.CachedInput, right.CachedInput) && sameOptionalInt64(left.Output, right.Output)
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func parseSample(line []byte) (cumulative, bool) {
	var envelope struct {
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type string `json:"type"`
			Info struct {
				TotalTokenUsage struct {
					InputTokens       *int64 `json:"input_tokens"`
					CachedInputTokens *int64 `json:"cached_input_tokens"`
					OutputTokens      *int64 `json:"output_tokens"`
					TotalTokens       *int64 `json:"total_tokens"`
				} `json:"total_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &envelope) != nil || envelope.Payload.Type != "token_count" || envelope.Payload.Info.TotalTokenUsage.TotalTokens == nil {
		return cumulative{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, envelope.Timestamp)
	if err != nil {
		return cumulative{}, false
	}
	usage := envelope.Payload.Info.TotalTokenUsage
	var regular *int64
	if usage.InputTokens != nil && usage.CachedInputTokens != nil {
		value := *usage.InputTokens - *usage.CachedInputTokens
		if value >= 0 {
			regular = &value
		}
	}
	return cumulative{At: at, Total: *usage.TotalTokens, RegularInput: regular, CachedInput: usage.CachedInputTokens, Output: usage.OutputTokens}, true
}

func deltas(samples []cumulative) []delta {
	result := make([]delta, 0, len(samples))
	var previous *cumulative
	for _, sample := range samples {
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
		if total >= 0 {
			result = append(result, delta{At: sample.At, Total: total, Breakdown: breakdown})
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
