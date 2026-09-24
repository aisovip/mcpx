package computeruse

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	server.AddTool(&mcp.Tool{Name: "type_text", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := appendToolLog(os.Getenv("MCPX_FAKE_KEY_LOG"), "type_text", req.Params.Arguments); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "typed"}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "press_key", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := appendToolLog(os.Getenv("MCPX_FAKE_KEY_LOG"), "press_key", req.Params.Arguments); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pressed"}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "drag", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if logPath := os.Getenv("MCPX_FAKE_DRAG_LOG"); logPath != "" {
			line := append(append([]byte(nil), req.Params.Arguments...), '\n')
			if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
				return nil, err
			}
			file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, err
			}
			_, writeErr := file.Write(line)
			closeErr := file.Close()
			if writeErr != nil {
				return nil, writeErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "dragged"}}}, nil
	})
	if os.Getenv("MCPX_FAKE_DRAG_PATH") == "1" {
		server.AddTool(&mcp.Tool{Name: "drag_path", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if err := appendToolLog(os.Getenv("MCPX_FAKE_DRAG_PATH_LOG"), "drag_path", req.Params.Arguments); err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "dragged path"}}}, nil
		})
	}
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		return 1
	}
	return 0
}

func appendToolLog(path, tool string, arguments json.RawMessage) error {
	if path == "" {
		return nil
	}
	line, err := json.Marshal(map[string]any{"tool": tool, "arguments": arguments})
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(line)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
