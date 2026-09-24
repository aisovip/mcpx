package server

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcpx/internal/audit"
	"mcpx/internal/computeruse"
	"mcpx/internal/remotesession"
)

type computerRunner interface {
	Execute(context.Context, computeruse.Request) (*mcp.CallToolResult, error)
	Close() error
}

type computerCallRequest struct {
	Action    string
	Code      string
	Title     string
	TimeoutMS int
}

func (r *Runtime) ensureComputerService() computerRunner {
	r.computerMu.Lock()
	defer r.computerMu.Unlock()
	if r.computerService == nil {
		r.computerService = computeruse.NewService()
	}
	return r.computerService
}

func (r *Runtime) toolComputer(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	envReq, _, session, fail := r.changeRequest(ctx, req, true)
	if fail != nil {
		return fail, nil
	}
	if session.Role != "owner" && session.Role != "editor" {
		return r.remoteError(envReq, session.ID, session.WorkspaceName, remotesession.ErrForbidden)
	}
	if err := validatePurpose(envReq.Intent); err != nil {
		return r.terminalError(envReq, session.ID, session.WorkspaceName, "PURPOSE_REQUIRED", err.Error())
	}
	call, err := parseComputerCall(envReq.Payload)
	if err != nil {
		return r.terminalError(envReq, session.ID, session.WorkspaceName, "bad_request", err.Error())
	}

	result, err := r.ensureComputerService().Execute(ctx, computeruse.Request{
		RemoteSessionID: session.ID,
		Action:          call.Action,
		Code:            call.Code,
		Title:           call.Title,
		TimeoutMS:       call.TimeoutMS,
		Meta:            mcpCallRequestMeta(envReq, session.ID, session.WorkspaceName),
	})
	if err != nil {
		code := computeruse.ErrorCode(err)
		if code == "" {
			code = computeruse.CodeCallFailed
		}
		r.logAudit(audit.Event{
			RequestID: envReq.RequestID, RemoteSessionID: session.ID, Workspace: session.WorkspaceName,
			Tool: "computer", Status: "error", Detail: map[string]any{"action": call.Action, "code": code},
		})
		return r.terminalError(envReq, session.ID, session.WorkspaceName, code, err.Error())
	}
	if result == nil {
		return r.terminalError(envReq, session.ID, session.WorkspaceName, computeruse.CodeCallFailed, "computer service returned no result")
	}
	augmentMCPCallResult(result, envReq, session.ID, session.WorkspaceName, "computer", call.Action)
	detail := map[string]any{"action": call.Action}
	if call.Title != "" {
		detail["title"] = call.Title
	}
	if call.TimeoutMS > 0 {
		detail["timeout_ms"] = call.TimeoutMS
	}
	status := "ok"
	if result.IsError {
		status = "error"
	}
	r.logAudit(audit.Event{
		RequestID: envReq.RequestID, RemoteSessionID: session.ID, Workspace: session.WorkspaceName,
		Tool: "computer", Status: status, Detail: detail,
	})
	return result, nil
}

func parseComputerCall(payload map[string]any) (computerCallRequest, error) {
	call := computerCallRequest{
		Action: strings.TrimSpace(stringPayload(payload, "action")),
		Code:   stringPayload(payload, "code"),
		Title:  strings.TrimSpace(stringPayload(payload, "title")),
	}
	switch call.Action {
	case "js":
		if strings.TrimSpace(call.Code) == "" {
			return computerCallRequest{}, fmt.Errorf("computer js requires non-empty code")
		}
		if utf8.RuneCountInString(call.Title) > 80 {
			return computerCallRequest{}, fmt.Errorf("computer title must be at most 80 characters")
		}
		timeout, present, err := optionalComputerTimeout(payload)
		if err != nil {
			return computerCallRequest{}, err
		}
		if present && (timeout < 1 || timeout > 300000) {
			return computerCallRequest{}, fmt.Errorf("computer timeout_ms must be between 1 and 300000")
		}
		call.TimeoutMS = timeout
	case "reset":
		if strings.TrimSpace(call.Code) != "" || call.Title != "" || payload["timeout_ms"] != nil {
			return computerCallRequest{}, fmt.Errorf("computer reset does not accept code, title, or timeout_ms")
		}
	default:
		return computerCallRequest{}, fmt.Errorf("unsupported computer action %q", call.Action)
	}
	return call, nil
}

func optionalComputerTimeout(payload map[string]any) (int, bool, error) {
	raw, ok := payload["timeout_ms"]
	if !ok || raw == nil {
		return 0, false, nil
	}
	switch value := raw.(type) {
	case float64:
		if value != float64(int(value)) {
			return 0, true, fmt.Errorf("computer timeout_ms must be an integer")
		}
		return int(value), true, nil
	case int:
		return value, true, nil
	case int64:
		return int(value), true, nil
	default:
		return 0, true, fmt.Errorf("computer timeout_ms must be an integer")
	}
}
