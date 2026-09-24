package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcpx/internal/computeruse"
	"mcpx/internal/mcpresult"
	"mcpx/internal/remotesession"
)

func TestComputerToolCatalogContract(t *testing.T) {
	runtime := &Runtime{}
	protocol := mcp.NewServer(&mcp.Implementation{Name: "mcpx-test", Version: "0.1.0"}, nil)
	runtime.registerTools(protocol)

	tool, ok := runtime.listedToolMap()["computer"]
	if !ok {
		t.Fatal("computer tool is not registered")
	}
	if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
		t.Fatalf("computer annotations = %+v, want destructive hint", tool.Annotations)
	}
	if tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
		t.Fatalf("computer annotations = %+v, want open-world hint", tool.Annotations)
	}

	var schema map[string]any
	if err := json.Unmarshal(mcpresult.ToolSchemaJSON(tool), &schema); err != nil {
		t.Fatal(err)
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties["user_confirmed"] != nil {
		t.Fatalf("computer schema must not expose user_confirmed: %s", mcpresult.ToolSchemaJSON(tool))
	}
	action, _ := properties["action"].(map[string]any)
	values, _ := action["enum"].([]any)
	if len(values) != 2 || values[0] != "js" || values[1] != "reset" {
		t.Fatalf("computer actions = %#v, want js/reset", values)
	}
}

func TestParseComputerCallAcceptsJSTimeoutAndTitle(t *testing.T) {
	call, err := parseComputerCall(map[string]any{
		"action":     "js",
		"code":       `nodeRepl.write("ok")`,
		"title":      "Check UI",
		"timeout_ms": float64(1500),
	})
	if err != nil {
		t.Fatal(err)
	}
	if call.Action != "js" || call.Code == "" || call.Title != "Check UI" || call.TimeoutMS != 1500 {
		t.Fatalf("call = %+v", call)
	}
}

func TestParseComputerCallRejectsInvalidCombinations(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
	}{
		{name: "missing code", payload: map[string]any{"action": "js"}},
		{name: "title too long", payload: map[string]any{"action": "js", "code": "1", "title": strings.Repeat("x", 81)}},
		{name: "timeout zero", payload: map[string]any{"action": "js", "code": "1", "timeout_ms": float64(0)}},
		{name: "timeout too large", payload: map[string]any{"action": "js", "code": "1", "timeout_ms": float64(300001)}},
		{name: "reset code", payload: map[string]any{"action": "reset", "code": "1"}},
		{name: "reset timeout", payload: map[string]any{"action": "reset", "timeout_ms": float64(1)}},
		{name: "unknown action", payload: map[string]any{"action": "click"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseComputerCall(test.payload); err == nil {
				t.Fatalf("payload %+v was accepted", test.payload)
			}
		})
	}
}

func TestComputerHandlerPassesThroughWithoutConfirmation(t *testing.T) {
	runtime := newWorkspaceRuntime(t, "demo")
	principal, err := runtime.principalFromContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	registered, _ := runtime.reg.Get("demo")
	created, err := runtime.remote.Create(context.Background(), principal, remotesession.CreateInput{
		WorkspaceName: "demo", WorkspacePath: registered.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeComputerRunner{result: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "computer ok"}}}}
	runtime.computerService = fake

	result, err := runtime.toolComputer(context.Background(), mcpresult.Request(map[string]any{
		"remote_session_id": created.Session.ID,
		"action":            "js",
		"purpose":           "verify computer passthrough",
		"code":              `nodeRepl.write("ok")`,
		"title":             "Verify passthrough",
		"timeout_ms":        float64(1500),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if fake.request.RemoteSessionID != created.Session.ID || fake.request.Action != "js" || fake.request.TimeoutMS != 1500 {
		t.Fatalf("service request = %+v", fake.request)
	}
	if result.Meta["mcpx/server"] != "computer" || result.Meta["mcpx/tool"] != "js" {
		t.Fatalf("result meta = %+v", result.Meta)
	}
}

func TestComputerToolResultPreservesMixedContent(t *testing.T) {
	runtime := newWorkspaceRuntime(t, "demo")
	protocol := mcp.NewServer(&mcp.Implementation{Name: "mcpx-test", Version: "0.1.0"}, nil)
	runtime.registerTools(protocol)

	principal, err := runtime.principalFromContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	registered, _ := runtime.reg.Get("demo")
	created, err := runtime.remote.Create(context.Background(), principal, remotesession.CreateInput{
		WorkspaceName: "demo", WorkspacePath: registered.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.computerService = &fakeComputerRunner{result: &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: "first output"},
		&mcp.TextContent{Text: "second output"},
		&mcp.ImageContent{Data: []byte{0x89, 0x50, 0x4e, 0x47}, MIMEType: "image/png"},
	}}}

	result := callRawToolResult(t, runtime.toolHandlers["computer"], map[string]any{
		"remote_session_id": created.Session.ID,
		"action":            "js",
		"purpose":           "verify mixed content passthrough",
		"code":              `nodeRepl.write("first output"); nodeRepl.write("second output");`,
	})
	if len(result.Content) != 3 {
		t.Fatalf("content items = %d, want 3: %+v", len(result.Content), result.Content)
	}
	first, _ := result.Content[0].(*mcp.TextContent)
	second, _ := result.Content[1].(*mcp.TextContent)
	if first == nil || first.Text != "first output" || second == nil || second.Text != "second output" {
		t.Fatalf("text content was rewritten: %+v", result.Content)
	}
	image, _ := result.Content[2].(*mcp.ImageContent)
	if image == nil || image.MIMEType != "image/png" || len(image.Data) != 4 {
		t.Fatalf("image content was rewritten: %+v", result.Content)
	}
}

func TestComputerHandlerMapsRuntimeError(t *testing.T) {
	runtime := newWorkspaceRuntime(t, "demo")
	principal, err := runtime.principalFromContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	registered, _ := runtime.reg.Get("demo")
	created, err := runtime.remote.Create(context.Background(), principal, remotesession.CreateInput{
		WorkspaceName: "demo", WorkspacePath: registered.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.computerService = &fakeComputerRunner{err: &computeruse.RuntimeError{
		Code: computeruse.CodeRuntimeUnavailable, Message: "install open-computer-use",
	}}

	result, err := runtime.toolComputer(context.Background(), mcpresult.Request(map[string]any{
		"remote_session_id": created.Session.ID,
		"action":            "js",
		"purpose":           "verify runtime error mapping",
		"code":              `nodeRepl.write("ok")`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := errorCode(decodeToolResult(t, result)); got != strings.ToLower(computeruse.CodeRuntimeUnavailable) {
		t.Fatalf("error code = %q, want %q", got, strings.ToLower(computeruse.CodeRuntimeUnavailable))
	}
}

type fakeComputerRunner struct {
	request computeruse.Request
	result  *mcp.CallToolResult
	err     error
}

func (f *fakeComputerRunner) Execute(_ context.Context, request computeruse.Request) (*mcp.CallToolResult, error) {
	f.request = request
	return f.result, f.err
}

func (f *fakeComputerRunner) Close() error { return nil }
