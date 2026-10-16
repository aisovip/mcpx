package server

import (
	"context"
	"testing"

	"mcpx/internal/mcpresult"
	"mcpx/internal/remotesession"
)

func operationTestSession(t *testing.T, rt *Runtime, workspace string) remotesession.Session {
	t.Helper()
	principal, err := rt.principalFromContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	registered, ok := rt.reg.Get(workspace)
	if !ok {
		t.Fatalf("workspace %q was not registered", workspace)
	}
	created, err := rt.remote.Create(context.Background(), principal, remotesession.CreateInput{
		WorkspaceName: workspace, WorkspacePath: registered.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	return created.Session
}

func callOperationTool(t *testing.T, rt *Runtime, name string, arguments map[string]any) map[string]any {
	t.Helper()
	result, err := rt.toolHandlers[name](context.Background(), mcpresult.Request(arguments))
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatal("tool result has no model-visible structured content")
	}
	return payload
}
