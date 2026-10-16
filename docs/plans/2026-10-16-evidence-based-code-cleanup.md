# 基于证据的代码清理

- 日期：2026-10-16
- 状态：已实施；验证通过
- 基线提交：`db4b883 chore(deps): 更新 Go 与 MCP SDK`
- 目标：减少当前 Go 实现中的确定死代码、无效参数、测试专用生产辅助代码和可证明多余的分支；保持当前 MCP 与持久化行为不变。

## Goal

在不改变当前 MCPX 的公开工具与协议行为、OAuth 行为或现有运行时数据的前提下，清理有明确证据表明不可达或不产生效果的代码，并评估历史兼容路径是否确实不属于当前契约。

本次以小批次执行，先验证引用关系与契约，再删除；不以文件大小、命名或 `legacy` / `fallback` 字样作为删除依据。

## Decisions

| 决策 | 结果 | 理由 |
| --- | --- | --- |
| 清理方法 | 先做只读审计，列出调用点、协议影响与测试，再处理确定项 | 代码约 62,440 行（含测试）；全仓库关键词扫描会命中仍在工作的安全和平台逻辑，不能代替可达性分析 |
| MCP 公共契约 | 工具名、数量、输入/输出 Schema、annotations、结果和错误语义、资源与 HTTP 路由保持不变 | 当前 MCP 行为是本次清理的硬边界；`public_catalog_test.go` 和协议测试是主要回归门禁 |
| OAuth 与身份验证 | 不改变端点、客户端认证、令牌格式、PKCE、DCR/CIMD、验证策略或现有 OAuth 持久化文件 | 代码精简不得降低授权强度或要求用户重新授权 |
| 配置与持久化 | 保留现行配置语义、SQLite schema、历史 migrations 与既有数据库数据 | 迁移代码仍可能被已存在的 `~/.mcpx` 数据库使用；删除它会改变已有数据的启动行为 |
| 安全与真实兜底 | 保留安全校验、并发/取消保护、临时文件原子替换、有效的跨平台实现、真实 I/O 错误处理和恢复路径 | 这些路径保护当前操作或数据，不因“兜底”命名就属于冗余 |
| 大文件结构 | 不为降低行数机械拆分 `runtime.go`、`tools_command_execute.go`、`plan/service.go`、`arc/human.go` 等文件 | 文件大小不是重复或错误抽象的证据；无明确职责混杂不做纯重排 |
| SDK 元数据 | 保留目前 `clientInfoFromContext` 的行为；本次不改为写入 SDK 请求中的真实客户端信息 | Go SDK v1.8.0 的 `ServerRequest.ClientInfo()` 能提供请求身份，但替换当前固定的 `unknown` 会改变持久化客户端元数据，不符合行为保持边界 |
| 历史协议分支 | 仅在确认不参与当前公开契约、当前 handler 或有效内部调用后，才考虑删除旧字段别名/适配逻辑 | 仓库规定不新增旧版兼容，但清理现有代码仍须证明它不承载当前客户端路径 |

## Rationale and alternatives

### 推荐方案：证据驱动的窄批次清理

首先检查 MCP 公开目录、HTTP/OAuth 路由、当前配置及持久化路径。随后对每个候选项做静态调用点搜索，检查接口注册、反射/字符串派发、平台构建标签和测试使用。只清理不参与当前契约的私有代码；每批结束运行受影响包测试，并以全量测试和构建确认边界保持。

目前已发现的明确候选项：

1. `internal/server/tools_catalog.go` 中的 `captureToolIndex` 为空操作，`registerTools` 调用它之前，`addTool` 已经填充 `toolIndex`。删除此空调用和方法不改变 MCP 注册。
2. `listedToolMap` 当前仅由测试文件调用，可评估移到测试辅助文件，避免把测试快照实现放在生产源码中。
3. `commandRequestDigestWithPayload` 接收 `requestID` 后立即丢弃；可删除此私有参数及调用点，保持当前摘要输入和输出不变。
4. `RetentionService.checkpoint` 返回的 `busy` 布尔值只被接收后丢弃；可评估将内部函数简化为只返回错误。仍保留 PASSIVE checkpoint、错误记录及 WAL 维护行为。
5. `internal/envelope/envelope.go` 中旧嵌套 `payload`、`session_id`、`call_id` / `callId` 输入均不属于当前扁平 MCP Schema；删除其归一化/合并行为，并保留过滤逻辑，避免这些旧参数进入业务 payload。`goal` 仍在扁平字段过滤中拦截，防止旧字段进入业务处理。
6. `commandFailureCode` 的 stderr 参数只被忽略，且调用结果明确只由退出码决定；删除无用参数与对应 stderr 读取，保留退出码分类行为。

`internal/terminal/exec.go` 的 shell 路径仍由当前 `internal/skill/exec.go` 与 `internal/server/tools_ext.go` 调用，因此不能整体移除。官方 Go SDK v1.8.0 的 `ClientInfo()` 是请求级 API，不是 context accessor；接入它会改变现有持久化记录，本次不做。

### 不选的方案

- **全仓库按 `legacy`、`fallback`、`deprecated` 搜索并删除：** 会误删当前协议归一化、安全校验、跨平台实现和恢复行为。
- **一次性拆分多个大文件：** 增加文件、接口和变更面，却没有证明能够减少真实重复或耦合，扩大回归风险。
- **删除历史数据库 migrations：** 可能使已存在的数据库无法打开或丢失其可用状态，超出“当前 MCP 实现不受影响”的边界。

## Scope and design

### 本次范围

