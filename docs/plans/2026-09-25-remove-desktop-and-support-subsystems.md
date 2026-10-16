# MCPX：移除桌面控制、托盘 GUI、Operation 与 Environment

- 日期：2026-09-25
- 状态：已批准并实施；验证完成
- 目标版本：当前版本直接删除，不保留旧工具名或旧入口

## Goal

精简 MCPX 的公开 MCP 工具面与随包代码，移除用户明确选择的四组能力：

1. `browser` 与 `computer` 桌面/UI 控制工具及各自运行时。
2. Windows 专用 `mcpx desktop` 托盘 GUI 和整套 `internal/desktop` 实现。
3. `operation_batch`、`operation_manage` 及它们依赖的通用 Operation 异步调度接口。
4. `environment`、`environment_read` 和 Session 创建时的自动环境快照采集。

保留 `screenshot_capture`，以及 `execute` 的终端 Task 生命周期（`run`、`attach`、`stop`、`stdin`）。其余 MCP 工具和 CLI 能力保持不变。

当前工具数预计从 21 降到 15：删除 computer/browser 两项、Operation 两项、Environment 两项；`screenshot_capture` 仍注册。

## Decisions

| 决策 | 结果 | 理由 |
| --- | --- | --- |
| 桌面/UI 控制 | 删除 `browser`、`computer` 与 `internal/browseruse`、`internal/computeruse` | 用户选择 B；不再要求外部原生进程、Node、浏览器扩展或桌面辅助功能授权 |
| Screenshot | 保留 `screenshot_capture` 及 `internal/screenshot` | 用户明确要求保留；它独立完成屏幕截图与产物登记 |
| Desktop GUI | 删除 `mcpx desktop` 命令与整个 `internal/desktop` 目录，包括 GUI、托盘、内嵌前端和 GUI 内的 Cloudflare 管理界面 | 用户明确要求整个 desktop 一起删除；CLI 服务、workspace、stop、update 和部署教程不因此删除 |
| Operation | 删除批量 DAG 工具、管理工具及通用 Operation 调度/持久运行时；从其他工具 Schema 中移除 `execution_mode` | 异步 Operation 的结果依赖 `operation_manage` 才能读取或恢复。只删除工具会留下无法管理的操作；直接工具调用保持同步 |
| Terminal Task | 保留 `execute` 的 Task 管理动作 | 这是终端任务自己的生命周期，与通用 Operation 服务不同，可继续通过 execute 管理长命令 |
| Environment | 删除 `environment`、`environment_read`、环境采集服务及 Session 创建时的环境快照读写；移除 Session 返回的环境快照字段 | 用户批准移除 Environment 工具和自动采集，避免保留不可读的后台采集行为 |
| SQLite 历史数据 | 不删除既有迁移、表、列或用户数据库数据 | 避免在未获授权时执行破坏性数据清理；保留已有过期数据清理逻辑，不再写入新 Environment/Operation 记录 |
| Operation ID 审计字段 | 保留事件、工作区身份推进、Task 与 Artifact 使用的通用 `operation_id` 字段 | 这些字段不等同于 Operation 调度功能，仍由独立业务流程使用 |
| 历史文档 | 保留已完成的历史设计/计划记录；更新 README、英文 README、当前 evaluation 和现行 agent/tool guidance | 历史记录不充当当前契约，当前使用文档必须反映删除后的行为 |

## Rationale and alternatives

### Operation

- **选择：** 移除通用调度器和 `execution_mode="async"`，保留同步工具调用与 `execute` Task。
- **只删除两个公开工具：** 不采用。其他工具目前能提交异步 Operation；提交后需要 Operation 管理入口查询、等待、取消或恢复，移除入口会造成不可完成的调用路径。
- **只移除 `operation_batch`：** 不采用。用户明确要求同时移除两个 Operation 工具，而且仍无法解决其余工具异步结果依赖 `operation_manage` 的问题。

### Environment

