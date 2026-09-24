package computeruse

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveComputerUseListApps(t *testing.T) {
	if os.Getenv("MCPX_COMPUTER_USE_LIVE_TEST") != "1" {
		t.Skip("set MCPX_COMPUTER_USE_LIVE_TEST=1 to use the installed Open Computer Use runtime")
	}
	service := NewService()
	t.Cleanup(func() { _ = service.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "mcpx-live-test",
		Action:          "js",
		Code:            `const apps = await cua.listApps({ emit: false }); nodeRepl.write(JSON.stringify(apps));`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError {
		t.Fatalf("live result = %+v", result)
	}
	if text := resultText(result); !strings.Contains(text, "[") && !strings.Contains(text, "{") {
		t.Fatalf("unexpected list_apps output: %q", text)
	}
}