- 检查当前跟踪的 Go 源码（`cmd/` 与 `internal/`），定位无生产调用方的私有符号、空操作、仅供测试使用的生产辅助代码、无效参数和重复实现。
- 对历史协议兼容分支逐项核实其数据来源、消费方、schema/route 注册及当前测试；只删除不属于当前契约的分支。
- 清理必要的测试与注释；只在行为契约改变时同步更新协议测试和当前文档。
- 只编辑经证实的候选项；审计后若无额外确定项，不为达到代码量目标而扩大范围。

### 明确不改

- MCP 工具名称、公开工具数量（当前 15 个）、参数、输出 Schema、annotations、资源模板、Streamable HTTP 行为。
- OAuth 与 Bearer 认证行为、权限校验、CIMD、PKCE、DCR、token secret 和 refresh-grant 持久化。
- 用户配置字段及其默认值、项目配置合并语义。
- SQLite 的迁移版本/SQL、表列、保留策略及既有数据库内容。
- 当前工具的命令审批、文件路径限制、幂等语义、会话归属、取消与恢复行为。
- 不新增抽象、依赖或通用清理框架；不删除使用中的平台实现或故障处理。

## Acceptance criteria

1. 当前 MCP 工具仍恰好为 `public_catalog_test.go` 所列 15 项，Schema、annotations、结构化结果和公开资源不变。
2. 当前 HTTP 路由、OAuth discovery、授权码/PKCE/DCR/CIMD 流程及 bearer 校验行为不变。
3. SQLite migrations、现有 schema 和用户数据操作不变；所有被删除的持久化代码均无当前调用路径（本计划不删除 migrations）。
4. 每个删除项都有提交前可复核的证据：生产引用点、运行时注册/反射情况、构建标签、对应测试与不改变公开行为的理由。
5. 只移除确认为无效、不可达或重复的代码；仍被当前 `internal/skill/exec.go` / `internal/server/tools_ext.go` 使用的 shell 路径以及安全/平台/错误恢复分支保留。
6. 全量测试、竞态检测、静态检查、格式检查、Linux 构建和 Windows amd64 交叉构建通过。
7. 完整 diff 不包含工作区生成物、无关格式化、大范围文件拆分或未授权的配置/存储行为变化。

## Test strategy

这是 Go 后端代码清理。对任何会改变当前可观察行为的候选项，先更新/添加行为测试，确认它在旧实现下按预期失败（RED），再删除分支并通过（GREEN）。纯空操作/不可达代码的机械删除没有可合理失败的业务行为断言；对此以现有 MCP 契约测试保持绿、引用审计和编译证明不变，不编造测试来断言实现细节。

执行顺序：

1. 先记录现有基线：全量测试和检查的当前结果，并复核工作区清洁状态。
2. 每个行为变更先做 RED 测试；只对经确认不属于当前契约的旧别名编写淘汰测试。
3. 纯行为中性的死代码清理先检查引用与注册点，再做最小删除；运行受影响包测试。
4. 收口时运行：
   - `go test ./... -count=1`
   - `go test -race ./... -count=1`
   - `go vet ./...`
   - `test -z "$(gofmt -l ./cmd ./internal)"`
   - `CGO_ENABLED=0 go build -o bin/mcpx-server ./cmd/mcpx-server`
   - `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...`
   - `git diff --check`
5. 检查完整 tracked 与 untracked diff；核实工具目录、路由、OAuth 测试、状态 schema/migration 未发生计划外变化。

## Implementation plan

1. 记录 `db4b883` 基线下的工作区状态与验证结果，确认无混入的用户改动。
2. 审计 MCP 公开边界：工具注册/Schema、资源、HTTP 路由、OAuth 与配置；记录清理前行为快照/测试。
3. 对候选函数和历史分支做引用矩阵：定义位置、生产调用点、测试调用点、动态注册/平台标签、当前契约依赖、建议处理。
4. 清理确定的行为中性候选：空 `captureToolIndex`、测试专用 `listedToolMap`、digest 与 `commandFailureCode` 中被忽略的参数/未使用 wrapper、被丢弃的 checkpoint 状态；逐项核对引用并运行目标包测试。
5. 对 `ParseRequest` 的旧 nested `payload`、`session_id` 与 `call_id` / `callId` 归一化先写回归测试并确认 RED，再改为只从当前扁平参数构造业务 payload；保留 `goal` 过滤，并验证当前 `purpose` / `intent` 路径。
6. 对新发现的高风险候选（配置、OAuth、安全、持久化、命令/文件执行行为）停止该项并回到用户确认，不借“清理”扩大授权。
7. 完成全量验证、复查完整 diff，汇报实际删除项、保留项及每项证据。

## Risks and boundaries

- 仓库未安装 `staticcheck`、`golangci-lint` 或 `deadcode`。引用审计使用 Go 包依赖、全仓库符号/字符串搜索、工具注册、平台文件与现有测试，不在本次新增静态分析依赖。
- 名称带有 `legacy` 或 `fallback` 不代表无用；匹配结果必须回到数据流和调用链核实。
- 任何涉及 MCP schema、认证授权、命令/文件安全、配置默认值、SQLite 迁移或持久化语义的计划外变化都要停下，更新计划并再次征求确认。

## Implementation results

- `RED`：`TestParseRequestIgnoresLegacyArgumentAliases` 在删除归一化前失败；`TestParseRequestMergesFlatArgumentsIntoPayload` 在删除旧 nested `payload` 合并前失败。
- 目标包：`go test ./internal/envelope -count=1` 通过（20 项）；`go test ./internal/server ./internal/state -count=1` 通过（253 项）。
- 全量：`go test ./... -count=1` 与 `go test -race ./... -count=1` 均通过（699 项，40 个包）。
- 其它验证：`go vet ./...`、gofmt 检查、Linux 构建、Windows amd64 交叉构建、`git diff --check` 均通过。
