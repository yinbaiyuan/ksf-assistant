package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"ksfassistant/core/internal/feishu"
)

type schemaNode struct {
	Type        string                `json:"type"`
	Description string                `json:"description"`
	Flag        string                `json:"flag"`
	Carrier     string                `json:"carrier"`
	Required    []string              `json:"required"`
	Properties  map[string]schemaNode `json:"properties"`
	Items       *schemaNode           `json:"items"`
	Minimum     *float64              `json:"minimum"`
	Maximum     *float64              `json:"maximum"`
	MaxLength   *int                  `json:"maxLength"`
	Enum        []any                 `json:"enum"`
}

type cliSchemaMeta struct {
	Scopes       []string `json:"scopes"`
	Required     []string `json:"required_scopes"`
	AccessTokens []string `json:"access_tokens"`
	Risk         string   `json:"risk"`
	Danger       bool     `json:"danger"`
}

type cliSchema struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	InputSchema schemaNode    `json:"inputSchema"`
	Meta        cliSchemaMeta `json:"_meta"`
}

type capabilityAdditions struct {
	SchemaVersion  int                           `json:"schemaVersion"`
	BridgeVersion  string                        `json:"bridgeVersion"`
	LarkCLIVersion string                        `json:"larkCliVersion"`
	Capabilities   []feishu.CapabilityDefinition `json:"capabilities"`
}

type capabilityGovernanceManifest struct {
	SchemaVersion int                          `json:"schemaVersion"`
	BridgeVersion string                       `json:"bridgeVersion"`
	Capabilities  []capabilityGovernanceRecord `json:"capabilities"`
}

type capabilityGovernanceRecord struct {
	ID             string                          `json:"id"`
	Backend        string                          `json:"backend"`
	Risk           string                          `json:"risk"`
	Effect         string                          `json:"effect"`
	Reversibility  string                          `json:"reversibility"`
	GuardProfile   feishu.CapabilityGuardProfile   `json:"guardProfile"`
	Preflight      *feishu.CapabilityStep          `json:"preflight,omitempty"`
	Reread         *feishu.CapabilityStep          `json:"reread,omitempty"`
	Postcondition  *feishu.CapabilityPostcondition `json:"postcondition,omitempty"`
	ConflictKey    []string                        `json:"conflictKey"`
	RetryClass     string                          `json:"retryClass"`
	ExecutionClass string                          `json:"executionClass"`
}

var helpCommandLine = regexp.MustCompile(`^\s{2}([^\s]+)\s+(.+)$`)
var helpFlagLine = regexp.MustCompile(`^\s+(?:-[A-Za-z],\s+)?--([a-z0-9][a-z0-9-]*)(?:\s+([^\s]+))?\s{2,}(.+)$`)

var generatedDomains = map[string]bool{
	"approval": true, "attendance": true, "base": true, "calendar": true, "contact": true, "docs": true, "drive": true,
	"im": true, "mail": true, "markdown": true, "mindnotes": true, "minutes": true, "note": true,
	"okr": true, "sheets": true, "slides": true, "task": true, "vc": true, "whiteboard": true, "wiki": true,
}
var destructiveScanDomains = []string{"approval", "attendance", "base", "calendar", "docs", "drive", "im", "mail", "markdown", "mindnotes", "minutes", "note", "okr", "sheets", "slides", "task", "vc", "whiteboard", "wiki"}
var forbiddenResourceTokens = []string{"permission", "member", "manager", "admin", "role", "openapi", "secret", "automation", "workflow", "database", "plugin"}

