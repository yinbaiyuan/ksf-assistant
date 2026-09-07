package service

import (
	"strings"
	"time"
)

type ConfigurationDiagnostics struct {
	RecentOperations         []ConfigurationReceipt `json:"recentOperations,omitempty"`
	ServiceVersion           string                 `json:"serviceVersion"`
	CLIVersion               string                 `json:"cliVersion"`
	CLIState                 string                 `json:"cliState"`
	PermissionRevision       string                 `json:"permissionRevision"`
	MissingUserScopes        []string               `json:"missingUserScopes"`
	MissingApplicationScopes []string               `json:"missingApplicationScopes"`
	SelfTarget               string                 `json:"selfTarget,omitempty"`
}

func (state *configurationRuntime) convergePresentation(result *ConfigurationSnapshot, now time.Time) {
	e := state.data.evidence
	result.Diagnostics = ConfigurationDiagnostics{ServiceVersion: e.ServiceVersion, CLIVersion: e.CLIVersion, CLIState: e.CLIState, PermissionRevision: e.PermissionRevision, MissingUserScopes: e.MissingUserScopes, MissingApplicationScopes: e.MissingApplicationScopes, SelfTarget: e.OperatorAlias}
	fact := func(id string) ConfigurationFact {
		for _, f := range result.Facts {
			if f.ID == id {
				return f
			}
		}
		return ConfigurationFact{ID: id, State: "unknown", Value: "待检查", Stale: true}
	}
	robot := fact("application")
	robot.ID = "robot"
	robot.Title = "飞书机器人"
	robot.Value = e.BotName
	if e.ApplicationState != "present" {
		robot.Value = ""
	}
	if robot.Value == "" {
		robot.Value = "名称暂不可用"
	}
	user := fact("user")
	user.ID = "authorizedUser"
	user.Title = "授权用户"
	if user.State == "present" {
		switch {
		case e.UserPermissions == "missing" || e.ApplicationPermissions == "missing":
			user.Value = "授权未完成"
		case e.UserPermissions != "present" || e.ApplicationPermissions != "present":
			user.Value = "权限待核验"
		default:
			user.Value = "已登录"
		}
		if e.UserName != "" {
			user.Value = e.UserName + " · " + user.Value
		}
	}
	connection := ConfigurationFact{ID: "taskConnection", Title: "任务连接", State: "present", Value: "正常", Source: "Core 接入条件汇总", CheckedAt: now.UTC().Format(time.RFC3339Nano)}
	var problems []string
	for _, id := range []string{"connection", "desktop", "operator", "bot"} {
		f := fact(id)
		if f.State != "present" {
			connection.State = "missing"
			problems = append(problems, f.Title+"："+f.Value)
		}
	}
	if len(problems) > 0 {
		connection.Value = "需要处理"
		if !state.data.quickAt.IsZero() && state.data.connection.ProcessState != "starting" && state.data.connection.ProcessState != "reconnecting" {
			result.Issues = append(result.Issues, ConfigurationIssue{"connection", "task_connection_unavailable", strings.Join(problems, "；")})
		}
	}
	if state.data.quickAt.IsZero() || state.data.connection.ProcessState == "starting" || state.data.connection.ProcessState == "reconnecting" {
		connection.State = "unknown"
		connection.Value = "连接中"
	}
	if user.State == "missing" {
		robot.State, robot.Value = "missing", "登录后显示"
		connection.State, connection.Value = "missing", "等待飞书登录"
		result.Summary = ConfigurationSummary{State: "authorization_required", Title: "等待飞书登录", Detail: "登录后恢复消息和任务卡片功能。", Tone: "neutral"}
		issues := result.Issues[:0]
		for _, issue := range result.Issues {
			if issue.Code != "task_connection_unavailable" {
				issues = append(issues, issue)
			}
		}
		result.Issues = issues
	}
	result.Facts = append(result.Facts, robot, user, connection)
	if result.Flow != nil && result.Flow.Kind == "user" && result.Flow.State == "completed" && e.Auth != nil && e.Auth.IdentityValid {
		result.Flow = nil
	}
	if len(e.MissingApplicationScopes) > 0 {
		result.Issues = append(result.Issues, ConfigurationIssue{"application", "application_permissions_missing", "应用尚未开通所需权限，请在飞书开放平台完善应用权限后刷新。"})
	} else if len(e.MissingUserScopes) > 0 {
		result.Issues = append(result.Issues, ConfigurationIssue{"authorization", "user_permissions_missing", "当前用户授权未覆盖本版本所需权限，请重新授权。"})
	}
	if e.CLIState != "" && e.CLIState != "ready" {
		result.Issues = append(result.Issues, ConfigurationIssue{"runtime", "cli_unavailable", "lark-cli 不可用或版本不兼容，请修复运行组件。"})
	}
	actions := result.Actions[:0]
	for _, a := range result.Actions {
		if a.ID == "set_feature" || a.ID == "enable_outbound" {
			continue
		}
		if a.ID == "start_auth" {
			a.Enabled = a.Enabled && e.ApplicationPermissions == "present" && (fact("user").State == "missing" || e.UserPermissions == "missing")
			a.Title = "登录飞书"
		}
		if a.ID == "restart" {
			a.Enabled = a.Enabled && (fact("connection").State == "missing")
		}
		if a.ID == "test_message" {
			a.Title = "向我发送测试消息"
			a.Enabled = a.Enabled && e.BotState == "present" && e.OperatorState == "present" && e.OperatorAlias != "" && !state.acting && !state.closed && !state.data.invalidated
		}
		actions = append(actions, a)
	}
	result.Actions = actions
}
