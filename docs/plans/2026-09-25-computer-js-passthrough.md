# MCPX Computer JS Passthrough

## Goal

为 MCPX 增加一等公民 `computer` 工具，用持久 JavaScript REPL 控制 macOS 和 Windows 桌面应用。工具只暴露 `js` 与 `reset` 两个 action，实际原生动作复用外部 `open-computer-use` 二进制，不在 MCPX 内重写 Accessibility、ScreenCaptureKit、UI Automation 或输入模拟。

## Context

- MCPX 当前没有 `computer` 工具。模型可以调用 `browser`，但浏览器工具与桌面应用控制是两条独立能力。
- 官方 `@oai/sky` / ChatGPT Computer Use 依赖 ChatGPT.app 的签名、私有 IPC、host-services pipe 和 service-side sender authorization，不能通过模拟环境安全复用。
- `iFurySt/open-codex-computer-use` 是 MIT 许可的独立实现，发布版 `open-computer-use@0.3.5` 提供标准 stdio MCP server 和 9 个原生工具，支持 macOS、Windows、Linux。
- 发布版没有持久 JS REPL；其 `main` commit `51f3a590e734c374b627c25ca9831a912eb02b14` 的 adapter 行为作为 MCPX adapter 的参考，但不作为 MCPX 的代码或运行时依赖。
- `open-computer-use` 的原生工具在进程内保存 app snapshot 与 `element_index` 映射。MCPX 必须保持 upstream 进程长期存活，不能按普通 `mcp_tool` 每次启动后关闭。
- 用户已确认：MCPX 不增加安全审批，由模型决定 Computer Use 调用；浏览器工具保持现状，不回退上一轮 `browseruse` 改动。

## Decisions

- `computer` 是 support tool，参数为 `remote_session_id`、`action`、`purpose`、`code`、`title`、`timeout_ms`。
- 首版只支持 `action="js"` 和 `action="reset"`，不暴露 9 个结构化 action。
- `js` 使用持久 Node REPL，支持 top-level await，绑定在同一 Remote Session 内跨调用保留。
- `reset` 清空 JS bindings 和 MCPX adapter 缓存，不关闭目标应用，不主动清空原生进程中的 app snapshot。
- 每个 Remote Session 拥有独立的 adapter + `open-computer-use mcp` 子进程；同一 Session 的调用串行执行。
- upstream 进程和 adapter 在 Runtime 关闭时回收；空闲 30 分钟后回收，下一次调用重新启动。
- JS 全局 API 使用上游 `open-computer-use` 的 `cua` contract，不使用 `@oai/sky` 名称。
- adapter 由 MCPX 内嵌并通过临时 `.mjs` 文件启动。MCPX 不要求用户安装上游 plugin、JS adapter 或其他 npm 包，只要求 `open-computer-use` 原生二进制和 Node.js 18+。
- 返回 upstream MCP `CallToolResult` 的 content、isError 与 metadata；`computer` 不声明 outputSchema，避免破坏 text、image 等异构结果。
- `computer` 不进入 MCPX confirmation 流程，不接收 `user_confirmed`。工具 annotation 仍按真实风险声明为 destructive/open-world。
- 默认不启用全局指针回退；Windows 的 SetFocus 与 UIA 文本回退仍由 `open-computer-use` 自身默认值控制。
- MCPX 不自动安装、下载或更新 `open-computer-use`；缺失时返回包含安装命令的结构化错误。

## Rationale

- 复用独立 native runtime 可以立即覆盖 macOS 和 Windows，避免复制上万行平台代码。
- MCPX 只实现 JS adapter 和生命周期管理，职责集中在模型接口、状态持久化和错误语义。
- 每个 Remote Session 独占进程可以隔离 `element_index` 和 JS bindings，避免不同客户端互相覆盖 UI 状态。
- 使用 upstream 的 `cua` API 能让现有 `open-computer-use` skill 与示例代码继续适用。
- 不声明 outputSchema、不包装调用结果，可以保留截图 image block、错误文本和上游 metadata，行为与 `mcp_tool` 的透明转发一致。

