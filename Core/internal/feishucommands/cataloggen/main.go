package main

import (
	"encoding/json"
	"fmt"
	"ksfassistant/core/internal/feishu"
	"os"
	"strings"
)

func main() {
	manifest, err := feishu.LoadCapabilityManifest()
	if err != nil {
		panic(err)
	}
	definitions := map[string]any{}
	public := []any{}
	for _, definition := range manifest.Capabilities {
		if !feishu.CapabilityPublished(definition) {
			continue
		}
		definitions[definition.ID] = definition.Flags
		public = append(public, publicCapability(definition))
	}
	capabilities, err := capabilitiesEnvelope()
	if err != nil {
		panic(err)
	}
	err = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"fields":       definitions,
		"catalog":      public,
		"capabilities": capabilities,
		"events":       map[string]any{"status": "ok", "fixed": true, "eventCount": len(feishu.FixedEventKeys), "events": feishu.FixedEventKeys},
	})
	if err != nil {
		panic(err)
	}
}
func publicCapability(definition feishu.CapabilityDefinition) map[string]any {
	fields := []map[string]any{}
	for _, name := range definition.FlagOrder {
		schema := definition.Flags[name]
		fields = append(fields, map[string]any{"name": name, "type": schema.Type, "required": schema.Required, "private": schema.Private})
	}
	queue := definition.Queue
	if definition.Risk == "read" {
		queue = "direct"
	}
	kinds := []string{}
	seen := map[string]bool{}
	for _, raw := range definition.ResultIdentifiers {
		var descriptor map[string]any
		if json.Unmarshal(raw, &descriptor) == nil {
			kind := strings.TrimSpace(fmt.Sprint(descriptor["kind"]))
			if kind != "" && kind != "<nil>" && !seen[kind] {
				seen[kind] = true
				kinds = append(kinds, kind)
			}
		}
	}
	var preflight, reread any
	if definition.Preflight != nil {
		preflight = definition.Preflight.ID
	}
	if definition.Reread != nil {
		reread = definition.Reread.ID
	}
	return map[string]any{"id": definition.ID, "domain": definition.Domain, "published": feishu.CapabilityPublished(definition), "identity": definition.Identity, "risk": definition.Risk, "backend": definition.Backend, "effect": definition.Effect, "reversibility": definition.Reversibility, "guardProfile": definition.GuardProfile, "postcondition": definition.Postcondition, "conflictKey": definition.ConflictKey, "retryClass": definition.RetryClass, "executionClass": definition.ExecutionClass, "requiredScopes": definition.RequiredScopes, "queue": queue, "inputFields": fields, "bounded": definition.Scope.Bounded, "preflight": preflight, "reread": reread, "savableResultKinds": kinds, "redaction": definition.Redaction}
}
func capabilitiesEnvelope() (any, error) {

	manifest, err := feishu.LoadCapabilityManifest()
	if err != nil {
		return nil, err
	}
	items := make([]any, 0, len(manifest.Capabilities))
	readCapabilities := []string{}
	queuedWriteCapabilities := []string{}
	riskCounts := map[string]int{}
	for _, definition := range manifest.Capabilities {
		if !feishu.CapabilityPublished(definition) {
			continue
		}
		items = append(items, publicCapability(definition))
		riskCounts[definition.Risk]++
		if definition.Risk == "read" {
			readCapabilities = append(readCapabilities, definition.ID)
		} else {
			queuedWriteCapabilities = append(queuedWriteCapabilities, definition.ID)
		}
	}
	scopes, err := feishu.RequiredPermissionScopes()
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "ok", "capabilities": map[string]any{
		"bridgeVersion": "2.0.0", "packageVersion": "1.2.0", "capabilityVersion": "2.0.0", "stabilityBaselineVersion": "0.6.1", "queueStateSchemaVersion": 2, "larkCliVersion": feishu.PinnedLarkCLIVersion, "frozenCatalogVersion": "1.0.92", "officialSdk": "none", "officialSdkVersion": "",
		"identities": []string{"bot", "user"}, "eventTransport": "official-cli", "singleInboundConnection": true, "events": feishu.FixedEventKeys, "managedEvents": feishu.CLIManagedEventKeys, "unsupportedEvents": []string{feishu.MailMessageReceivedEvent}, "fixedEventCatalog": true,
		"inboundMessageTypes": []string{"text", "image", "file", "audio", "media", "post"}, "outboundMessageFormats": []string{"text", "markdown", "card", "image", "file"},
		"readCapabilities": readCapabilities, "queuedWriteCapabilities": queuedWriteCapabilities,
		"registeredCapabilities": items, "registeredCapabilityCount": len(items), "riskCounts": riskCounts,
		"intentionallyExcluded": []string{"application_management", "member_admin_role_permission_management", "credential_or_secret_management", "automation_configuration", "live_meeting_control", "urgent_phone_or_sms", "arbitrary_openapi", "background_full_crawl"},
		"requiredScopes":        map[string]any{"bot": scopes.Bot, "user": scopes.User},
	}}, nil

}