func generateFeishuCapabilities(repoRoot, binary string) error {
	if binary == "" {
		return errors.New("--generate-feishu-capabilities requires an executable path")
	}
	if !filepath.IsAbs(binary) {
		return errors.New("lark-cli path must be absolute")
	}
	basePath := filepath.Join(repoRoot, "Core", "internal", "feishu", "contracts", "capability-manifest-1.0.0.json")
	baseDefinitions, err := baseCapabilityDefinitions(basePath)
	if err != nil {
		return err
	}
	baseIDs := map[string]bool{}
	for _, definition := range baseDefinitions {
		baseIDs[definition.ID] = true
	}
	definitions := []feishu.CapabilityDefinition{}
	seen := map[string]bool{}
	for domain := range generatedDomains {
		commands, err := discoverDomainCommands(binary, domain)
		if err != nil {
			return err
		}
		for _, command := range commands {
			if strings.HasPrefix(command, "+") {
				if !allowGeneratedCommand(domain, strings.SplitN(command, "\t", 2)[0]) {
					continue
				}
				definition, err := shortcutCapability(binary, domain, command)
				if err != nil {
					return err
				}
				appendGeneratedCapability(&definitions, seen, baseIDs, definition)
				continue
			}
			for _, schemaName := range commandSchemaNames(domain, command) {
				if !allowGeneratedCommand(domain, schemaName) {
					continue
				}
				definition, err := typedCapability(binary, schemaName)
				if err != nil {
					return err
				}
				appendGeneratedCapability(&definitions, seen, baseIDs, definition)
			}
		}
	}
	for _, domain := range destructiveScanDomains {
		commands, err := discoverDomainCommands(binary, domain)
		if err != nil {
			return err
		}
		for _, command := range commands {
			if strings.HasPrefix(command, "+") {
				if !destructiveName(command) {
					continue
				}
				definition, err := shortcutCapability(binary, domain, command)
				if err != nil {
					return err
				}
				appendGeneratedCapability(&definitions, seen, baseIDs, definition)
				continue
			}
			for _, schemaName := range commandSchemaNames(domain, command) {
				if !destructiveName(schemaName) || forbiddenGeneratedName(schemaName) {
					continue
				}
				definition, err := typedCapability(binary, schemaName)
				if err != nil {
					return err
				}
				appendGeneratedCapability(&definitions, seen, baseIDs, definition)
			}
		}
	}
	addImmediateMailCapabilities(&definitions, seen, baseIDs)
	attachApprovalDestructiveGuards(definitions)
	attachGeneratedDestructiveGuards(definitions, baseDefinitions)
	attachGeneratedExecutionGovernance(definitions)
	if err := validateGeneratedGovernance(definitions); err != nil {
		return err
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	output := capabilityAdditions{SchemaVersion: 2, BridgeVersion: "2.0.0", LarkCLIVersion: "1.0.92", Capabilities: definitions}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(repoRoot, "Core", "internal", "feishu", "contracts", "capability-additions-2.0.0.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return writeCapabilityGovernance(repoRoot, baseDefinitions, definitions, feishu.BuiltinServiceCapabilityDefinitions())
}

func writeCapabilityGovernance(repoRoot string, groups ...[]feishu.CapabilityDefinition) error {
	definitions := []feishu.CapabilityDefinition{}
	for _, group := range groups {
		definitions = append(definitions, group...)
	}
	if len(definitions) != feishu.CapabilityRegistryV2Count {
		return fmt.Errorf("governance source contains %d capabilities, expected %d", len(definitions), feishu.CapabilityRegistryV2Count)
	}
	for index := range definitions {
		definition := &definitions[index]
		if definition.Backend == "" {
			definition.Backend = "lark-cli"
		}
		if definition.Effect == "" || definition.Reversibility == "" {
			definition.Effect, definition.Reversibility = operationEffect(definition.ID, definition.Risk)
		}
		if isDestructiveEffect(definition.Effect) {
			definition.Risk = "destructive"
		}
	}
	attachGeneratedDestructiveGuards(definitions, nil)
	attachGeneratedExecutionGovernance(definitions)
	if err := validateGeneratedGovernance(definitions); err != nil {
		return err
	}
	records := make([]capabilityGovernanceRecord, 0, len(definitions))
	destructiveCount := 0
	seen := map[string]bool{}
	for _, definition := range definitions {
		if definition.ID == "" || seen[definition.ID] {
			return fmt.Errorf("duplicate governance capability %q", definition.ID)
		}
		seen[definition.ID] = true
		if definition.Risk == "destructive" {
			destructiveCount++
		}
		records = append(records, capabilityGovernanceRecord{
			ID: definition.ID, Backend: definition.Backend, Risk: definition.Risk,
			Effect: definition.Effect, Reversibility: definition.Reversibility,
			GuardProfile: definition.GuardProfile, Preflight: definition.Preflight,
			Reread: definition.Reread, Postcondition: definition.Postcondition,
			ConflictKey: definition.ConflictKey, RetryClass: definition.RetryClass,
			ExecutionClass: definition.ExecutionClass,
		})
	}
	if destructiveCount != 101 {
		return fmt.Errorf("governance contains %d destructive capabilities, expected 101", destructiveCount)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	payload, err := json.MarshalIndent(capabilityGovernanceManifest{SchemaVersion: 2, BridgeVersion: "2.0.0", Capabilities: records}, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	path := filepath.Join(repoRoot, "Core", "internal", "feishu", "contracts", "capability-governance-2.0.0.json")
	return os.WriteFile(path, payload, 0o644)
}

func attachApprovalDestructiveGuards(definitions []feishu.CapabilityDefinition) {
	for index := range definitions {
		definition := &definitions[index]
		if definition.ID != "approval.instances.cancel" && definition.ID != "approval.tasks.rollback" {
			continue
		}
		step := &feishu.CapabilityStep{
			ID:       "approval.instances.get",
			Map:      map[string]string{"instance-code": "$input.data.instance_code"},
			Defaults: map[string]any{},
		}
		definition.Preflight = step
		reread := *step
		definition.Reread = &reread
	}
}

func allowGeneratedCommand(domain, name string) bool {
	if forbiddenGeneratedName(name) {
		return false
	}
	normalizedName := strings.NewReplacer("_", ".", "-", ".").Replace(strings.TrimPrefix(strings.SplitN(name, "\t", 2)[0], "+"))
	switch domain {
	case "base":
		if strings.Contains(normalizedName, "advperm") || strings.Contains(normalizedName, "button.rule") || strings.Contains(normalizedName, "workflow") || strings.Contains(normalizedName, ".role") {
			return false
		}
	case "task":
		if strings.Contains(normalizedName, ".agent") || strings.HasPrefix(normalizedName, "agent") {
			return false
		}
	case "im":
		if strings.Contains(normalizedName, "moderation") || strings.Contains(normalizedName, "join.requests") || strings.Contains(normalizedName, "user.setting") {
			return false
		}
	case "drive":
		if strings.Contains(normalizedName, "secure.label") {
			return false
		}
	case "calendar":
		if strings.Contains(normalizedName, "transfer") {
			return false
		}
	}
	if domain == "contact" {
		return semanticReadName(name)
	}
	if domain != "vc" {
		return true
	}
	normalized := strings.TrimPrefix(strings.SplitN(name, "\t", 2)[0], "+")
	for _, allowed := range []string{"detail", "meeting-events", "meeting-list-active", "recording", "search", "vc.meeting.get"} {
		if normalized == allowed {
			return true
		}
	}
	return false
}

func addImmediateMailCapabilities(definitions *[]feishu.CapabilityDefinition, seen, base map[string]bool) {
	byID := map[string]feishu.CapabilityDefinition{}
	for _, definition := range *definitions {
		byID[definition.ID] = definition
	}
	for _, name := range []string{"send", "reply", "reply.all", "forward"} {
		baseID := "mail.shortcut." + name
		definition, ok := byID[baseID]
		if !ok {
			continue
		}
		definition.ID += ".immediate"
		definition.Risk = "high-impact-write"
		definition.Effect = "send"
		definition.Reversibility = "irreversible"
		definition.FixedArgs = append(append([]string{}, definition.FixedArgs...), "--confirm-send")
		appendGeneratedCapability(definitions, seen, base, definition)
	}
}

func appendGeneratedCapability(values *[]feishu.CapabilityDefinition, seen, base map[string]bool, definition feishu.CapabilityDefinition) {
	if definition.ID == "" || base[definition.ID] || seen[definition.ID] || forbiddenGeneratedName(definition.ID) {
		return
	}
	seen[definition.ID] = true
	*values = append(*values, definition)
}

func baseCapabilityDefinitions(path string) ([]feishu.CapabilityDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest capabilityAdditions
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return manifest.Capabilities, nil
}

func attachGeneratedDestructiveGuards(definitions, base []feishu.CapabilityDefinition) {
	all := map[string]feishu.CapabilityDefinition{}
	for _, definition := range append(append([]feishu.CapabilityDefinition{}, base...), definitions...) {
		all[definition.ID] = definition
	}
	for index := range definitions {
		definition := &definitions[index]
		if definition.Risk != "destructive" {
			continue
		}
		if definition.Transport == "typed" {
			parts := strings.Split(definition.ID, ".")
			if len(parts) >= 3 {
				resource := strings.Join(parts[:len(parts)-1], ".")
				if definition.Preflight == nil {
					for _, candidateID := range []string{resource + ".get", resource + ".detail"} {
						if candidate, ok := all[candidateID]; ok {
							if step, ready := generatedGuardStep(candidate, *definition); ready {
								definition.Preflight = &step
								break
							}
						}
					}
				}
				if definition.Reread == nil {
					for _, candidateID := range []string{resource + ".list", resource + ".search", resource + ".query"} {
						if candidate, ok := all[candidateID]; ok {
							if step, ready := generatedGuardStep(candidate, *definition); ready {
								definition.Reread = &step
								break
							}
						}
					}
				}
			}
		}
		if definition.Preflight == nil {
			definition.Preflight = &feishu.CapabilityStep{Kind: "input_summary", ID: "$local.input_summary", Map: map[string]string{}, Defaults: map[string]any{}}
		}
	}
}

func generatedGuardStep(read, write feishu.CapabilityDefinition) (feishu.CapabilityStep, bool) {
	if read.Risk != "read" {
		return feishu.CapabilityStep{}, false
	}
	mapping := map[string]string{}
	for name, field := range read.Flags {
		if _, ok := write.Flags[name]; ok {
			mapping[name] = name
		} else if field.Required {
			return feishu.CapabilityStep{}, false
		}
	}
	return feishu.CapabilityStep{ID: read.ID, Map: mapping, Defaults: map[string]any{}}, true
}

func discoverDomainCommands(binary, domain string) ([]string, error) {
	output, err := runCLI(binary, domain, "--help")
	if err != nil {
		return nil, err
	}
	commands := []string{}
	inCommands := false
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "Available Commands:" {
			inCommands = true
			continue
		}
		if !inCommands {
			continue
		}
		if strings.TrimSpace(line) == "Flags:" {
			break
		}
		match := helpCommandLine.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		name := strings.TrimSpace(match[1])
		if name == "help" || forbiddenGeneratedName(domain+"."+name) {
			continue
		}
		commands = append(commands, name+"\t"+strings.TrimSpace(match[2]))
	}
	return commands, scanner.Err()
}

func commandSchemaNames(domain, commandLine string) []string {
	parts := strings.SplitN(commandLine, "\t", 2)
	if len(parts) != 2 || strings.HasPrefix(parts[0], "+") {
		return nil
	}
	methods := strings.Split(parts[1], ",")
	result := make([]string, 0, len(methods))
	for _, method := range methods {
		method = strings.TrimSpace(method)
		if method == "" || strings.Contains(method, " ") {
			continue
		}
		result = append(result, domain+"."+parts[0]+"."+method)
	}
	return result
}

func typedCapability(binary, schemaName string) (feishu.CapabilityDefinition, error) {
	output, err := runCLI(binary, "schema", schemaName)
	if err != nil {
		return feishu.CapabilityDefinition{}, err
	}
	var schema cliSchema
	if err := json.Unmarshal(output, &schema); err != nil {
		return feishu.CapabilityDefinition{}, fmt.Errorf("decode lark-cli schema %s: %w", schemaName, err)
	}
	return capabilityFromCLISchema(schema)
}

func capabilityFromCLISchema(schema cliSchema) (feishu.CapabilityDefinition, error) {
	command := strings.Fields(schema.Name)
	if len(command) < 3 {
		return feishu.CapabilityDefinition{}, errors.New("invalid lark-cli schema command")
	}
	definition := baseGeneratedDefinition(strings.Join(command, "."), command, schema.Meta.Risk)
	definition.Transport = "typed"
	definition.RequiredScopes = uniqueSorted(append(append([]string{}, schema.Meta.Scopes...), schema.Meta.Required...))
	if containsText(schema.Meta.AccessTokens, "user") {
		definition.Identity = "user"
	} else {
		definition.Identity = "bot"
	}
	definition.Flags = map[string]feishu.CapabilityField{}
	required := map[string]bool{}
	for _, container := range []string{"params", "data"} {
		node, found := schema.InputSchema.Properties[container]
		if !found {
			continue
		}
		if node.Carrier != "" {
			name := strings.TrimPrefix(node.Carrier, "--")
			definition.Flags[name] = feishu.CapabilityField{Type: "json", Required: containsText(schema.InputSchema.Required, container), Private: true, MaxBytes: 2 * 1024 * 1024}
			continue
		}
		for _, name := range node.Required {
			required[name] = true
		}
		for name, property := range node.Properties {
			flag := strings.TrimPrefix(property.Flag, "--")
			if flag == "" {
				flag = strings.ReplaceAll(name, "_", "-")
			}
			field := capabilityField(flag, property)
			field.Required = required[name]
			definition.Flags[flag] = field
		}
	}
	definition.FlagOrder = sortedKeys(definition.Flags)
	definition.CLIConfirm = schema.Meta.Danger
	hardenApprovalDefinition(&definition)
	return definition, nil
}

func hardenApprovalDefinition(definition *feishu.CapabilityDefinition) {
	if definition.Domain != "approval" {
		return
	}
	for name, field := range definition.Flags {
		if name == "page-size" && field.Type == "integer" {
			field.Min, field.Max = 1, 100
		}
		if strings.Contains(name, "code") || strings.HasSuffix(name, "-id") || strings.HasSuffix(name, "-ids") || strings.Contains(name, "token") || name == "keyword" {
			field.Private = true
		}
		definition.Flags[name] = field
	}
}

func shortcutCapability(binary, domain, commandLine string) (feishu.CapabilityDefinition, error) {
	command := strings.SplitN(commandLine, "\t", 2)[0]
	output, err := runCLI(binary, domain, command, "--help")
	if err != nil {
		return feishu.CapabilityDefinition{}, err
	}
	risk := "read"
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Risk:") {
			risk = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Risk:"))
		}
	}
	id := domain + ".shortcut." + strings.ReplaceAll(strings.TrimPrefix(command, "+"), "-", ".")
	definition := baseGeneratedDefinition(id, []string{domain, command}, risk)
	definition.Transport = "shortcut"
	definition.Flags = map[string]feishu.CapabilityField{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		match := helpFlagLine.FindStringSubmatch(scanner.Text())
		if len(match) != 4 {
			continue
		}
		name, kind, description := match[1], match[2], match[3]
		if excludedShortcutFlag(name, kind) {
			if name == "yes" {
				definition.CLIConfirm = true
			}
			continue
		}
		field := helpCapabilityField(name, kind, description)
		definition.Flags[name] = field
	}
	if err := scanner.Err(); err != nil {
		return feishu.CapabilityDefinition{}, err
	}
	definition.FlagOrder = sortedKeys(definition.Flags)
	return definition, nil
}

func baseGeneratedDefinition(id string, command []string, risk string) feishu.CapabilityDefinition {
	if risk == "high-risk-write" {
		risk = "high-impact-write"
	}
	if semanticReadName(id) {
		risk = "read"
	}
	effect, reversibility := operationEffect(id, risk)
	if effect == "delete" || effect == "clear" || effect == "overwrite" || effect == "move" || effect == "revert" || effect == "cancel" {
		risk = "destructive"
	}
	return feishu.CapabilityDefinition{
		ID: id, Domain: command[0], Risk: risk, Identity: "user", Backend: "lark-cli", Effect: effect,
		Reversibility: reversibility, Queue: "actionbox", Command: command, FixedArgs: []string{},
		Flags: map[string]feishu.CapabilityField{}, FlagOrder: []string{}, Scope: feishu.CapabilityScope{Bounded: true, RequiredGroups: [][]string{}},
		Redaction: map[string]any{"identifiers": true, "body": true}, ResultIdentifiers: []json.RawMessage{},
	}
}

func capabilityField(name string, node schemaNode) feishu.CapabilityField {
	field := feishu.CapabilityField{Type: "string", Max: 4000}
	switch node.Type {
	case "boolean":
		field.Type, field.Max = "boolean", 0
	case "integer", "number":
		field.Type, field.Min, field.Max = "integer", 0, 1000000
		if node.Minimum != nil {
			field.Min = int(*node.Minimum)
		}
		if node.Maximum != nil {
			field.Max = int(*node.Maximum)
		}
	case "array":
		if node.Items != nil && node.Items.Type == "string" {
			field.Type, field.Max, field.MaxItems = "csv", 0, 200
		} else {
			field.Type, field.Max, field.MaxBytes, field.Private = "json", 0, 2*1024*1024, true
		}
	case "object":
		field.Type, field.Max, field.MaxBytes, field.Private = "json", 0, 2*1024*1024, true
	case "string":
		if node.MaxLength != nil && *node.MaxLength > 0 {
			field.Max = *node.MaxLength
		}
	}
	if len(node.Enum) > 0 {
		field.Type = "enum"
		for _, value := range node.Enum {
			field.Values = append(field.Values, fmt.Sprint(value))
		}
	}
	lower := strings.ToLower(name + " " + node.Description)
	if strings.Contains(lower, "file path") || strings.Contains(lower, "文件路径") || strings.HasSuffix(name, "-file") || strings.HasSuffix(name, "-path") {
		field.Type, field.Path, field.Private, field.Max = "path", true, true, 0
	}
	if strings.Contains(lower, "content") || strings.Contains(lower, "正文") || strings.Contains(lower, "recipient") || strings.Contains(lower, "收件") {
		field.Private = true
	}
	return field
}

func helpCapabilityField(name, kind, description string) feishu.CapabilityField {
	node := schemaNode{Type: "string", Description: description}
	switch strings.ToLower(kind) {
	case "", "bool":
		node.Type = "boolean"
	case "int", "int32", "int64":
		node.Type = "integer"
	case "stringarray", "strings":
		node.Type = "array"
		node.Items = &schemaNode{Type: "string"}
	}
	field := capabilityField(name, node)
	if name == "data" || name == "json" || strings.HasSuffix(name, "-json") {
		field.Type, field.Private, field.Max, field.MaxBytes = "json", true, 0, 2*1024*1024
	}
	field.Required = strings.Contains(strings.ToLower(description), "required") || strings.Contains(description, "必填")
	return field
}

func operationEffect(id, risk string) (string, string) {
	lower := strings.ToLower(id)
	tokens := operationTokens(lower)
	switch {
	case semanticReadName(lower):
		return "read", "none"
	case tokens["cancel"]:
		return "cancel", "irreversible"
	case tokens["clear"]:
		return "clear", "hard-delete"
	case tokens["revert"] || tokens["rollback"]:
		return "revert", "potentially-reversible"
	case tokens["replace"] || tokens["overwrite"]:
		return "overwrite", "potentially-reversible"
	case tokens["move"]:
		return "move", "potentially-reversible"
	case tokens["trash"]:
		return "delete", "soft-delete"
	case tokens["delete"] || tokens["remove"] || tokens["revoke"] || tokens["recall"]:
		return "delete", "hard-delete"
	case tokens["subscribe"] || tokens["subscription"] || tokens["unsubscribe"]:
		return "update", "reversible"
	case tokens["send"] || tokens["reply"] || tokens["forward"] || tokens["share"]:
		return "send", "irreversible"
	case tokens["create"] || tokens["add"] || tokens["upload"]:
		return "create", "reversible"
	case risk == "read":
		return "read", "none"
	case risk == "remote-operation":
		return "remote", "unknown"
	default:
		return "update", "unknown"
	}
}

func operationTokens(value string) map[string]bool {
	result := map[string]bool{}
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return r == '.' || r == '-' || r == '_' || r == '+' || r == '/' || r == ' '
	}) {
		result[token] = true
	}
	return result
}

