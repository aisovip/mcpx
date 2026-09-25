# Computer 能力修复与完善：窗口、坐标与反馈

## Goal

把 MCPX 的 `computer`（cua）通道从"动作一律报成功、模型只能猜"修成"可定位、可验证、能真正操作桌面"：能力继续下沉在 fork 的 `open-computer-use`（下称 OCU）原生层，MCPX 只保留工具入口、透传、能力探测与 artifact 注册，不再在 MCPX 内新增电脑操作语义。

## Context

demo 工作空间 `rs_JiAbt3YQlT5oarRm` 的实测证据（2026-09-25 04:05–18:58）：

- 18:41:58–18:50:04 共 24 次 `computer` 调用、10 次 `screenshot_capture`、9 次 `artifact`；逐张校验截图做像素比对，**画布区域 8 分钟内零变化**，模型汇报的"小猪已完成"没有任何截图证据。
- 所有 `drag`/`drag_path` 都返回 `succeeded`，结果一律 `(no output)`；模型只能靠 `throw new Error(...)` 探针取值（7 次）。
- 模型在 4 套坐标假设（窗口局部/2x/全局/2x 全局）之间反复试错，仍无法确定坐标语义。
- 元素索引漂移：模型点的 `33/34/41/48/63/93/110`，在当时 AX 树里实际是缩放/小地图/复制(disabled)/手形/全屏幕按钮，`click` 不回显落点。
- 目标窗口全程非 key（红绿灯灰），屏幕上有 Chrome、终端、tldraw 三窗叠放；`preferredFocusedWindow()` 隐式选窗，无窗口选择参数。
- 失败被层层吞掉：`internal/computeruse/service.go:158` 设 `OPEN_COMPUTER_USE_ACTION_READ_BACK=0`；sidecar 的 `click/drag/dragPath` 丢弃原生返回文本（含投递路径提示）；`computer` 调用 0 个 artifact。

同时发现**生产与实验不一致**：运行中的 daemon（`~/.local/bin/mcpx __background-child`）用 PATH 上的 `/opt/homebrew/bin/open-computer-use`（npm `open-computer-use@0.3.5` 预编译二进制，`strings` 无 `drag_path`），因此 `dragPath` 一直在走 MCPX 的多段 `drag` 回退；带 `drag_path` 的补丁构建只存在于 `/tmp/ocu-src.2OoCfc/.build/out/Products/Debug/OpenComputerUse`，且补丁未提交、位于 `/tmp`（随时丢失）。

## Decisions

1. **补丁落点**：继续在 OCU fork 上叠加第二个补丁；MCPX 侧只做入口、透传、能力探测（`native.hasTool`）与 artifact 注册。
2. **坐标系唯一化**：输入坐标空间固定为**窗口截图像素**（origin = 窗口左上角，scale = 截图像素 / 窗口点）。原生在状态与动作结果里回报机器可读元数据块，MCPX 原样透传，不解析、不换算。
3. **窗口显式化**：原生新增 `list_windows`；`get_app_state` 与所有动作接受可选 `window_id`；目标窗口非 key 时返回结构化 `warnings`，不再静默成功；`activate: true` 由调用方显式选择是否抢焦点（默认不抢）。
4. **动作可验证**：原生动作支持每次调用的 `read_back` 开关（回读动作后状态 + 截图）；动作结果统一带 `ok`、落点、投递方式、`warnings` 与原生文本。
5. **MCPX 契约修复**：JS 通道必须返回最后表达式的值（`node_repl` 语义）；`computer` 返回的 image content part 自动注册为 artifact 并附 `mcpx://` URI。
6. **删除兼容分支**：删掉 MCPX 的多段 `drag` 回退与相关常量；缺少 `drag_path` 的二进制直接报错并给出所需工具清单（不静默降级）。
7. **曲线采样留在 JS 层，但仅为参数换算**：`curve` 为 JS 函数时闭包无法跨进程，函数→点数组的求值只能发生在 sidecar；`line/quadratic/cubic` 同样在该层采样。此层不做任何 OS 交互，原生 `drag_path` 契约保持 points-only。
8. **截图参数下沉原生**：`format/quality/max_width/max_height/mode(window|region|screen)/region` 由原生实现；`cua.screenshot()` 返回图像 + 元数据。
9. **部署**：fork 落地到持久目录 `~/Workspace/code/ai/open-computer-use`（从 `/tmp` 迁移并提交补丁），构建产物通过既有 `MCPX_COMPUTER_USE_BINARY` 指定，不改写 npm 包内文件。