## Alternatives Considered

### 模拟 ChatGPT Computer Use 环境

未采用。环境变量和 Unix socket 可以模拟，但官方 macOS launch constraints 与 service-side sender authorization 仍会拒绝外部进程；继续绕过需要伪造签名、entitlement 或私有 IPC 身份。

### 直接通过 `mcp_tool` 调用 `open-computer-use mcp`

未采用。发布版没有 `js` tool，而且现有 `mcp_tool` 每次调用启动并关闭 upstream 进程，`get_app_state` 产生的 element index 无法可靠用于后续 action。

### 在 MCPX 内自行实现 macOS/Windows 原生控制

未采用。需要重新实现 Accessibility、ScreenCaptureKit、CGEvent、UI Automation、截图像素映射、权限引导和输入回退，范围远大于本次目标。

### 直接把上游 JS adapter 作为运行时依赖

未采用。该 adapter 当前只在 GitHub `main`，没有随 npm 发布；直接依赖未发布源码会让 MCPX 的安装与升级不可控。

## Scope

### In

- 新增 `internal/computeruse` 包：运行时发现、持久 adapter 管理、Session 生命周期和错误映射。
- 新增 MCPX 自有的 `computer_service_sidecar.mjs`：MCP stdio、持久 JS REPL、`cua` API、原生 MCP child 代理。
- 新增 `computer` 公开工具及 catalog、annotation、capability inventory 和 guidance。
- 支持 `MCPX_COMPUTER_USE_BINARY`、`MCPX_COMPUTER_USE_NODE` 显式覆盖。
- 在 macOS 和 Windows 上解析平台二进制路径与 Node 路径。
- 为缺失二进制、启动失败、超时、JS 异常、reset 和并发调用提供稳定错误语义。
- 增加 fake native MCP server contract tests、server tool tests、目录回归测试和 opt-in live test。
- 更新 README，记录 Node.js、`open-computer-use`、macOS 权限和 Windows 桌面会话前提。

### Out

- 不支持或模拟官方 ChatGPT Computer Use。
- 不在 MCPX 中实现 macOS/Windows 原生 UI 自动化。
- 不新增 Computer Use 专用确认、审批或 `user_confirmed` 参数。
- 不控制浏览器页面；该能力继续由 `browser` 工具提供。
- 不自动安装、下载、签名或更新 `open-computer-use`。
- 不保证 JS bindings 跨 MCPX 重启或跨 Remote Session 持久化。
- 不支持 Linux 作为本次正式验收平台；代码保持可移植，但不宣称 Linux 支持。

## Design

### 组件

1. `internal/computeruse/runtime.go`
   - 解析 `MCPX_COMPUTER_USE_BINARY` 或 PATH 中的 `open-computer-use` / `ocu`。
   - 解析 `MCPX_COMPUTER_USE_NODE` 或 PATH 中的 `node`。
   - 返回稳定错误，区分 `runtime_not_found`、`node_not_found`、`binary_not_found` 和版本启动失败。

2. `internal/computeruse/service.go`
   - 维护 `remote_session_id -> adapter session` 映射。
   - 每个 session 具有独立 mutex，确保 JS 与原生 UI 状态串行访问。
   - 使用 MCP Go SDK 直接连接 MCPX adapter，并持有 `ClientSession` 跨调用复用。
   - adapter process 绑定到 session-scoped context，不绑定单次 handshake timeout；initialize 单独使用 30 秒 timeout。
   - 记录 last-used time；每次调用后清理超过 30 分钟未使用的 session。
   - `CloseSession(remoteSessionID)` 和 `Close()` 负责终止 adapter 及其 native child。

