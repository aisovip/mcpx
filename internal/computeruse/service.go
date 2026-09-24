package computeruse

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	buildversion "mcpx/internal/version"
	"mcpx/internal/winproc"
)

//go:embed computer_service_sidecar.mjs
var computerServiceSidecar []byte

const (
	defaultJSTimeout = 30 * time.Second
	maxJSTimeout     = 300 * time.Second
	handshakeTimeout = 30 * time.Second
	sessionIdleTTL   = 30 * time.Minute
	callGracePeriod  = 10 * time.Second
)

// Request describes one persistent Computer Use adapter call.
type Request struct {
	RemoteSessionID string
	Action          string
	Code            string
	Title           string
	TimeoutMS       int
	Meta            mcp.Meta
}

type adapterSession struct {
	mu       sync.Mutex
	client   *mcp.ClientSession
	cancel   context.CancelFunc
	lastUsed time.Time
}

// Service owns one persistent JavaScript/Computer Use adapter per Remote
// Session. The underlying open-computer-use MCP process is kept alive so
// element indexes and REPL bindings remain valid across calls.
type Service struct {
	mu          sync.Mutex
	sessions    map[string]*adapterSession
	adapterDir  string
	adapterPath string
	closed      bool
}

func NewService() *Service {
	return &Service{sessions: map[string]*adapterSession{}}
}

// ErrorCode returns the stable public code for a Computer Use runtime error.
func ErrorCode(err error) string {
	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		return runtimeErr.Code
	}
	return ""
}

// Execute runs js or reset on the persistent adapter for one Remote Session.
func (s *Service) Execute(ctx context.Context, request Request) (*mcp.CallToolResult, error) {
	if s == nil {
		return nil, &RuntimeError{Code: CodeStartFailed, Message: "computer use service is not initialized"}
	}
	sessionID := strings.TrimSpace(request.RemoteSessionID)
	if sessionID == "" {
		return nil, &RuntimeError{Code: CodeCallFailed, Message: "remote_session_id is required"}
	}
	action := strings.TrimSpace(request.Action)
	if action != "js" && action != "reset" {
		return nil, &RuntimeError{Code: CodeCallFailed, Message: "computer action must be js or reset"}
	}
	if action == "js" && strings.TrimSpace(request.Code) == "" {
		return nil, &RuntimeError{Code: CodeCallFailed, Message: "computer js requires non-empty code"}
	}

	s.evictIdle()
	slot, err := s.session(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	toolName, arguments := "js", map[string]any{"code": request.Code}
	if action == "reset" {
		toolName, arguments = "js_reset", map[string]any{}
	} else {
		if title := strings.TrimSpace(request.Title); title != "" {
			arguments["title"] = title
		}
		if request.TimeoutMS > 0 {
			arguments["timeout_ms"] = request.TimeoutMS
		}
	}

	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.client == nil {
		return nil, &RuntimeError{Code: CodeCallFailed, Message: "computer use session is closed"}
	}
	timeout := normalizeTimeout(request.TimeoutMS)
	callCtx, cancel := context.WithTimeout(ctx, timeout+callGracePeriod)
	defer cancel()
	result, err := slot.client.CallTool(callCtx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: arguments,
		Meta:      request.Meta,
	})
	slot.lastUsed = time.Now()
	if err != nil {
		s.dropSession(sessionID, slot)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return nil, &RuntimeError{Code: CodeTimeout, Message: fmt.Sprintf("computer js timed out after %s", timeout), Err: err}
		}
		return nil, &RuntimeError{Code: CodeCallFailed, Message: fmt.Sprintf("computer call failed: %v", err), Err: err}
	}
	return result, nil
}

