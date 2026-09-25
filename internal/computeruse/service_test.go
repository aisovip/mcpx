package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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
		Code:            `const app = await cua.getApp("Fake App"); await app.getAXState();`,
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

func TestServiceDragPathRequiresNativeDragPath(t *testing.T) {
	service, ctx := newFakeService(t)
	dragLog := t.TempDir() + "/drag.jsonl"
	t.Setenv("MCPX_FAKE_DRAG_LOG", dragLog)

	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); await app.dragPath({ points: [[10, 20], [30, 40], [50, 30]], durationMs: 600 });`,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(result)
	if result == nil || !result.IsError || !strings.Contains(text, "drag_path") || !strings.Contains(text, "MCPX") {
		t.Fatalf("missing native drag_path result = %+v text=%q", result, text)
	}
	if _, err := os.Stat(dragLog); !os.IsNotExist(err) {
		t.Fatalf("dragPath must not fall back to drag calls: %v", err)
	}
}

func TestServiceDragPathSamplesCurveFunction(t *testing.T) {
	service, ctx := newFakeService(t)
	dragPathLog := t.TempDir() + "/drag-path.jsonl"
	t.Setenv("MCPX_FAKE_DRAG_PATH", "1")
	t.Setenv("MCPX_FAKE_DRAG_PATH_LOG", dragPathLog)

	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); const drawn = await app.dragPath({ from: [0, 0], to: [40, 0], steps: 4, curve: (t, from, to) => [from[0] + (to[0] - from[0]) * t, Math.sin(Math.PI * t) * 10] }); nodeRepl.write(JSON.stringify(drawn));`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError || !strings.Contains(resultText(result), `"segments":4`) {
		t.Fatalf("dragPath curve result = %+v text=%q", result, resultText(result))
	}
	entries := readToolLog(t, dragPathLog)
	if len(entries) != 1 {
		t.Fatalf("drag_path calls = %d, want 1: %+v", len(entries), entries)
	}
	arguments, _ := entries[0]["arguments"].(map[string]any)
	points, _ := arguments["points"].([]any)
	if len(points) != 5 {
		t.Fatalf("drag_path points = %d, want 5: %+v", len(points), points)
	}
	first, _ := points[0].(map[string]any)
	last, _ := points[4].(map[string]any)
	if first["x"] != float64(0) || first["y"] != float64(0) || last["x"] != float64(40) || last["y"] != float64(0) {
		t.Fatalf("drag_path endpoints = %+v .. %+v", first, last)
	}
}

func TestServiceDragPathUsesNativeDragPathWhenAvailable(t *testing.T) {
	service, ctx := newFakeService(t)
	dragPathLog := t.TempDir() + "/drag-path.jsonl"
	dragLog := t.TempDir() + "/drag.jsonl"
	t.Setenv("MCPX_FAKE_DRAG_PATH", "1")
	t.Setenv("MCPX_FAKE_DRAG_PATH_LOG", dragPathLog)
	t.Setenv("MCPX_FAKE_DRAG_LOG", dragLog)

	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); const drawn = await app.dragPath({ points: [[0, 0], [10, 5], [20, 0]], durationMs: 900, mouseButton: "left", delivery: "app_post" }); nodeRepl.write(JSON.stringify(drawn));`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError || !strings.Contains(resultText(result), `"mode":"drag_path"`) {
		t.Fatalf("native drag_path result = %+v text=%q", result, resultText(result))
	}
	entries := readToolLog(t, dragPathLog)
	if len(entries) != 1 {
		t.Fatalf("drag_path calls = %d, want 1: %+v", len(entries), entries)
	}
	arguments, _ := entries[0]["arguments"].(map[string]any)
	points, _ := arguments["points"].([]any)
	if len(points) != 3 || arguments["duration_ms"] != float64(900) || arguments["mouse_button"] != "left" || arguments["delivery"] != "app_post" {
		t.Fatalf("drag_path arguments = %+v", arguments)
	}
	if _, err := os.Stat(dragLog); !os.IsNotExist(err) {
		t.Fatalf("native drag_path must not fall back to drag: %v", err)
	}
}

func TestServiceDragPathDefaultsTo256WithNativeDragPath(t *testing.T) {
	service, ctx := newFakeService(t)
	dragPathLog := t.TempDir() + "/drag-path.jsonl"
	t.Setenv("MCPX_FAKE_DRAG_PATH", "1")
	t.Setenv("MCPX_FAKE_DRAG_PATH_LOG", dragPathLog)

	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); const drawn = await app.dragPath({ from: [0, 0], to: [10, 0], curve: (t, from, to) => [from[0] + (to[0] - from[0]) * t, Math.sin(Math.PI * t) * 2] }); nodeRepl.write(JSON.stringify(drawn));`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError || !strings.Contains(resultText(result), `"segments":256`) {
		t.Fatalf("default dragPath result = %+v text=%q", result, resultText(result))
	}
	entries := readToolLog(t, dragPathLog)
	if len(entries) != 1 {
		t.Fatalf("drag_path calls = %d, want 1: %+v", len(entries), entries)
	}
	arguments, _ := entries[0]["arguments"].(map[string]any)
	points, _ := arguments["points"].([]any)
	if len(points) != 257 {
		t.Fatalf("default drag_path points = %d, want 257", len(points))
	}
}