## Rationale

- 观察到的失败集中在"坐标不可知 + 窗口不可控 + 结果不可见"，三者都在原生层才拿得到真值（窗口 bounds、截图尺寸、投递路径），放在 MCPX 只能靠猜或复制平台代码。
- 保留多段 `drag` 回退会让"没画上"表现成"成功"，是本次事故的直接成因之一；仓库策略也不保留旧版本兼容，因此直接删。
- `read_back` 做成按调用开关，可以保留当前低延迟默认值，同时让模型在需要时拿到确定性证据。
- 函数曲线无法序列化到原生，是该形态的固有限制；把它的职责限定为纯求值，避免 MCPX 再次长成第二套实现。

## Alternatives Considered

- **B 方案：MCPX 侧用 AppleScript / Windows UIA 兜底窗口与坐标**：把电脑操作塞进 MCPX，新增系统授权与平台分支，与"能力不在 MCPX"的约束冲突，否决。
- **保留多段 `drag` 回退**：静默降级导致假成功，且与"不保留旧版本兼容"冲突，否决。
- **MCPX 侧解析原生文本、自己算坐标**：把语义复制到 MCPX，且原生文本不是稳定契约，否决。
- **MCPX 侧用 CGWindowList 自行枚举窗口**：同上，且要重写平台权限处理，否决。
- **保留 `(no output)` 语义、要求模型显式 `nodeRepl.write`**：与 `node_repl` 的既有契约不一致，且实测证明模型不会这么做，否决。

## Scope

### In

- MCPX：JS 通道返回值、动作结构化结果（含原生文本）、缺工具明确报错、删除多段 drag 回退、cua 图像自动注册 artifact。
- OCU fork：窗口枚举与选择、坐标元数据块、非 key 窗口 warnings、`activate`、动作 `read_back`、截图参数、`drag_path` 点上限 257 → 4096。
- 部署：fork 持久化 + 补丁提交 + 二进制构建 + daemon 指向补丁二进制 + README 说明。
- 端到端验收：在 tldraw（demo 场景）用元数据算出的坐标真实画出一个可见形状。

### Out

- 不新增 `waitFor` / `expect` 断言 DSL、剪贴板与文本替换原语。
- 不修改 artifacts 表结构（不记录 `call_id` 关联）。
- 不改 `screenshot_capture`（Go 侧既有工具保持现状，仅共享 artifact 注册管线）。
- 不做 browser 工具相关改动。
- 不把 fork 发布到 npm / brew，不改写 npm 包内二进制。

## Design

### 组件职责

| 层 | 职责 |
| --- | --- |
| OCU fork（Swift） | 窗口枚举/选择、坐标与截图元数据、投递策略与 warnings、动作回读、截图参数、`drag_path` 点上限 |
| MCPX sidecar（`internal/computeruse/computer_service_sidecar.mjs`） | REPL 值与动作结果返回、参数与图像透传、能力探测（缺 `drag_path` 报错）、曲线点采样 |
| MCPX Go（`internal/server/tools_computer.go`、`internal/computeruse/`） | `computer` 工具入口、会话与进程生命周期、审计、cua 图像 → artifact 注册 |
| 原生 runtime 解析（`internal/computeruse/runtime.go`） | 复用既有 `MCPX_COMPUTER_USE_BINARY` / PATH 解析，指向补丁构建 |

### 原生接口变更

- `list_windows()` → `[{window_id, app, title, bounds{x,y,width,height}, is_key, is_main, layer, order}]`。
- `get_app_state(app, window_id?, text_limit?, max_tree_nodes?, max_tree_depth?)` → 文本 + 截图 + 元数据块：
  `mcpx.window = {"window_id":…,"app":…,"title":…,"bounds":{…},"is_key":…,"screenshot":{"width":…,"height":…},"scale":…,"input_space":"window_screenshot_px"}`。
  文本同时给出一行换算提示：`screen_point = window_origin + pixel / scale`。