func (s *Service) session(ctx context.Context, sessionID string) (*adapterSession, error) {
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]*adapterSession{}
	}
	if s.closed {
		s.mu.Unlock()
		return nil, &RuntimeError{Code: CodeStartFailed, Message: "computer use service is closed"}
	}
	if existing := s.sessions[sessionID]; existing != nil {
		s.mu.Unlock()
		return existing, nil
	}
	s.mu.Unlock()

	runtimeConfig, err := ResolveRuntime()
	if err != nil {
		return nil, err
	}
	adapterPath, err := s.ensureAdapter()
	if err != nil {
		return nil, err
	}

	sessionCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(sessionCtx, runtimeConfig.NodePath, adapterPath, runtimeConfig.BinaryPath)
	cmd.Env = envWithOverride("OPEN_COMPUTER_USE_ACTION_READ_BACK", "0")
	winproc.ConfigureNoWindow(cmd)
	client := mcp.NewClient(&mcp.Implementation{Name: "mcpx-computer", Version: buildversion.Current}, nil)
	connectCtx, cancelConnect := context.WithTimeout(ctx, handshakeTimeout)
	clientSession, connectErr := client.Connect(connectCtx, &mcp.CommandTransport{Command: cmd}, nil)
	cancelConnect()
	if connectErr != nil {
		cancel()
		return nil, &RuntimeError{Code: CodeStartFailed, Message: fmt.Sprintf("start computer use adapter: %v", connectErr), Err: connectErr}
	}

	created := &adapterSession{client: clientSession, cancel: cancel, lastUsed: time.Now()}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = created.close()
		return nil, &RuntimeError{Code: CodeStartFailed, Message: "computer use service is closed"}
	}
	if existing := s.sessions[sessionID]; existing != nil {
		s.mu.Unlock()
		_ = created.close()
		return existing, nil
	}
	s.sessions[sessionID] = created
	s.mu.Unlock()
	return created, nil
}

func (s *Service) ensureAdapter() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", &RuntimeError{Code: CodeStartFailed, Message: "computer use service is closed"}
	}
	if s.adapterPath != "" {
		return s.adapterPath, nil
	}
	dir, err := os.MkdirTemp("", "mcpx-computer-use-*")
	if err != nil {
		return "", &RuntimeError{Code: CodeStartFailed, Message: fmt.Sprintf("create computer use adapter directory: %v", err), Err: err}
	}
	path := filepath.Join(dir, "computer_service_sidecar.mjs")
	if err := os.WriteFile(path, computerServiceSidecar, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", &RuntimeError{Code: CodeStartFailed, Message: fmt.Sprintf("write computer use adapter: %v", err), Err: err}
	}
	s.adapterDir = dir
	s.adapterPath = path
	return path, nil
}

func (s *Service) evictIdle() {
	now := time.Now()
	type staleSession struct {
		id  string
		old *adapterSession
	}
	var stale []staleSession
	s.mu.Lock()
	for id, slot := range s.sessions {
		if slot == nil || !slot.mu.TryLock() {
			continue
		}
		if now.Sub(slot.lastUsed) > sessionIdleTTL {
			delete(s.sessions, id)
			stale = append(stale, staleSession{id: id, old: slot})
		}
		slot.mu.Unlock()
	}
	s.mu.Unlock()
	for _, item := range stale {
		_ = item.old.close()
	}
}

func (s *Service) dropSession(sessionID string, slot *adapterSession) {
	if slot == nil {
		return
	}
	s.mu.Lock()
	if s.sessions[sessionID] == slot {
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()
	_ = slot.close()
}

func (a *adapterSession) close() error {
	if a == nil {
		return nil
	}
	var err error
	if a.client != nil {
		err = a.client.Close()
	}
	if a.cancel != nil {
		a.cancel()
	}
	return err
}

// Close terminates every adapter and native child owned by the service.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	sessions := make([]*adapterSession, 0, len(s.sessions))
	for _, slot := range s.sessions {
		sessions = append(sessions, slot)
	}
	s.sessions = map[string]*adapterSession{}
	adapterDir := s.adapterDir
	s.adapterDir = ""
	s.adapterPath = ""
	s.mu.Unlock()

	var result error
	for _, slot := range sessions {
		slot.mu.Lock()
		if err := slot.close(); err != nil && result == nil {
			result = err
		}
		slot.mu.Unlock()
	}
	if adapterDir != "" {
		if err := os.RemoveAll(adapterDir); err != nil && result == nil {
			result = err
		}
	}
	return result
}

func normalizeTimeout(value int) time.Duration {
	if value <= 0 {
		return defaultJSTimeout
	}
	timeout := time.Duration(value) * time.Millisecond
	if timeout > maxJSTimeout {
		return maxJSTimeout
	}
	return timeout
}

func envWithOverride(key, value string) []string {
	prefix := key + "="
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, prefix) {
			env = append(env, entry)
		}
	}
	return append(env, prefix+value)
}