- **选择：** 停止自动采集，移除服务与 Session 输出字段，不主动清除旧 SQLite 数据。
- **只隐藏两个工具但保留每次 Session 自动采集：** 不采用。会继续产生用户已要求移除的 Environment 数据收集与存储行为。
- **清空旧快照并删除数据库表/列：** 不采用。会破坏既有本地数据，超出本次授权。未来如需完全清理磁盘数据，另行确认。

### Desktop

- **选择：** 删除整个 GUI 子系统，而非保留失去 Browser Service 后会报错的浏览器状态卡片。
- **只删浏览器卡片、保留 GUI：** 不采用，用户已明确要求删除整个 desktop。

## Scope and design

### Public MCP catalog

- `internal/server/tools_catalog.go`：删除 `browser`、`computer`、`operation_batch`、`operation_manage`、`environment`、`environment_read` 的注册与专属 Schema；保留 `screenshot_capture`。
- `internal/server/capabilities.go`、`internal/server/agent_guidance.go`、`internal/server/tools_domains.go`：同步移除公开名称、能力定义与 guidance dispatch 条目。
- `internal/server/prompts/tools.yaml`、`internal/server/guidance/agent.yaml`：删掉六个已移除工具的描述、路由建议和能力标签。
- `internal/server/observability.go`：删掉 Operation 异步提交分支与 computer 特殊处理。普通工具直接调用原 handler；ARC、Activity、审计和事件观测保持工作。
- `internal/server/tools_clean_core.go`、`internal/server/tools_artifact.go`、`tools_catalog.go` 中调用方 Schema：移除 Operation 批量限制和 `execution_mode` 字段。保留用于 workspace identity transition、Task、Artifact、历史事件的 `operation_id` 字段。
- 更新公共工具清单、数量、Schema 与 Annotation 测试，明确确认已移除六项、仍暴露 `screenshot_capture`，且剩余工具没有 `execution_mode`。

### Runtime and packages

- 删除 `internal/server/tools_browser.go`、`tools_browser_test.go`、`tools_computer.go`、`tools_computer_test.go`。
- 删除 `internal/browseruse/`、`internal/computeruse/`。
- 删除 `internal/server/tools_operation.go`、其 Operation 专属测试，以及 `internal/operation/`。
- 从 `internal/server/runtime.go` 移除 Browser、Computer、Operation 服务字段、构造、关闭流程；不再在启动时初始化 Operation worker。
- 移除 `internal/server/operation_runtime.go` 和 `execution_mode` 分派逻辑。保留 `execute` Task 的 Task Manager、task log、cancel/attach/stdin 路径。
- 删除 `internal/server/tools_environment.go` 与 `internal/environment/`。从 `createRemoteSession` 移除自动环境检查；移除 `remotesession.Session` / `CreateResult` 的 Environment 快照输出字段及专用写入方法。Session 查询不再读取 Environment 快照列；既有 SQLite 列和历史迁移保留。
- 保留 `internal/state/retention.go` 对既有 Operation 与 Environment 快照表的过期记录清理查询，以及历史 schema migration。新版本不再插入这些记录；不引入清空用户状态的 migration。

### CLI, dependencies, and docs

- 移除 `cmd/mcpx-server/main.go` 中 `desktop` 子命令 dispatch、`runDesktop`、usage 与已知命令登记，并更新 CLI 测试。
- 删除 `internal/desktop/`，包括 Windows 托盘、Cloudflare 图形管理、Wails API、Vue 源码、已提交的 `frontend/dist` 和占位实现。
- 运行 `go mod tidy`，移除不再需要的 Wails 与其依赖；确认其它平台构建不受影响。
- 更新 `README.md`、`README.en.md`、`docs/evaluations/clean-core-p1-p4.md`：删除 GUI / Computer / Browser / Operation / Environment 工具现行说明，移除已失效的 async Operation 使用教程，保留 `screenshot_capture` 描述和现存 Cloudflare 部署教程。
- 不修改已完成的历史 plan/spec，不删除 `~/.mcpx` 用户数据或其他运行时目录。

## Acceptance criteria

