package taskruntime

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"time"
)

func Run(ctx context.Context, arguments []string, input io.Reader, output io.Writer, options Options) int {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := execute(ctx, arguments, input, options)
	response.Protocol, response.Version = Protocol, 1
	response.OK = err == nil
	if err != nil {
		var failure *Error
		if !errors.As(err, &failure) {
			failure = &Error{Code: "io_error", Message: "task runtime could not complete the local operation"}
		}
		response = Response{Protocol: Protocol, Version: 1, Error: failure}
	}
	if err := json.NewEncoder(output).Encode(response); err != nil {
		return 1
	}
	if response.OK {
		return 0
	}
	return 1
}

func execute(ctx context.Context, arguments []string, input io.Reader, options Options) (Response, error) {
	var response Response
	if len(arguments) == 1 && arguments[0] == "--version" {
		response.SoftwareVersion = SoftwareVersion
		return response, nil
	}
	if len(arguments) == 0 {
		return response, fail("invalid_request", "usage: ksf-assistant-task report|get|list|doctor --root PATH --host codex")
	}
	operation := arguments[0]
	switch operation {
	case "report", "get", "list", "doctor":
	default:
		return response, fail("invalid_request", "unsupported task operation")
	}
	flags := flag.NewFlagSet("ksf-assistant-task", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "explicit KSF workspace root")
	host := flags.String("host", "codex", "reporting host")
	if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *host != "codex" {
		return response, fail("invalid_request", "only --root PATH and --host codex are supported; identity is never accepted in argv")
	}
	data, err := io.ReadAll(io.LimitReader(input, MaxRequestBytes+1))
	if err != nil {
		return response, fail("invalid_request", "cannot read private JSON input")
	}
	if len(data) > MaxRequestBytes {
		return response, fail("limit_exceeded", "stdin JSON exceeds 256 KiB")
	}
	var request Request
	if err := strictDecode(data, &request); err != nil {
		return response, err
	}
	if request.Protocol != Protocol || request.Version != 1 {
		return response, fail("unsupported_protocol", "expected ksfassistant-task-runtime-v1 version 1")
	}
	if request.Host != "" && request.Host != *host {
		return response, fail("invalid_request", "stdin host does not match --host")
	}
	request.Host = *host
	if operation != "report" && (request.State != nil || request.ExpectedRevision != nil || request.EventID != "") {
		return response, fail("invalid_request", "report fields are not accepted by this command")
	}
	if operation != "list" && (request.Limit != 0 || request.After != "") {
		return response, fail("invalid_request", "pagination is only accepted by list")
	}
	if (operation == "list" || operation == "doctor") && (request.ThreadID != "" || request.TaskID != "") {
		return response, fail("invalid_request", "list and doctor do not accept identity")
	}
	if operation == "report" && request.TaskID != "" {
		return response, fail("invalid_request", "report requires private host identity, not task_id")
	}
	if request.ThreadID != "" && request.TaskID != "" {
		return response, fail("invalid_identity", "provide private identity or public task_id, not both")
	}
	if (operation == "report" || operation == "get") && request.TaskID == "" {
		environmentIdentity := os.Getenv("CODEX_THREAD_ID")
		if request.ThreadID != "" && environmentIdentity != "" && request.ThreadID != environmentIdentity {
			return response, fail("invalid_identity", "stdin and environment identities do not match")
		}
		if request.ThreadID == "" {
			request.ThreadID = environmentIdentity
		}
		if !validIdentity(request.Host, request.ThreadID) {
			return response, fail("invalid_identity", "CODEX_THREAD_ID or private stdin thread_id is required")
		}
	}
	if strings.TrimSpace(*root) == "" {
		return response, fail("invalid_root", "--root PATH is required; no implicit root selection")
	}
	store, err := Open(*root, options)
	if err != nil {
		return response, err
	}
	switch operation {
	case "report":
		view, history, replayed, err := store.Report(ctx, request)
		if err != nil {
			return response, err
		}
		response.Task, response.History, response.Replayed = &view, history, replayed
	case "get":
		identity := request.TaskID
		if identity == "" {
			identity, err = store.TaskID(ctx, request.Host, request.ThreadID)
			if err != nil {
				var failure *Error
				if errors.As(err, &failure) && failure.Code == "key_unavailable" {
					doctor := store.Doctor(ctx)
					if doctor.Store == "missing" && doctor.Key == "missing" && doctor.Records == 0 && len(doctor.Diagnostics) == 0 {
						return response, fail("not_found", "no task store or identity key exists; expected_revision 0 may initialize a new task")
					}
				}
				return response, err
			}
		}
		view, history, err := store.Get(ctx, identity)
		if err != nil {
			return response, err
		}
		response.Task, response.History = &view, history
	case "list":
		response.Tasks, response.NextAfter, err = store.List(ctx, request.Limit, request.After)
		if err != nil {
			return response, err
		}
	case "doctor":
		doctor := store.Doctor(ctx)
		response.Doctor = &doctor
	}
	return response, nil
}