func isDestructiveEffect(effect string) bool {
	switch effect {
	case "delete", "clear", "overwrite", "move", "revert", "cancel":
		return true
	default:
		return false
	}
}

func attachGeneratedExecutionGovernance(definitions []feishu.CapabilityDefinition) {
	for index := range definitions {
		definition := &definitions[index]
		definition.RetryClass = "idempotent"
		if definition.Risk != "read" {
			definition.RetryClass = "never"
		}
		definition.ExecutionClass = "standard"
		if definition.Risk == "remote-operation" {
			definition.ExecutionClass = "long-remote"
		}
		definition.ConflictKey = generatedConflictFields(*definition)
		definition.GuardProfile = feishu.CapabilityGuardNone
		if definition.Risk != "destructive" {
			continue
		}
		definition.GuardProfile = feishu.CapabilityGuardBounded
		kind := "manual_review_only"
		if definition.Reread != nil {
			kind = "observed_not_proven"
		}
		if definition.Poll != nil {
			definition.GuardProfile = feishu.CapabilityGuardStrong
			kind = "remote_terminal"
		}
		definition.Postcondition = &feishu.CapabilityPostcondition{Kind: kind}
	}
}

func generatedConflictFields(definition feishu.CapabilityDefinition) []string {
	preferred := []string{"target-id", "target-value", "message-id", "chat-id", "user-id", "event-id", "task-id", "file-token", "folder-token", "document-id", "spreadsheet-token", "base-token", "table-id", "record-id", "meeting-id", "minute-token", "note-id", "presentation-id", "mailbox-id", "thread-id", "draft-id", "objective-id", "key-result-id", "instance-code"}
	result := []string{}
	for _, name := range preferred {
		if _, ok := definition.Flags[name]; ok {
			result = append(result, name)
			if len(result) == 3 {
				break
			}
		}
	}
	if len(result) == 0 {
		return []string{"$capability"}
	}
	return result
}

