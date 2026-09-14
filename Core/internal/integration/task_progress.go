package integration

import (
	"fmt"
	"strings"
)

type taskProgressSegment struct {
	ID   string `json:"id,omitempty"`
	Text string `json:"text"`
}

func desktopTurnProgressSegments(turn map[string]any) []taskProgressSegment {
	items, _ := turn["items"].([]any)
	segments := make([]taskProgressSegment, 0, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item == nil || fmt.Sprint(item["type"]) != "agentMessage" {
			continue
		}
		// Explicitly allow only public channels; unknown/private phases stay out.
		phase := strings.ToLower(cleanString(item["phase"]))
		if phase != "commentary" && phase != "final_answer" && phase != "" {
			continue
		}
		text := normalizePublicText(fmt.Sprint(item["text"]))
		if text == "" {
			continue
		}
		id := cleanString(item["id"])
		if id == "" {
			id = cleanString(item["itemId"])
		}
		segment := taskProgressSegment{ID: id, Text: text}
		if id == "" && len(segments) > 0 && segments[len(segments)-1].ID == "" && segments[len(segments)-1].Text == text {
			continue
		}
		segments = append(segments, segment)
	}
	return segments
}

func mergeTaskProgressSegments(existing, incoming []taskProgressSegment) []taskProgressSegment {
	result := append([]taskProgressSegment(nil), existing...)
	if len(incoming) == 0 {
		return result
	}
	positions := map[string]int{}
	for index, segment := range result {
		if segment.ID != "" {
			positions[segment.ID] = index
		}
	}
	for _, segment := range incoming {
		if segment.ID != "" {
			if index, found := positions[segment.ID]; found {
				result[index] = segment
			}
		}
	}

	maximum := len(result)
	if len(incoming) < maximum {
		maximum = len(incoming)
	}
	for size := maximum; size > 0; size-- {
		for start := 0; start+size <= len(incoming); start++ {
			matched := true
			for index := 0; index < size; index++ {
				if !sameTaskProgressSegment(result[len(result)-size+index], incoming[start+index]) {
					matched = false
					break
				}
			}
			if matched {
				return append(result, incoming[start+size:]...)
			}
		}
	}
	pending := make([]taskProgressSegment, 0, len(incoming))
	for _, segment := range incoming {
		if segment.ID != "" {
			if _, found := positions[segment.ID]; found {
				continue
			}
		}
		pending = append(pending, segment)
	}
	return append(result, pending...)
}

func sameTaskProgressSegment(first, second taskProgressSegment) bool {
	if first.ID != "" && second.ID != "" {
		return first.ID == second.ID
	}
	if strings.HasPrefix(first.Text, omittedProgressPrefix) {
		return strings.HasSuffix(second.Text, strings.TrimPrefix(first.Text, omittedProgressPrefix))
	}
	return first.Text == second.Text
}

func taskProgressText(segments []taskProgressSegment) string {
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		if text := normalizePublicText(segment.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func normalizePublicText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}

func taskLinkProgressSegments(link TaskLink) []taskProgressSegment {
	if link.TurnState != "running" || link.ExtraString("progressTurnId") == "" || link.ExtraString("progressTurnId") != link.ExtraString("latestInputTurnId") {
		return nil
	}
	var segments []taskProgressSegment
	if !link.ExtraValue("progressSegments", &segments) {
		return nil
	}
	return segments
}
