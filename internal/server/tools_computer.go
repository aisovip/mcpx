package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcpx/internal/audit"
	"mcpx/internal/auth"
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
	envReq, principal, session, fail := r.changeRequest(ctx, req, true)
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
	r.registerComputerImages(ctx, principal, session.ID, result)
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

// registerComputerImages turns every image content part of a computer result into a durable
// artifact so the model can re-read it through the mcpx resource URI after the call returns.
func (r *Runtime) registerComputerImages(ctx context.Context, principal auth.Principal, sessionID string, result *mcp.CallToolResult) {
	if r == nil || r.artifacts == nil || result == nil {
		return
	}
	var lines []string
	for _, item := range result.Content {
		image, ok := item.(*mcp.ImageContent)
		if !ok || len(image.Data) == 0 {
			continue
		}
		mimeType := image.MIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		registered, err := r.artifacts.RegisterBytes(
			ctx, sessionID, principal.ID, image.Data,
			"computer-screenshot."+imageExtension(mimeType), "screenshot", mimeType,
		)
		if err != nil {
			continue
		}
		_ = r.remote.AddEvent(ctx, principal, remotesession.Event{
			RemoteSessionID: sessionID,
			Type:            "artifact.registered",
			OperationID:     registered.ID,
			Summary:         registered.Name,
			ResourceURI:     registered.ResourceURI,
		})
		encoded, err := json.Marshal(computerImageReference{
			ArtifactID:  registered.ID,
			ResourceURI: registered.ResourceURI,
			MIMEType:    mimeType,
			Bytes:       len(image.Data),
		})
		if err != nil {
			continue
		}
		lines = append(lines, "artifact: "+string(encoded))
	}
	if len(lines) > 0 {
		result.Content = append(result.Content, &mcp.TextContent{Text: strings.Join(lines, "\n")})
	}
}

type computerImageReference struct {
	ArtifactID  string `json:"artifact_id"`
	ResourceURI string `json:"resource_uri"`
	MIMEType    string `json:"mime_type"`
	Bytes       int    `json:"bytes"`
}

func imageExtension(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/png":
		return "png"
	default:
		return "bin"
	}
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
