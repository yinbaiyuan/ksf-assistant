package feishu

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type DirectoryKind string

const (
	PersonDirectory DirectoryKind = "person"
	GroupDirectory  DirectoryKind = "group"
)

type DirectoryCandidate struct {
	Name        string        `json:"displayName"`
	Aliases     []string      `json:"aliases,omitempty"`
	Description string        `json:"description,omitempty"`
	External    bool          `json:"external,omitempty"`
	Fingerprint string        `json:"targetFingerprint"`
	Exact       bool          `json:"exact"`
	Target      MessageTarget `json:"-"`
}

type DirectoryResolution struct {
	Status             string               `json:"status"`
	Source             string               `json:"source,omitempty"`
	Target             MessageTarget        `json:"-"`
	Candidate          *DirectoryCandidate  `json:"candidate,omitempty"`
	Candidates         []DirectoryCandidate `json:"candidates,omitempty"`
	BindingFingerprint string               `json:"bindingFingerprint,omitempty"`
}

type DirectoryService struct {
	capabilities *CapabilityService
}

func NewDirectoryService(capabilities *CapabilityService) DirectoryService {
	return DirectoryService{capabilities: capabilities}
}

func (service DirectoryService) Search(ctx context.Context, kind DirectoryKind, query string, limit int) ([]DirectoryCandidate, error) {
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 100 {
		return nil, errors.New("directory_query_invalid")
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	capabilityID := "contact.user.search"
	input := map[string]any{"query": query, "page-size": minDirectoryInt(limit, 50)}
	if kind == GroupDirectory {
		capabilityID = "im.shortcut.chat.search"
		input = map[string]any{"query": query, "page-size": limit, "page-all": true}
	} else if kind != PersonDirectory {
		return nil, errors.New("directory_kind_invalid")
	}
	if service.capabilities == nil {
		return nil, errors.New("capability_service_unavailable")
	}
	prepared, err := service.capabilities.Prepare(ctx, capabilityID, input, "directory")
	if err != nil {
		return nil, err
	}
	if prepared.Operation.Status != OperationSucceeded || prepared.Result == nil {
		return nil, errors.New("directory_lookup_incomplete")
	}
	response, _ := prepared.Result["response"].(map[string]any)
	candidates := directoryCandidatesFromResponse(kind, response)
	normalized := normalizeDirectoryName(query)
	for index := range candidates {
		candidates[index].Exact = candidateHasExactName(candidates[index], normalized)
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].Exact != candidates[right].Exact {
			return candidates[left].Exact
		}
		return candidates[left].Name < candidates[right].Name
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

func (service DirectoryService) Resolve(ctx context.Context, kind DirectoryKind, name string, config ClientConfig) (DirectoryResolution, error) {
	candidates, err := service.Search(ctx, kind, name, 100)
	if err != nil {
		return DirectoryResolution{}, err
	}
	bindings := config.NameBindings
	if kind == GroupDirectory {
		bindings = config.GroupNameBindings
	}
	return resolveDirectoryCandidates(name, candidates, bindings[normalizeDirectoryName(name)]), nil
}

func (service DirectoryService) Bind(ctx context.Context, kind DirectoryKind, name, fingerprint string, store ClientConfigStore) (DirectoryResolution, error) {
	config, err := store.Load()
	if err != nil {
		return DirectoryResolution{}, err
	}
	candidates, err := service.Search(ctx, kind, name, 100)
	if err != nil {
		return DirectoryResolution{}, err
	}
	normalized := normalizeDirectoryName(name)
	var selected *DirectoryCandidate
	for index := range candidates {
		if candidateHasExactName(candidates[index], normalized) && candidates[index].Fingerprint == fingerprint {
			candidate := candidates[index]
			selected = &candidate
			break
		}
	}
	if selected == nil {
		return DirectoryResolution{}, errors.New("directory_binding_candidate_not_found")
	}
	binding := map[string]any{"name": strings.TrimSpace(name), "type": selected.Target.Type, "id": selected.Target.ID, "boundAt": time.Now().UTC()}
	if kind == PersonDirectory {
		config.NameBindings[normalized] = binding
	} else if kind == GroupDirectory {
		config.GroupNameBindings[normalized] = binding
	} else {
		return DirectoryResolution{}, errors.New("directory_kind_invalid")
	}
	if err := store.Save(config); err != nil {
		return DirectoryResolution{}, err
	}
	return DirectoryResolution{Status: "bound", Source: "explicit_binding", Candidate: selected}, nil
}

func (service DirectoryService) Unbind(kind DirectoryKind, name string, store ClientConfigStore) (bool, error) {
	config, err := store.Load()
	if err != nil {
		return false, err
	}
	normalized := normalizeDirectoryName(name)
	var existed bool
	if kind == PersonDirectory {
		_, existed = config.NameBindings[normalized]
		delete(config.NameBindings, normalized)
	} else if kind == GroupDirectory {
		_, existed = config.GroupNameBindings[normalized]
		delete(config.GroupNameBindings, normalized)
	} else {
		return false, errors.New("directory_kind_invalid")
	}
	return existed, store.Save(config)
}

func resolveDirectoryCandidates(name string, candidates []DirectoryCandidate, binding map[string]any) DirectoryResolution {
	normalized := normalizeDirectoryName(name)
	exact := []DirectoryCandidate{}
	for _, candidate := range candidates {
		if candidateHasExactName(candidate, normalized) {
			candidate.Exact = true
			exact = append(exact, candidate)
		}
	}
	if binding != nil {
		identifier := strings.TrimSpace(fmt.Sprint(binding["id"]))
		for index := range exact {
			if exact[index].Target.ID == identifier {
				candidate := exact[index]
				return DirectoryResolution{Status: "resolved", Source: "binding", Target: candidate.Target, Candidate: &candidate}
			}
		}
		return DirectoryResolution{Status: "binding_stale", BindingFingerprint: fingerprintIdentifier(identifier), Candidates: exact}
	}
	if len(exact) == 1 {
		candidate := exact[0]
		return DirectoryResolution{Status: "resolved", Source: "unique_exact_name", Target: candidate.Target, Candidate: &candidate}
	}
	if len(exact) > 1 {
		return DirectoryResolution{Status: "ambiguous", Candidates: exact}
	}
	partial := make([]DirectoryCandidate, 0, minDirectoryInt(10, len(candidates)))
	for _, candidate := range candidates {
		if candidateContainsName(candidate, normalized) {
			partial = append(partial, candidate)
			if len(partial) == 10 {
				break
			}
		}
	}
	return DirectoryResolution{Status: "not_found", Candidates: partial}
}

func directoryCandidatesFromResponse(kind DirectoryKind, response map[string]any) []DirectoryCandidate {
	result := []DirectoryCandidate{}
	seen := map[string]bool{}
	for _, object := range directoryObjects(response) {
		identifier := firstDirectoryString(object, "open_id", "openId")
		targetType := "open_id"
		if kind == GroupDirectory {
			identifier = firstDirectoryString(object, "chat_id", "chatId")
			targetType = "chat_id"
		}
		name := firstDirectoryString(object, "name", "display_name", "displayName")
		if identifier == "" || name == "" || seen[identifier] {
			continue
		}
		if status := strings.ToLower(firstDirectoryString(object, "chat_status", "chatStatus")); status == "disbanded" || status == "dissolved" || status == "deleted" {
			continue
		}
		seen[identifier] = true
		aliases := uniqueDirectoryStrings(name, firstDirectoryString(object, "en_name", "enName"))
		result = append(result, DirectoryCandidate{
			Name: name, Aliases: aliases, Description: truncateDirectoryText(firstDirectoryString(object, "description"), 160),
			External: object["external"] == true, Fingerprint: fingerprintIdentifier(identifier), Target: MessageTarget{Type: targetType, ID: identifier},
		})
	}
	return result
}

func directoryObjects(value any) []map[string]any {
	result := []map[string]any{}
	var visit func(any)
	visit = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			result = append(result, item)
			for _, child := range item {
				visit(child)
			}
		case []any:
			for _, child := range item {
				visit(child)
			}
		}
	}
	visit(value)
	return result
}

func normalizeDirectoryName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func candidateHasExactName(candidate DirectoryCandidate, normalized string) bool {
	for _, name := range append([]string{candidate.Name}, candidate.Aliases...) {
		if normalizeDirectoryName(name) == normalized {
			return true
		}
	}
	return false
}

func candidateContainsName(candidate DirectoryCandidate, normalized string) bool {
	for _, name := range append([]string{candidate.Name}, candidate.Aliases...) {
		if strings.Contains(normalizeDirectoryName(name), normalized) {
			return true
		}
	}
	return false
}

func firstDirectoryString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		text := strings.TrimSpace(fmt.Sprint(value[key]))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func uniqueDirectoryStrings(values ...string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func truncateDirectoryText(value string, maximum int) string {
	runes := []rune(strings.Join(strings.Fields(value), " "))
	if len(runes) > maximum {
		runes = runes[:maximum]
	}
	return string(runes)
}

func minDirectoryInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