1. `tools/list` 恰好公开 15 个工具；不存在 `browser`、`computer`、`operation_batch`、`operation_manage`、`environment`、`environment_read`；存在 `screenshot_capture`。
2. 所有剩余工具 Schema 不提供 `execution_mode`；普通工具调用不提交通用 Operation，`execute` 的 `run/attach/stop/stdin` 行为不变。
3. 新建 Session 不再触发 Environment 检查、快照写入或返回 Environment 快照标识/摘要。
4. `mcpx desktop` 不再是有效子命令；CLI 帮助中不再列出它；Workspace/服务启动停止、OAuth 注册、update 与 observe 等其他命令保留。
5. 不再构建/启动 Browser、Computer、Operation、Environment、Desktop 实现；Go 依赖中移除仅由 Desktop 引入的模块。
6. 现有历史 SQLite migrations 与数据不被删除；过期记录清理仍可运行。
7. 中文/英文用户文档和当前 evaluation 不再把被删除能力描述为当前 MCPX 功能。

## Test strategy

按后端 TDD 执行：

1. **RED：** 先更新工具目录/能力测试，断言六个名字已不存在、`screenshot_capture` 存在、目录为 15 项；更新 Schema 测试，断言无 `execution_mode`；更新 Session 测试，断言创建 Session 不返回/写入环境快照；更新 CLI 测试，断言 `desktop` 是未知命令。运行各目标测试并确认失败来自旧行为。
2. **GREEN：** 删除对应运行时、注册与命令接线，调整测试到新契约；逐包跑受影响测试。
3. **REFACTOR：** 删除没有调用者的包和测试，`go mod tidy`，清理现行文档，检查历史存储清理 SQL 是否仍被现有数据路径需要。
4. **验证命令：**
   - `gofmt -w` 修改后的 Go 文件；`test -z "$(gofmt -l ./cmd ./internal)"`
   - `go test ./internal/server ./internal/remotesession ./internal/state ./cmd/mcpx-server -count=1`
   - `go test ./... -count=1`
   - `go vet ./...`
   - `CGO_ENABLED=0 go build -o bin/mcpx-server ./cmd/mcpx-server`
   - `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...`（验证删除 Windows GUI 后的跨平台编译）
   - 如项目 Evaluation 流程有可执行命令，再跑相关 clean-core evaluation。
5. 检查完整 tracked 与 untracked diff、`go.mod`/`go.sum`、文档引用、CLI help、工具目录测试，以及编译生成物；不提交 Git。

## Implementation plan

1. 编写并运行工具目录、Schema、Session 创建与 CLI 的 RED 测试。
2. 删除 MCP 注册、capability metadata、通用 async dispatch、Browser/Computer handlers、Environment handlers/Session 采集、Desktop CLI 入口。
3. 删除不再引用的 Browser/Computer/Operation/Environment/Desktop 包与测试；整理受 Operation 影响的 server/inflight/retention 测试，保留 Task 级别的长命令测试。
4. 清理 docs、tool prompts、agent guidance；调整公共工具数量与 evaluation 文档。
5. `go mod tidy` 并运行全部验收命令。
6. 对完整 diff 进行简化与安全复查，特别确认没有遗留已失效工具名、不可访问的 async result 路径、自动环境数据采集或构建产物。

## Risks and boundaries

- 移除通用 `execution_mode="async"` 会让原先通过 Operation 调度的非终端工具改为只支持直接同步调用；这是移除不可管理的 Operation 生命周期所必需的用户可见变化。
- 删除 Desktop 也删除其 Cloudflare Tunnel 图形配置、启停、健康检查和故障恢复 UI；Cloudflare Tunnel 命令行/外部部署方式不在本次实现范围内。
- 既有数据库中可能留有过期 Operation/Environment 记录和对应 schema。为避免数据丢失，保留历史迁移与过期记录清理，不新增删除表/列或清空历史数据的 migration。
- 用户指定保留的 `screenshot_capture` 独立于已删除的 Browser/Computer 工具，不应随其测试、Schema、Artifact 注册路径一起删除。
