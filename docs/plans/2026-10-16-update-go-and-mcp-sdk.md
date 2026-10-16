# 更新 Go 与 MCP Go SDK

- 日期：2026-10-16
- 状态：已批准并实施；验证完成
- 目标版本：Go 1.27.2、`github.com/modelcontextprotocol/go-sdk` v1.8.0

## Goal

将 MCPX 的 Go 最低版本和官方 MCP Go SDK 更新到当前选定版本；保持现有 OAuth 授权服务器及对外行为不变。

## Decisions

| 决策 | 结果 | 理由 |
| --- | --- | --- |
| Go 版本 | `go.mod` 使用 `go 1.27.2`；不额外添加 `toolchain` 指令，除非 Go 工具链实际要求 | 用户已确认更新 Go；本机已有 Go 1.27.2，且该版本符合 SDK 与项目构建环境 |
| MCP Go SDK | 更新到官方 `github.com/modelcontextprotocol/go-sdk` v1.8.0 | MCPX 使用官方 Go SDK；此版本是已核实的当前稳定版本 |
| OAuth 实现 | 保留 `internal/oauth` 现有实现，不引入 Authelia OAuth 框架或 ORY Fosite | 用户选择暂时保持当前 OAuth 实现；官方 MCP Go SDK 提供 OAuth 客户端和资源服务器辅助能力，不提供可直接替代 MCPX 的授权服务器 |
| OAuth 行为和状态 | 不更改端点、授权流程、令牌格式、客户端注册与刷新令牌持久化；不迁移或清理 OAuth 文件 | 版本升级不应导致现有客户端或授权状态失效 |
| OpenAI Secure MCP Tunnel | 不在本次接入 | Tunnel 改变部署形态，适用于私有连接场景；本次目标是更新现有 MCPX 网关依赖，不改为 Tunnel 运行时 |
| 版本文档 | 同步更新 `README.md`、`README.en.md`、`AGENTS.md` 等现行文档中的最低 Go 版本 | 当前使用和开发说明应与 `go.mod` 一致；不改历史计划中的历史版本记录 |

## Rationale and alternatives

### MCP SDK

MCPX 当前使用 `github.com/modelcontextprotocol/go-sdk` v1.7.0。已核实的 v1.8.0 是同一官方 Go SDK 的后续稳定版本，最低 Go 要求低于本次选定的 Go 1.27.2。升级后以现有项目测试和构建检查 API 与行为兼容性。

官方资料：

- [MCP Go SDK v1.8.0 发布说明](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0)
- [MCP Go SDK v1.8.0 `auth` 包文档](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.8.0/auth)
- [Go 1.27.2 发布记录](https://go.dev/doc/devel/release#go1.27.2)

### OAuth

MCPX 的 OAuth 授权服务器、客户端注册、CIMD、PKCE、JWT 与刷新令牌存储仍由 `internal/oauth` 实现。官方 SDK 的 `auth` 包包含 Bearer Token 校验、受保护资源元数据与客户端授权码流程，但不提供完整授权服务器，因此不能直接替换当前实现。OpenAI Secure MCP Tunnel 是另一种私有部署与传输架构，不作为本次依赖升级的一部分。

本次不以 SDK 升级为名调整 OAuth 元数据或协议行为；若以后要补齐 OpenAI 文档中的 issuer-response 等兼容行为，应另立范围并单独验证。

## Scope and design

### 修改范围

- `go.mod`：将 Go 指令从 1.26.1 更新到 1.27.2；将 MCP Go SDK 从 v1.7.0 更新到 v1.8.0。
- `go.sum`：运行 `go mod tidy`，只保留新依赖图所需校验项。
- `README.md`、`README.en.md`、`AGENTS.md`：更新当前最低 Go 版本说明。
- 对全仓库进行版本号搜索，更新仍作为当前要求的 Go 1.26.1 描述；保留历史计划、发布记录中作为历史事实的版本文字。
- 检查 `.github/workflows/ci.yml` 和 `.goreleaser.yaml`。CI 已通过 `go-version-file` 从 `go.mod` 读取 Go 版本，预期无需重复硬编码版本。

### 明确不改

- `internal/oauth/` 的实现、路由、配置契约和持久化格式。
- OAuth 客户端注册、刷新令牌与 token secret 文件。
- OAuth 的 `iss` 响应参数、scope 策略或其它 OpenAI 兼容性行为。
- MCP 工具目录、工具 Schema、服务端业务逻辑及公开 API。
- 不引入新的 OAuth 框架、依赖服务或 OpenAI Tunnel 客户端。

## Acceptance criteria

1. `go.mod` 要求 Go 1.27.2，并依赖 MCP Go SDK v1.8.0。
2. 现行 README 与仓库指南声明 Go 1.27.2 或更高版本；CI 与发布构建使用与 `go.mod` 相符的 Go 工具链。
3. `go mod tidy` 后依赖文件整洁，无无关依赖增删。
4. `internal/oauth` 源码和现有 OAuth 存储文件契约不变；升级不要求用户重新授权。
5. 全部单测、竞态检测、静态检查、格式检查和目标平台构建通过。
6. 完整 diff 不包含无关源码、历史计划改写或构建生成物。

## Test strategy

这是 Go 版本、模块依赖和文档的升级，没有计划中的 OAuth 或业务逻辑改动，因此不新增仅验证实现细节的测试；使用现有测试套件确认 SDK API 与项目行为兼容。实施时运行：

1. `go mod tidy`，随后再次执行 `go mod tidy -diff`（如当前 Go 工具支持）确认依赖文件稳定。
2. `go test ./... -count=1`
3. `go test -race ./... -count=1`
4. `go vet ./...`
5. `test -z "$(gofmt -l ./cmd ./internal)"`
6. `CGO_ENABLED=0 go build -o bin/mcpx-server ./cmd/mcpx-server`
7. `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...`
8. `git diff --check`、全仓库过期版本搜索，并复查完整 tracked 与 untracked diff。

## Implementation plan

1. 将 `go.mod` 的 Go 版本与 MCP Go SDK 依赖更新至本计划确定的版本。
2. 使用 Go 1.27.2 执行 `go mod tidy`，审阅 `go.mod` / `go.sum` 的传递依赖变化。
3. 更新 README 与仓库指南中的当前 Go 最低版本说明；检查 CI 和 GoReleaser 的版本来源，不作重复配置。
4. 执行上述测试、静态检查、格式检查和跨平台构建。
5. 复查完整 diff，确认 OAuth 源码和持久化契约未改变，并报告验证结果。

## Verification sources

- 当前 MCPX：`go.mod`、`README.md`、`README.en.md`、`AGENTS.md`、`.github/workflows/ci.yml`、`.goreleaser.yaml`。
- 官方 MCP Go SDK：v1.8.0 发布说明与 `auth` 包文档（见上方链接）。
- 官方 Go：1.27.2 发布记录（见上方链接）。