- 动作工具（`click`/`drag`/`drag_path`/`press_key`/`type_text`/`scroll`/`set_value`/`perform_secondary_action`）新增 `window_id?`、`read_back?`、`activate?`；结果包含 `warnings`（如 `target_window_not_key`）与既有投递提示文本。
- 新增 `screenshot(app, window_id?, mode, region?, format?, quality?, max_width?, max_height?)` → 图像 + 同一元数据块。
- 越界校验：坐标换算后落在目标窗口之外时返回 `coordinate_outside_window` 错误，不再发出无声事件。
- `drag_path`：点数上限 257 → 4096，其余校验不变。

### JS API 契约

- `await cua.windows()` → 窗口列表（原生 `list_windows` 透传）。
- `await cua.getApp(name)` → 绑定句柄，**不再自动输出整棵 AX 树**；需要状态时显式 `getAXState()`。
- `await app.getAXState({windowId?, textLimit?, maxTreeNodes?, maxTreeDepth?})` → 文本（含元数据块）+ 截图。
- `await app.click(target, {windowId?, readBack?, activate?, …})` → `{ok, target, windowId, warnings, native}`。
- `await app.dragPath({points|from/to/curve…, windowId?, readBack?, delivery?})` → `{mode:"drag_path", segments, points, durationMs, mouseButton, delivery, warnings, native}`。
- `await app.screenshot({windowId?, mode?, region?, format?, quality?, maxWidth?, maxHeight?})` → `{image, meta}`，图像自动 emit 并注册 artifact。
- JS 通道返回最后表达式的值；显式 `nodeRepl.write()` 仍可用；`console.log` 绑定到工具输出而不是 MCP stdio。

### 数据流

模型 → `computer(js)` → sidecar →（`MCPX_COMPUTER_USE_BINARY` 指定的）OCU 原生 MCP → 原生返回文本/图像/元数据 → MCPX 原样透传并把图像注册为 `mcpx://…/artifacts/…` → 模型可见文本、图像与 URI。

### 边界情况

- 窗口已关闭或 `window_id` 失效 → 显式 `window_not_found`，并返回可用窗口列表摘要。
- 目标窗口非 key → 动作仍执行但带 `target_window_not_key` warning；调用方可用 `activate: true` 前置窗口。
- 坐标越界 → `coordinate_outside_window` 错误（含换算后的屏幕坐标与窗口 bounds）。
- 二进制缺少 `drag_path` / `list_windows` 等工具 → 明确错误 + 所需工具清单，不降级。
- 多显示器与 Retina 缩放 → `scale` 由原生按目标窗口所在屏幕计算并回报。
- 截图过大 → 由原生的 `max_width/max_height/quality` 控制，MCPX 不再二次处理。

## Acceptance Criteria

1. `computer` js 通道执行 `JSON.stringify({a:1})` 返回 `{"a":1}`，不再 `(no output)`。
2. 任一动作返回结构化结果（含落点、投递方式、`warnings`、原生文本）；`dragPath` 返回实际使用的 points 与 mode，且不再有多段 `drag` 回退路径。
3. `getAXState` / `screenshot` 返回值包含元数据块：窗口 bounds、截图像素尺寸、scale、`input_space`。
4. `cua.windows()` 能列出 tldraw 与 Chrome 窗口；`getAXState({windowId})` 能指定目标窗口。
5. 非 key 窗口动作返回 `target_window_not_key`；`activate: true` 后目标窗口成为 key 窗口。
6. demo 场景重放：选中椭圆工具 → 用元数据换算出的窗口内坐标拖动 → 校验截图中出现该形状（像素比对可见变化）。
7. 运行中的 daemon 使用补丁二进制（`drag_path` 存在），`MCPX_COMPUTER_USE_BINARY` 生效。
8. 质量门禁通过：`gofmt -l ./cmd ./internal` 空、`go vet ./...` 干净、`go test ./... -count=1` 全绿；Swift 侧 `swift test`（OpenComputerUseKit 单测）通过。

## Test Strategy

