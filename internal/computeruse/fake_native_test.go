package computeruse

import (
	"context"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	if os.Getenv("MCPX_FAKE_NATIVE_MCP") == "1" {
		os.Exit(runFakeNativeMCP())
	}
	os.Exit(m.Run())
}

func runFakeNativeMCP() int {
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-open-computer-use", Version: "0.3.5"}, nil)
	server.AddTool(&mcp.Tool{Name: "list_apps", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if os.Getenv("MCPX_FAKE_NATIVE_CRASH_ON_LIST_APPS") == "1" {
			os.Exit(3)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Fake App — fake.app [running]\nOther App -- other.app [last-used=2026-01-01, uses=3]"}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "get_app_state", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "App=fake.app\nFake App window"},
			&mcp.ImageContent{Data: []byte{0x89, 0x50, 0x4e, 0x47}, MIMEType: "image/png"},
		}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "click", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "clicked"}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "type_text", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "typed"}}}, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		return 1
	}
	return 0
}