func TestServiceDragPathRejectsInvalidCurvePoint(t *testing.T) {
	service, ctx := newFakeService(t)
	t.Setenv("MCPX_FAKE_DRAG_PATH", "1")
	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); await app.dragPath({ from: [0, 0], to: [10, 0], steps: 2, curve: () => [NaN, 0] });`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError || !strings.Contains(resultText(result), "finite number") {
		t.Fatalf("invalid curve result = %+v", result)
	}
}

func TestServiceReturnsCompletionValue(t *testing.T) {
	service, ctx := newFakeService(t)
	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `JSON.stringify({a: 1})`,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(result)
	if result == nil || result.IsError || strings.TrimSpace(text) != `{"a":1}` {
		t.Fatalf("completion value result = %+v text=%q", result, text)
	}
}

func TestServiceActionsReturnStructuredResults(t *testing.T) {
	service, ctx := newFakeService(t)
	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); await app.click(3);`,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(result)
	for _, fragment := range []string{`"ok":true`, `"action":"click"`, `"target":"element:3"`, `"native":"clicked"`} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("click result missing %s: %q", fragment, text)
		}
	}
}

func TestServiceGetAppDoesNotEmitState(t *testing.T) {
	service, ctx := newFakeService(t)
	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); nodeRepl.write("bound:" + typeof app.click);`,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(result)
	if result == nil || result.IsError || !strings.Contains(text, "bound:function") {
		t.Fatalf("binding result = %+v text=%q", result, text)
	}
	if strings.Contains(text, "App=fake.app") {
		t.Fatalf("getApp must not dump app state: %q", text)
	}
}

func TestServiceConsoleLogIsCaptured(t *testing.T) {
	service, ctx := newFakeService(t)
	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `console.log("probe-42")`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError || !strings.Contains(resultText(result), "probe-42") {
		t.Fatalf("console result = %+v text=%q", result, resultText(result))
	}
}

func TestServiceWindowsListsNativeWindows(t *testing.T) {
	service, ctx := newFakeService(t)
	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `await cua.windows();`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError || !strings.Contains(resultText(result), `"window_id":77`) {
		t.Fatalf("windows result = %+v text=%q", result, resultText(result))
	}
}

func TestServiceActionsPassWindowIDAndReadBack(t *testing.T) {
	service, ctx := newFakeService(t)
	dragPathLog := t.TempDir() + "/drag-path.jsonl"
	t.Setenv("MCPX_FAKE_DRAG_PATH", "1")
	t.Setenv("MCPX_FAKE_DRAG_PATH_LOG", dragPathLog)

	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); await app.dragPath({ points: [[0, 0], [10, 0]], windowId: 77, readBack: true, activate: true });`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError {
		t.Fatalf("dragPath result = %+v text=%q", result, resultText(result))
	}
	entries := readToolLog(t, dragPathLog)
	if len(entries) != 1 {
		t.Fatalf("drag_path calls = %d, want 1: %+v", len(entries), entries)
	}
	arguments, _ := entries[0]["arguments"].(map[string]any)
	if arguments["window_id"] != float64(77) {
		t.Fatalf("window_id = %v, want 77: %+v", arguments["window_id"], arguments)
	}
	if arguments["read_back"] != true {
		t.Fatalf("read_back = %v, want true: %+v", arguments["read_back"], arguments)
	}
	if arguments["activate"] != true {
		t.Fatalf("activate = %v, want true: %+v", arguments["activate"], arguments)
	}
}

