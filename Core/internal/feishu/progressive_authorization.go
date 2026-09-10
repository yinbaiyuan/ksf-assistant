package feishu

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"ksfassistant/core/internal/privatestore"
)

const progressiveAuthorizationRequestFilename = "progressive-authorization-request-v1.json"

type ProgressiveAuthorizationRequest struct {
	SchemaVersion int      `json:"schemaVersion"`
	ID            string   `json:"id"`
	ApplicationID string   `json:"applicationId"`
	Purpose       string   `json:"purpose"`
	Scopes        []string `json:"scopes"`
	CreatedAt     string   `json:"createdAt"`
}

type ProgressiveAuthorizationRequiredError struct {
	RequestID string
}

func (err *ProgressiveAuthorizationRequiredError) Error() string {
	return "progressive_authorization_required:" + err.RequestID
}

func progressiveAuthorizationRequestPath(dataRoot string) string {
	return filepath.Join(dataRoot, "private-cache", progressiveAuthorizationRequestFilename)
}

func readProgressiveAuthorizationRequest(dataRoot string) (*ProgressiveAuthorizationRequest, error) {
	var request ProgressiveAuthorizationRequest
	missing, err := privatestore.ReadJSON(progressiveAuthorizationRequestPath(dataRoot), &request)
	if err != nil || missing {
		return nil, err
	}
	if request.SchemaVersion != 1 || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(request.ID) || !regexp.MustCompile(`^cli_[A-Za-z0-9_-]{1,124}$`).MatchString(request.ApplicationID) || strings.TrimSpace(request.Purpose) == "" || len(request.Purpose) > 240 || len(request.Scopes) == 0 {
		return nil, errors.New("渐进授权请求无效")
	}
	allowed, err := RequiredPermissionScopes()
	if err != nil {
		return nil, err
	}
	allowedSet := make(map[string]bool, len(allowed.User))
	for _, scope := range allowed.User {
		allowedSet[scope] = true
	}
	seen := map[string]bool{}
	for _, scope := range request.Scopes {
		if !allowedSet[scope] || seen[scope] {
			return nil, errors.New("渐进授权范围无效")
		}
		seen[scope] = true
	}
	return &request, nil
}

func writeProgressiveAuthorizationRequest(dataRoot, appID, purpose string, scopes []string) (*ProgressiveAuthorizationRequest, error) {
	if !filepath.IsAbs(dataRoot) || filepath.Clean(dataRoot) != dataRoot || !regexp.MustCompile(`^cli_[A-Za-z0-9_-]{1,124}$`).MatchString(appID) {
		return nil, errors.New("渐进授权上下文无效")
	}
	allowed, err := RequiredPermissionScopes()
	if err != nil {
		return nil, err
	}
	allowedSet := make(map[string]bool, len(allowed.User))
	for _, scope := range allowed.User {
		allowedSet[scope] = true
	}
	set := map[string]bool{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || !allowedSet[scope] {
			return nil, errors.New("能力描述符包含未受管授权范围")
		}
		set[scope] = true
	}
	if len(set) == 0 {
		return nil, errors.New("能力描述符未声明授权范围")
	}
	clean := make([]string, 0, len(set))
	for scope := range set {
		clean = append(clean, scope)
	}
	sort.Strings(clean)
	purpose = strings.TrimSpace(purpose)
	if purpose == "" || len(purpose) > 240 {
		return nil, errors.New("渐进授权用途无效")
	}
	var request *ProgressiveAuthorizationRequest
	path := progressiveAuthorizationRequestPath(dataRoot)
	err = privatestore.WithFileLock(path+".lock", func() error {
		existing, readErr := readProgressiveAuthorizationRequest(dataRoot)
		if readErr != nil {
			return readErr
		}
		if existing != nil && existing.ApplicationID == appID && existing.Purpose == purpose && slices.Equal(existing.Scopes, clean) {
			request = existing
			return nil
		}
		var random [16]byte
		if _, randomErr := rand.Read(random[:]); randomErr != nil {
			return randomErr
		}
		request = &ProgressiveAuthorizationRequest{SchemaVersion: 1, ID: hex.EncodeToString(random[:]), ApplicationID: appID, Purpose: purpose, Scopes: clean, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		return privatestore.WriteJSON(path, request)
	})
	return request, err
}

func removeProgressiveAuthorizationRequest(dataRoot string) error {
	err := os.Remove(progressiveAuthorizationRequestPath(dataRoot))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func ensureCapabilityUserAuthorization(ctx context.Context, runner CapabilityExecutor, definition CapabilityDefinition) error {
	if definition.Identity != "user" || len(definition.RequiredScopes) == 0 {
		return nil
	}
	status, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--verify", "--json"}, nil, 15*time.Second)
	if err != nil {
		return err
	}
	appID, _ := status["appId"].(string)
	identities, _ := status["identities"].(map[string]any)
	user, _ := identities["user"].(map[string]any)
	granted := stringList(user["scope"])
	missing := comparePermissionScopes(definition.RequiredScopes, granted).Missing
	if len(missing) == 0 {
		return nil
	}
	request, err := writeProgressiveAuthorizationRequest(runner.DataRoot, appID, definition.ID, missing)
	if err != nil {
		return err
	}
	return &ProgressiveAuthorizationRequiredError{RequestID: request.ID}
}