3. `internal/computeruse/computer_service_sidecar.mjs`
   - 作为 MCP stdio server 暴露 `js` 与 `js_reset`。
   - 启动 `open-computer-use mcp`，并保持同一 child 完成 initialize、tools/list 与 tools/call。
   - 在持久 Node REPL/Worker 中执行 JS，保留 top-level bindings。
   - 提供 `cua` API，并将它映射到 `open-computer-use` 的 9 个原生 tool。
   - 提供 `nodeRepl.write(value)` 与 `nodeRepl.emitImage(image)`，生成 MCP text/image content。
   - 在 `js_reset`、stdin EOF、SIGINT/SIGTERM 时清理 Worker 和 native child。
   - 设置 `OPEN_COMPUTER_USE_ACTION_READ_BACK=0`，由 JS 显式调用状态读取，避免 action 自动携带无用 snapshot。

### JS API

adapter 提供以下稳定入口：

```ts
cua.getState(options?: { emit?: boolean }): Promise<unknown>
cua.listApps(options?: { emit?: boolean }): Promise<unknown>
cua.getApp(nameOrBundleID: string): Promise<AppHandle>

app.getAXState(options?): Promise<unknown>
app.getScreenshot(options?): Promise<unknown>
app.getAXStateAndScreenshot(options?): Promise<unknown>
app.click(elementIndexOrPoint, options?): Promise<void>
app.scroll(elementIndex, direction, pages?): Promise<void>
app.drag(from: [number, number], to: [number, number]): Promise<void>
app.typeText(text: string): Promise<void>
app.pressKey(key: string): Promise<void>
app.setValue(elementIndex: number, value: string): Promise<void>
app.performSecondaryAction(elementIndex: number, action: string): Promise<void>
```

`nodeRepl.write` 与 `nodeRepl.emitImage` 只收集本次 JS call 的额外内容，不改变 native tool 返回值。

### 公开工具契约

```json
{
  "name": "computer",
  "input": {
    "remote_session_id": "string",
    "action": "js | reset",
    "purpose": "string",
    "code": "string (action=js 必填)",
    "title": "string (可选，最多 80 字符)",
    "timeout_ms": "integer (可选，1..300000，默认 30000)"
  }
}
```

- `action=js`：`purpose` 与 `code` 必填；`title`、`timeout_ms` 可选。
- `action=reset`：`purpose`、`code`、`title`、`timeout_ms` 必须省略或为空。
- `purpose` 仅用于审计与模型意图记录，不触发 MCPX confirmation。
- 工具不提供 `user_confirmed`。

### 数据流

```text
MCP client
  -> computer(action=js)
  -> MCPX Runtime / computeruse.Service
  -> persistent MCPX JS adapter
  -> open-computer-use mcp child
  -> macOS Accessibility/ScreenCaptureKit 或 Windows UIA/Win32
  -> target desktop app
```

调用结果反向按原路径返回；MCPX 只追加自己的 routing metadata，不把图片编码进 structuredContent。

### 错误与边界

- `COMPUTER_RUNTIME_UNAVAILABLE`：找不到 Node 或 `open-computer-use`。
- `COMPUTER_START_FAILED`：adapter 或 native child 无法 initialize。
- `COMPUTER_CALL_FAILED`：upstream tool 返回协议错误或进程退出。
- `COMPUTER_TIMEOUT`：JS call 超时；adapter 必须丢弃该 Worker 状态并允许下一次 `reset` 或新调用重启。
- `bad_request`：action 参数组合非法。
- `open-computer-use` 或系统返回的权限错误、UIA 错误与 unsupported error 原样返回，不映射为 browser 错误。

## Acceptance Criteria