- **MCPX 契约（Go + fake native）**：`internal/computeruse/fake_native_test.go` 基础上覆盖：JS 最后表达式返回值透传、动作结构化结果透传、缺 `drag_path` 时报错文案与 code、`reset` 语义不变。
- **MCPX 资源（Go）**：`computer` 结果中的 image content part 注册 artifact 并附 `mcpx://` URI；无图像时不注册（对照 `screenshot_capture` 既有测试）。
- **JS 层（Node 子进程）**：以 stdio 驱动 sidecar，覆盖：`(no output)` 回归用例、`console.log` 不污染协议、`dragPath` 校验错误文案、能力探测失败路径。
- **原生（Swift 单测）**：坐标换算（scale≠1、多显示器原点）、元数据序列化、非 key 窗口 warnings、越界错误、`drag_path` 点数上限、窗口选择解析。
- **端到端（macOS 本机，需用户在场）**：demo 场景重放，前后截图做像素比对；同时验证"非 key 窗口拖拽"与"越界坐标"不再静默成功。
- **回归**：`dragPath({from,to,curve:"linear"})` 的报错文案更新为受支持取值；`(no output)` 用例作为固定回归项。

## Implementation Plan

1. **Phase 0 部署对齐**：把 `/tmp` 的 OCU 补丁落到持久 fork 目录并提交；构建二进制；用 `MCPX_COMPUTER_USE_BINARY` 让 daemon 使用它；确认 `drag_path` 生效。
2. **Phase 1 MCPX 契约**：JS 返回值、动作结构化结果、`getApp` 不再刷树、删除多段 drag 回退、缺工具明确报错（RED → GREEN → REFACTOR）。
3. **Phase 1b MCPX 资源**：cua 图像 → artifact 注册 + URI 回填；`computer` 结果文本补充图像引用。
4. **Phase 2a 原生坐标与警告**：元数据块、坐标越界错误、非 key 窗口 warnings、`activate`。
5. **Phase 2b 原生窗口**：`list_windows`、`window_id` 选择（状态与动作）。
6. **Phase 2c 原生动作与截图**：动作 `read_back`、`screenshot` 工具与参数、`drag_path` 4096 点上限。
7. **Phase 3 端到端与文档**：demo 场景重放、README/计划文档说明补丁二进制要求与环境变量。
8. **收口**：完整 diff（含 untracked）复查，清理 `.tmp-mcpx-client/`，按 quality gate 全量验证。

## Verification

```bash
# 质量门禁
gofmt -l ./cmd ./internal
go vet ./...
go test ./... -count=1

# fork（OCU）
cd ~/Workspace/code/ai/open-computer-use && swift test

# 部署确认：运行中的 server 使用补丁二进制且存在 drag_path
pgrep -fl computer_service_sidecar   # 命令行应指向补丁构建
strings <补丁二进制> | grep -c drag_path   # 非 0
```

端到端证据：demo 场景重放前后的截图 artifact（像素比对可见变化）+ `computer` 调用返回的元数据块与结构化结果原文。

## Status（2026-09-25 21:55 更新）

### 已完成

- **Phase 1 MCPX 契约**：JS 通道返回最后表达式、动作返回结构化结果（`{ok, action, …, native}`）、`getApp` 只绑定不刷树、`console.*` 捕获、删除多段 `drag` 回退（缺 `drag_path`/`list_windows`/`screenshot` 时显式报错并要求 `MCPX_COMPUTER_USE_BINARY` 指向 MCPX 构建）。
- **Phase 1b 资源**：`computer` 返回的 image content part 自动注册为 artifact，并在结果文本追加 `artifact: {artifact_id, resource_uri, mime_type, bytes}`。
- **Phase 2a-2c 原生**：`list_windows`、`screenshot`（png/jpeg + quality + max_width/max_height）、`get_app_state`/动作的 `window_id`、`read_back`、`activate`、`mcpx.window` 元数据块、`coordinate_outside_window` 越界拒绝、非前台窗口 `target_window_not_key` 警告、`drag_path` 4096 点上限。
- **Phase 0 部署**：fork 迁移到 `~/Workspace/code/ai/open-computer-use`；drag_path 补丁已提交（`f1b1e51`）；第二补丁（窗口/坐标/截图）在工作区，Dev app 构建于 `dist/Open Computer Use (Dev).app`；MCPX 二进制替换到 `~/.local/bin/mcpx`（旧版备份 `mcpx.bak-202609252150`）。
- **测试**：`gofmt -l ./cmd ./internal` 空；`go vet ./...` 干净；`go test ./... -count=1` 38 个包全绿；fork `swift test` 174 通过 1 跳过。