func validateGeneratedGovernance(definitions []feishu.CapabilityDefinition) error {
	for _, definition := range definitions {
		if definition.Effect == "" || definition.Reversibility == "" || definition.RetryClass == "" || definition.ExecutionClass == "" || len(definition.ConflictKey) == 0 {
			return fmt.Errorf("generated capability %s is missing governance metadata", definition.ID)
		}
		if definition.Risk == "destructive" && (definition.GuardProfile == feishu.CapabilityGuardNone || definition.Preflight == nil || definition.Preflight.ID == "" || definition.Postcondition == nil || definition.Postcondition.Kind == "" || definition.RetryClass != "never") {
			return fmt.Errorf("generated destructive capability %s is not governed", definition.ID)
		}
	}
	return nil
}

func destructiveName(value string) bool {
	// Discovery also inspects the CLI help description so legacy shortcut
	// coverage remains stable. Final risk/effect metadata is never derived from
	// this broad scan; operationEffect classifies the exact capability ID.
	lower := strings.ToLower(value)
	for _, token := range []string{"delete", "remove", "trash", "clear", "revoke", "recall", "cancel", "rollback", "replace", "overwrite", "move", "revert"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func forbiddenGeneratedName(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "user_mailbox.event") || strings.HasSuffix(lower, ".shortcut.watch") {
		return true
	}
	for _, token := range forbiddenResourceTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return strings.Contains(lower, "urgent_phone") || strings.Contains(lower, "urgent_sms") || strings.Contains(lower, "meeting.join") || strings.Contains(lower, "meeting.end")
}

func semanticReadName(value string) bool {
	parts := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r == '.' || r == '-' || r == '_' || r == '+' })
	if len(parts) == 0 {
		return false
	}
	last := parts[len(parts)-1]
	for _, token := range []string{"get", "list", "query", "search", "detail", "transcript", "status", "profile", "inspect", "lint", "categories"} {
		if last == token {
			return true
		}
	}
	return strings.Contains(strings.ToLower(value), "get_recall_detail") || strings.Contains(strings.ToLower(value), "history.list")
}

func excludedShortcutFlag(name, kind string) bool {
	if name == "json" && strings.TrimSpace(kind) != "" {
		return false
	}
	for _, value := range []string{"as", "dry-run", "format", "help", "jq", "json", "yes", "confirm-send", "no-validate", "force", "params"} {
		if name == value {
			return true
		}
	}
	return strings.Contains(name, "permission") || strings.Contains(name, "member") || strings.Contains(name, "role")
}

func runCLI(binary string, arguments ...string) ([]byte, error) {
	command := exec.Command(binary, arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("lark-cli %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func sortedKeys(values map[string]feishu.CapabilityField) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func uniqueSorted(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func containsText(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func parseInteger(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