- `tools/list` 包含 `computer`，且不包含 `user_confirmed` 参数。
- `computer(action="js", code="...", purpose="...")` 能在持久 REPL 中执行 top-level await。
- 两次 `js` 调用之间 top-level bindings 保留；`reset` 后旧 binding 不存在。
- `await cua.getApp("TextEdit")` 与 `app.getAXState()` 能通过外部 `open-computer-use` 返回真实 UI 状态。
- `nodeRepl.emitImage` 产生的 image content 原样返回客户端。
- 同一 Remote Session 的并发 `computer` 调用被串行化，不产生 element index 竞争。
- 缺失运行时返回可执行的安装指引，不出现 `BROWSER_UNAVAILABLE` 或平台桩错误。
- `computer` 调用不进入 MCPX confirmation；upstream 的默认安全开关保持不变。
- macOS 安装 `open-computer-use` 并完成 Accessibility/Screen Recording 授权后，live test 通过。
- Windows 至少通过 fake native contract test 与 `GOOS=windows` 编译；真实 Windows UIA smoke 必须在 Windows 主机执行后才能宣称 Windows 支持通过。

## Test Strategy

- Adapter happy path：fake native MCP server 返回固定 app state；验证 `cua.getApp`、`getAXState`、`typeText` 的 tool 参数映射。
- Persistence：两次 `js` call 复用变量；`js_reset` 后变量消失。
- Content：`nodeRepl.write`、`nodeRepl.emitImage` 以及 native image content 保持顺序和 MIME。
- Error：JS throw、timeout、native process exit、initialize failure 均返回稳定错误。
- Isolation：两个 Remote Session 使用两个 adapter/native child，bindings 不交叉。
- Concurrency：同一 Session 并发调用按顺序执行，不出现 interleaved tool call。
- Runtime discovery：PATH、`MCPX_COMPUTER_USE_BINARY`、`MCPX_COMPUTER_USE_NODE` 的成功与失败分支。
- Server contract：catalog、schema、annotation、无 confirmation、透明 CallToolResult。
- Regression：`mcp_tool`、`browser`、`screenshot_capture` 现有测试保持通过，公开工具目录更新为 21 个。
- Live macOS：仅在 `MCPX_COMPUTER_USE_LIVE_TEST=1` 且二进制存在时运行；调用 `list_apps`，不对用户应用执行破坏性动作。
- Cross-platform：本机 macOS 与 Linux CI 跑 adapter/unit tests；Windows 做交叉编译，并在真实 Windows 环境运行 fake native contract test。真实 Windows UIA 验收单独记录，不在本机伪造通过。

## Implementation Plan

1. 先写 `computer` catalog/schema/handler contract 与 fake adapter client 的 RED 测试，确认工具尚未注册且调用失败。
2. 实现 `computeruse.Service` 的长生命周期 MCP 连接、Session 生命周期、串行调用、空闲回收、运行时发现和错误映射；用 fake MCP server 使 Go 测试 GREEN。
3. 实现内嵌 JS adapter，覆盖 persistent bindings、`cua` API、text/image content、reset、timeout 和 native child 生命周期；用 fake native MCP server 跑 RED → GREEN。
4. 将 `computer` 接入 runtime、catalog、capabilities、guidance 和 audit/observation；确认无 confirmation 分支，并更新公开目录回归测试。
5. 更新 README 与安装说明，明确 Node.js 18+、`npm i -g open-computer-use`、macOS `doctor` 权限步骤、Windows 桌面会话要求。
6. 执行 macOS live smoke、完整 Go 测试、Node adapter 测试、race、gofmt、vet、发布构建和 Windows 交叉编译；记录真实 Windows 主机尚未执行的验证缺口。
7. 复查 tracked diff 与 untracked files，删除临时诊断文件，不提交 Git。

## Verification

- `go test ./internal/computeruse ./internal/server -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/computeruse ./internal/server -count=1`
- Node adapter contract tests
- `test -z "$(gofmt -l ./cmd ./internal)"`
- `go vet ./...`
- `CGO_ENABLED=0 go build -o /tmp/mcpx-server ./cmd/mcpx-server`
- `GOOS=windows GOARCH=amd64 go build ./cmd/mcpx-server`
- macOS opt-in live smoke：安装 `open-computer-use`，完成权限授权，运行 `computer(action="js")` 的 `list_apps` 路径
- Windows live smoke：必须在真实 Windows 桌面会话执行；本机只报告交叉编译与 fake contract test 结果