### 阻塞项：新构建缺 macOS 授权

`open-computer-use@0.3.5`（npm）的 app 由上游 Developer ID（Yifan PANG, J9P29FA5BX）签名，已获 Accessibility + Screen Recording；MCPX fork 的 Dev 构建由本机证书签名且 bundle id 为 `com.ifuryst.opencomputeruse.dev`，**无法继承该授权**。实测：

```text
/opt/homebrew/bin/open-computer-use doctor      -> accessibility=granted, screenRecording=granted
dist/Open Computer Use (Dev).app/…/OpenComputerUse doctor -> accessibility=missing, screenRecording=missing
```

因此 Phase 3 端到端验收的剩余步骤是：用户给 Dev 构建一次性授权，然后以 `MCPX_COMPUTER_USE_BINARY` 重启 daemon 重放 demo 场景。授权前，`computer` 调用会返回 `Accessibility permission is required`。

### 已知限制（Windows）

fork 的 Windows runtime 由 `apps/OpenComputerUseWindows`（Go）实现，本次只同步了既有的 `drag_path`；`list_windows`、`screenshot`、`window_id`、`read_back`、`mcpx.window` 元数据仅 macOS 可用。Windows 上调用这些能力会得到明确的“需要 MCPX 构建”错误，其余动作不受影响。

## Status 增补（2026-09-25 22:15）

### macOS 端到端已验证

授权后（`doctor` 显示 `accessibility=granted, screenRecording=granted`）用补丁二进制跑通：

- `cua.windows()` 返回 93 个窗口，tldraw 窗口 `window_id=15779`、bounds `{1621,30,959x988}`。
- `getAXState({windowId:15779})` 返回 `mcpx.window` 元数据：`screenshot 1242x1280`、`scale 1.295`、`warnings:["target_window_not_key"]`。
- 坐标契约实测正确：点击截图像素 `(621,640)` 后系统指针落在 NSEvent `(2100.5, 556.0)`，与 `bounds.origin + pixel/scale = (2100.5, 524.2)`（top-left 坐标）完全一致。
- 绘制验证：AX 点击“椭圆形”按钮（Value: off → on）→ `dragPath({from:[280,300],to:[760,620],curve:"line",steps:40,delivery:"global",windowId})` → 独立 `screencapture -R` 与 `app.screenshot` 均显示画布上出现蓝色椭圆（前后截图 diff bbox `(422,453,1915,1838)`）。

### 关键发现：tldraw（Electron/canvas）必须用 `delivery: "global"`

同一坐标下 `app_post`（默认）拖拽在 tldraw 上是**静默空操作**——这正是 demo 里“画半天一笔没成功”的直接原因：native 返回成功、画布零变化。`global` 才能驱动该窗口。动作结果现在会把这句投递提示放在 `native` 文本里回传，模型可见。

### Windows 已补齐同一契约

`apps/OpenComputerUseWindows`（Go + runtime.ps1）新增：`list_windows`、`screenshot`（png/jpeg + quality + max_width/max_height）、所有动作的 `window_id` / `read_back` / `activate`、`mcpx.window` 元数据、越界坐标拒绝、`drag_path` 4096 点上限、`OPEN_COMPUTER_USE_ACTION_READ_BACK` 语义对齐。

坐标换算放在 Go 层（`windowPointFromScreenshotPixels`）：把截图像素按 `scale = screenshotWidth / bounds.width` 还原成窗口内点，再交给 PowerShell 加窗口原点并 `ScreenToClient`，因此 DPI 缩放下不会再把像素当逻辑点。

验证：`gofmt` 干净、`go vet ./...` 干净、`go test ./...` 通过、`GOOS=windows GOARCH=amd64|arm64 go build` 通过。**未在真实 Windows 上运行过**，需要在 Windows 机器上做一次冒烟（list_windows → get_app_state(window_id) → drag_path 画一条线）。