func TestServiceScreenshotReturnsMetadataAndImage(t *testing.T) {
	service, ctx := newFakeService(t)
	logPath := t.TempDir() + "/screenshot.jsonl"
	t.Setenv("MCPX_FAKE_SCREENSHOT_LOG", logPath)

	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); await app.screenshot({ windowId: 77, format: "jpeg", quality: 0.6, maxWidth: 320 });`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError || !strings.Contains(resultText(result), `"input_space":"window_screenshot_px"`) {
		t.Fatalf("screenshot result = %+v text=%q", result, resultText(result))
	}
	entries := readToolLog(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("screenshot calls = %d, want 1: %+v", len(entries), entries)
	}
	arguments, _ := entries[0]["arguments"].(map[string]any)
	if arguments["window_id"] != float64(77) || arguments["format"] != "jpeg" || arguments["quality"] != 0.6 || arguments["max_width"] != float64(320) {
		t.Fatalf("screenshot arguments = %+v", arguments)
	}
	var foundImage bool
	for _, item := range result.Content {
		if image, ok := item.(*mcp.ImageContent); ok && image.MIMEType == "image/jpeg" && len(image.Data) == 4 {
			foundImage = true
		}
	}
	if !foundImage {
		t.Fatalf("screenshot image content missing: %+v", result.Content)
	}
}

func TestServiceWindowsRequiresNativeSupport(t *testing.T) {
	service, ctx := newFakeService(t)
	t.Setenv("MCPX_FAKE_NEW_NATIVE_TOOLS", "0")

	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `await cua.windows();`,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(result)
	if result == nil || !result.IsError || !strings.Contains(text, "list_windows") || !strings.Contains(text, "MCPX_COMPUTER_USE_BINARY") {
		t.Fatalf("missing list_windows result = %+v text=%q", result, text)
	}
}

func TestServiceScreenshotRequiresNativeSupport(t *testing.T) {
	service, ctx := newFakeService(t)
	t.Setenv("MCPX_FAKE_NEW_NATIVE_TOOLS", "0")

	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); await app.screenshot();`,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(result)
	if result == nil || !result.IsError || !strings.Contains(text, "screenshot") || !strings.Contains(text, "MCPX_COMPUTER_USE_BINARY") {
		t.Fatalf("missing screenshot tool result = %+v text=%q", result, text)
	}
}

func TestServiceKeyboardWrappers(t *testing.T) {
	service, ctx := newFakeService(t)
	keyLog := t.TempDir() + "/keys.jsonl"
	t.Setenv("MCPX_FAKE_KEY_LOG", keyLog)

	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code: `const app = await cua.getApp("Fake App");
await app.hotkey("cmd", "shift", "p");
await app.typeText("hello", { submit: true });
await app.keySequence([
  { key: "cmd+a" },
  { text: "MCPX" },
  { sleepMs: 1 },
  { key: "Return" }
], { delayMs: 1 });
nodeRepl.write("keyboard-ok");`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError || !strings.Contains(resultText(result), "keyboard-ok") {
		t.Fatalf("keyboard result = %+v", result)
	}
	entries := readToolLog(t, keyLog)
	want := []struct {
		tool string
		key  string
		text string
	}{
		{tool: "press_key", key: "cmd+shift+p"},
		{tool: "type_text", text: "hello"},
		{tool: "press_key", key: "Return"},
		{tool: "press_key", key: "cmd+a"},
		{tool: "type_text", text: "MCPX"},
		{tool: "press_key", key: "Return"},
	}
	if len(entries) != len(want) {
		t.Fatalf("keyboard calls = %d, want %d: %+v", len(entries), len(want), entries)
	}
	for index, expected := range want {
		arguments, _ := entries[index]["arguments"].(map[string]any)
		if entries[index]["tool"] != expected.tool {
			t.Fatalf("keyboard call %d = %+v, want %+v", index, entries[index], expected)
		}
		if expected.key != "" && arguments["key"] != expected.key {
			t.Fatalf("keyboard key %d = %+v, want %q", index, entries[index], expected.key)
		}
		if expected.text != "" && arguments["text"] != expected.text {
			t.Fatalf("keyboard text %d = %+v, want %q", index, entries[index], expected.text)
		}
	}
}

func TestServiceRejectsInvalidDragPathPoints(t *testing.T) {
	service, ctx := newFakeService(t)
	t.Setenv("MCPX_FAKE_DRAG_PATH", "1")
	result, err := service.Execute(ctx, Request{
		RemoteSessionID: "session-1",
		Action:          "js",
		Code:            `const app = await cua.getApp("Fake App"); await app.dragPath({ points: [[0, 0]] });`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError || !strings.Contains(resultText(result), "at least two points") {
		t.Fatalf("invalid dragPath result = %+v", result)
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

func readDragLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	result := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var args map[string]any
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatalf("decode drag log %q: %v", line, err)
		}
		result = append(result, args)
	}
	return result
}

func readToolLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	result := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode tool log %q: %v", line, err)
		}
		result = append(result, entry)
	}
	return result
}

func assertDrag(t *testing.T, args map[string]any, fromX, fromY, toX, toY float64) {
	t.Helper()
	assertDragNear(t, args, fromX, fromY, toX, toY)
}

func assertDragNear(t *testing.T, args map[string]any, fromX, fromY, toX, toY float64) {
	t.Helper()
	for key, want := range map[string]float64{
		"from_x": fromX, "from_y": fromY, "to_x": toX, "to_y": toY,
	} {
		if got, ok := args[key].(float64); !ok || math.Abs(got-want) > 1e-9 {
			t.Fatalf("drag %s = %v, want %v: %+v", key, args[key], want, args)
		}
	}
}

func fakeNativeMCP(t *testing.T) string {
	t.Helper()
	t.Setenv("MCPX_FAKE_NATIVE_MCP", "1")
	return os.Args[0]
}
