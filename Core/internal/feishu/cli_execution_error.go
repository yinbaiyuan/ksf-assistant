package feishu

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type CLIExecutionError struct {
	Code       string         `json:"code"`
	ExitCode   int            `json:"exitCode"`
	Started    bool           `json:"started"`
	Outcome    string         `json:"outcome"`
	Structured map[string]any `json:"error,omitempty"`
	Cause      error          `json:"-"`
}

func (err *CLIExecutionError) Error() string { return err.Code }
func (err *CLIExecutionError) Unwrap() error { return err.Cause }

var cliErrorToken = regexp.MustCompile(`^[a-z][a-z0-9_]{0,79}$`)

const safeUserCommandErrors = `
user_command_batch_arguments_invalid user_command_batch_continue_on_error_restricted user_command_batch_input_invalid user_command_batch_input_restricted user_command_batch_input_type user_command_batch_limit_or_shape user_command_batch_nested_input_restricted user_command_batch_operation_restricted user_command_batch_sheet_move_target_required user_command_record_share_limit
user_command_api_override_refused user_command_array_invalid
user_command_batch_arguments_invalid user_command_batch_continue_on_error_restricted user_command_batch_input_invalid user_command_batch_input_restricted user_command_batch_input_type user_command_batch_limit_or_shape user_command_batch_nested_input_restricted user_command_batch_operation_restricted user_command_batch_sheet_move_target_required user_command_record_share_limit
user_command_artifact_changed user_command_artifact_conflict user_command_artifact_limit_exceeded user_command_artifact_missing user_command_artifact_not_regular user_command_artifact_parent_unsafe user_command_artifact_path_unsafe user_command_artifact_plan_changed user_command_artifact_plan_invalid user_command_artifact_plan_required user_command_artifact_publication_incomplete user_command_artifact_publish_failed user_command_artifact_symlink user_command_artifact_unplanned user_command_artifacts_already_attempted
user_command_body_required user_command_cannot_self_approve user_command_confirmation_required user_command_content_required user_command_document_format_restricted user_command_duplicate_flag user_command_duplicate_parameter user_command_dynamic_resource_unsupported user_command_enum_invalid user_command_execution_closed user_command_execution_override_refused user_command_explicit_identity_required user_command_explicit_page_limit_required
user_command_file_changed user_command_file_duplicate user_command_file_field_invalid user_command_file_input_unsupported user_command_file_invalid user_command_file_not_frozen user_command_file_unavailable user_command_file_unsafe user_command_flag_invalid user_command_flag_unsupported user_command_flag_value_required
user_command_idempotency_key_invalid user_command_identifier_invalid user_command_identity_changed user_command_identity_mismatch user_command_identity_required user_command_identity_unavailable user_command_identity_unsupported user_command_input_invalid user_command_input_not_frozen user_command_input_too_large user_command_integer_invalid
user_command_json_depth user_command_json_duplicate user_command_json_invalid user_command_json_trailing user_command_mail_content_required user_command_manifest_invalid user_command_mutually_exclusive user_command_number_invalid user_command_output_root_invalid user_command_pagination_unbounded user_command_pattern_required user_command_required_field_missing user_command_schema_invalid user_command_slide_limit user_command_slides_invalid user_command_spreadsheet_locator_required user_command_staging_failed user_command_stdin_conflict user_command_target_unresolved user_command_too_large user_command_unexpected_input user_command_unsupported user_command_version_mismatch user_command_video_cover_required user_command_workdir_unavailable
`

func safeUserCommandErrorCode(err error) string {
	for current := err; current != nil; current = errors.Unwrap(current) {
		code, _, _ := strings.Cut(current.Error(), ":")
		if contains(strings.Fields(safeUserCommandErrors), code) {
			return code
		}
	}
	return ""
}

func commandExecutionError(code string, exitCode int, started bool, cause error, outputs ...[]byte) *CLIExecutionError {
	result := &CLIExecutionError{Code: code, ExitCode: exitCode, Started: started, Outcome: "not_started", Cause: cause}
	if safeCode := safeUserCommandErrorCode(cause); safeCode != "" {
		result.Code = safeCode
	}
	if started {
		result.Outcome = "unknown"
	}
	for _, output := range outputs {
		var envelope struct {
			OK    *bool          `json:"ok"`
			Error map[string]any `json:"error"`
		}
		if json.Unmarshal(output, &envelope) != nil || envelope.OK == nil || *envelope.OK || envelope.Error == nil {
			continue
		}
		for _, key := range []string{"http_status", "status_code"} {
			if value, ok := envelope.Error[key].(float64); ok && value >= 100 && value <= 599 && value == float64(int(value)) {
				if result.Structured == nil {
					result.Structured = map[string]any{}
				}
				result.Structured["http_status"] = int(value)
				break
			}
		}
		category, _ := envelope.Error["type"].(string)
		subtype, _ := envelope.Error["subtype"].(string)
		if !contains([]string{"validation", "authentication", "authorization", "config", "network", "api", "policy", "internal", "confirmation"}, category) || !cliErrorToken.MatchString(subtype) {
			continue
		}
		if result.Structured == nil {
			result.Structured = map[string]any{}
		}
		result.Structured["type"], result.Structured["subtype"] = category, subtype
		if value, ok := envelope.Error["code"].(float64); ok && value == float64(int64(value)) {
			result.Structured["code"] = value
		}
		if value, ok := envelope.Error["retryable"].(bool); ok {
			result.Structured["retryable"] = value
		}
		result.Code = fmt.Sprintf("lark_cli_%s_%s", category, subtype)
		break
	}
	return result
}

func cliFailureResult(result map[string]any, err error) map[string]any {
	var failure *CLIExecutionError
	if !errors.As(err, &failure) {
		return result
	}
	result = cloneInput(result)
	result["ok"] = false
	result["structuredCLI"] = map[string]any{
		"code": failure.Code, "exitCode": failure.ExitCode, "started": failure.Started,
		"outcome": failure.Outcome, "error": failure.Structured,
	}
	return result
}
