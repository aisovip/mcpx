package computeruse

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServicePersistsJSSessionUntilReset(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available")
	}
	native := fakeNativeMCP(t)
	t.Setenv("MCPX_COMPUTER_USE_NODE", node)
	t.Setenv("MCPX_COMPUTER_USE_BINARY", native)

	service := NewService()
	t.Cleanup(func() { _ = service.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	first, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `let marker = 41; await cua.getState({ emit: false }); nodeRepl.write("marker=" + marker);`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultText(first); !strings.Contains(got, "marker=41") {
		t.Fatalf("first result = %q", got)
	}

	second, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `nodeRepl.write("marker=" + marker);`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultText(second); !strings.Contains(got, "marker=41") {
		t.Fatalf("persistent result = %q", got)
	}

	if _, err := service.Execute(ctx, Request{RemoteSessionID: "session-1", Action: "reset"}); err != nil {
		t.Fatal(err)
	}
	third, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `nodeRepl.write(typeof marker);`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultText(third); !strings.Contains(got, "undefined") {
		t.Fatalf("post-reset result = %q", got)
	}
}

func TestServiceReturnsRuntimeUnavailable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPX_COMPUTER_USE_NODE", "")
	t.Setenv("MCPX_COMPUTER_USE_BINARY", "")
	t.Setenv("PATH", dir)

	service := NewService()
	defer service.Close()
	_, err := service.Execute(context.Background(), Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `nodeRepl.write("never");`,
	})
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != CodeRuntimeUnavailable {
		t.Fatalf("error = %T %v, want runtime unavailable", err, err)
	}
}

func TestServicePreservesImageContent(t *testing.T) {
	service, ctx := newFakeService(t)
	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); await app.getScreenshot();`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if got := resultText(result); !strings.Contains(got, "Fake App window") {
		t.Fatalf("text result = %q", got)
	}
	var foundImage bool
	for _, item := range result.Content {
		if image, ok := item.(*mcp.ImageContent); ok && image.MIMEType == "image/png" && len(image.Data) > 0 {
			foundImage = true
		}
	}
	if !foundImage {
		t.Fatalf("image content missing: %+v", result.Content)
	}
}

func TestServiceParsesHumanReadableListApps(t *testing.T) {
	service, ctx := newFakeService(t)
	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const apps = await cua.listApps({ emit: false }); nodeRepl.write(JSON.stringify(apps));`,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(result)
	for _, fragment := range []string{`"id":"fake.app"`, `"displayName":"Fake App"`, `"isRunning":true`, `"useCount":3`} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("listApps output missing %s: %s", fragment, text)
		}
	}
}

func TestServiceRecoversAfterJSError(t *testing.T) {
	service, ctx := newFakeService(t)
	failed, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `throw new Error("boom");`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed == nil || !failed.IsError || !strings.Contains(resultText(failed), "boom") {
		t.Fatalf("error result = %+v", failed)
	}
	recovered, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `nodeRepl.write("recovered");`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultText(recovered); !strings.Contains(got, "recovered") {
		t.Fatalf("recovery result = %q", got)
	}
}

func TestServiceTimeoutResetsJavaScriptWorker(t *testing.T) {
	service, ctx := newFakeService(t)
	timedOut, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `while (true) {}`,
		TimeoutMS:       100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if timedOut == nil || !timedOut.IsError || !strings.Contains(resultText(timedOut), "timed out") {
		t.Fatalf("timeout result = %+v", timedOut)
	}
	recovered, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `nodeRepl.write("after-timeout");`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultText(recovered); !strings.Contains(got, "after-timeout") {
		t.Fatalf("post-timeout result = %q", got)
	}
}

func TestServiceRestartsAfterNativeProcessExit(t *testing.T) {
	service, ctx := newFakeService(t)
	t.Setenv("MCPX_FAKE_NATIVE_CRASH_ON_LIST_APPS", "1")

	_, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `await cua.listApps({ emit: false });`,
	})
	if err == nil {
		t.Fatal("expected native process exit to fail the computer call")
	}
	if code := ErrorCode(err); code != CodeCallFailed {
		t.Fatalf("error code = %q, want %q: %v", code, CodeCallFailed, err)
	}

	t.Setenv("MCPX_FAKE_NATIVE_CRASH_ON_LIST_APPS", "0")
	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const apps = await cua.listApps({ emit: false }); nodeRepl.write(String(apps.length));`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError || !strings.Contains(resultText(result), "2") {
		t.Fatalf("post-restart result = %+v", result)
	}
}

func TestServiceIsolatesRemoteSessions(t *testing.T) {
	service, ctx := newFakeService(t)
	for _, item := range []struct {
		session string
		value   string
	}{{"session-a", "A"}, {"session-b", "B"}} {
		if _, err := service.Execute(ctx, Request{
			RemoteSessionID: item.session,
			Action:          "js",
			Code:            `var marker = "` + item.value + `";`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		session string
		want    string
	}{{"session-a", "A"}, {"session-b", "B"}} {
		result, err := service.Execute(ctx, Request{
			RemoteSessionID: item.session,
			Action:          "js",
			Code:            `nodeRepl.write(marker);`,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := resultText(result); !strings.Contains(got, item.want) {
			t.Fatalf("%s marker = %q, want %s", item.session, got, item.want)
		}
	}
}

func TestServiceSerializesConcurrentCalls(t *testing.T) {
	service, ctx := newFakeService(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, value := range []string{"first", "second"} {
		value := value
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := service.Execute(ctx, Request{
				RemoteSessionID: "session-1",
				Action:          "js",
				Code:            `await new Promise(resolve => setTimeout(resolve, 50)); nodeRepl.write("` + value + `");`,
			})
			if err == nil && (result == nil || result.IsError) {
				err = errors.New("concurrent call failed")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func newFakeService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available")
	}
	native := fakeNativeMCP(t)
	t.Setenv("MCPX_COMPUTER_USE_NODE", node)
	t.Setenv("MCPX_COMPUTER_USE_BINARY", native)
	service := NewService()
	t.Cleanup(func() { _ = service.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return service, ctx
}

func resultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var values []string
	for _, item := range result.Content {
		if text, ok := item.(*mcp.TextContent); ok {
			values = append(values, text.Text)
		}
	}
	return strings.Join(values, "\n")
}

func fakeNativeMCP(t *testing.T) string {
	t.Helper()
	t.Setenv("MCPX_FAKE_NATIVE_MCP", "1")
	return os.Args[0]
}
