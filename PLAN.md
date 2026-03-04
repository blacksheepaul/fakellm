# MockLLM 实现计划

## 项目目标

一个可编程的 OpenAI 兼容 Mock LLM 服务，用于压测/折磨上游网关。

- HTTP 框架：[Hertz](https://github.com/cloudwego/hertz)
- Go 版本：1.24+
- Streaming 协议：SSE (`text/event-stream`)
- 限流粒度：全局单队列
- 配置生效：Admin HTTP API 运行时热更新
- 端点范围：仅 `POST /v1/chat/completions`

---

## 目录结构

```
mockllm/
├── cmd/server/main.go
├── internal/
│   ├── config/         # 运行时配置 + 热更新
│   ├── admission/      # 并发门控 semaphore
│   ├── queue/          # 排队逻辑
│   ├── tokenstream/    # lorem token 生成 + 速率/抖动/slowdown
│   ├── handler/        # Hertz 路由，SSE 响应
│   └── admin/          # Admin API 路由
└── pkg/openai/         # OpenAI 协议结构体
```

---

## 三层管道

```
Request
  │
  ▼
[Admission]   并发上限（semaphore），超限直接 429
  │
  ▼
[Queue]       等待队列（带超时），排队上限可配，满了 503
  │
  ▼
[TokenStream] 生成 lorem token，按配置速率通过 SSE 吐出
  │
  ▼
Response (SSE text/event-stream)
```

---

## 各模块设计

### `pkg/openai` — 协议结构体

- `ChatRequest`：`model`, `messages`, `stream` 字段
- `StreamChunk`：SSE 每帧格式（`id`, `object`, `choices[].delta`）
- `ErrorResponse`：统一错误格式 `{"error": {"message": "...", "type": "..."}}`

### `internal/config` — 运行时配置

```go
type Config struct {
    // Admission
    MaxConcurrent int           // 并发上限，0=不限

    // Queue
    MaxQueueDepth int           // 队列深度上限
    QueueTimeout  time.Duration // 排队最长等待

    // TokenStream
    TokensPerSecond float64     // 基础 token 发送速率
    FixedDelayMs    int         // 每 token 固定附加延迟(ms)
    JitterMs        int         // 每 token ±随机抖动(ms)

    // Slowdown（越催越慢）
    SlowdownQPSThreshold float64 // 触发阈值（全局 QPS）
    SlowdownFactor       float64 // 超阈值后速率乘数（<1 表示变慢）
}
```

- 用 `sync/atomic.Pointer[Config]` 保证无锁读
- Admin 写时 copy-on-write，atomic swap

### `internal/admission` — 并发门控

- semaphore 用 `chan struct{}`，容量 = `MaxConcurrent`
- `TryAcquire()` 非阻塞，失败立即 429
- `Release()` 在 handler defer 中调用
- `MaxConcurrent=0` 时跳过门控

### `internal/queue` — 排队

- 全局 `chan Request`，容量 = `MaxQueueDepth`
- 入队：`select` 非阻塞写，满了 503
- 每个 `Request` 携带 `context.Context`（client 断开自动取消）
- `QueueTimeout`：入队后等待超时返回 504

### `internal/tokenstream` — Token 流

- 预置 lorem ipsum 文本，按空格切成 word 列表
- 流程：
  1. 原子读当前 Config
  2. 计算有效速率（QPS 超阈值则乘以 `SlowdownFactor`）
  3. 每个 word：等待 `interval + fixedDelay + jitter` → 写 SSE 帧
  4. 结束发 `data: [DONE]\n\n`
- QPS 统计：1 秒滑动窗口，`sync/atomic` 计数

### `internal/handler` — Hertz 路由

`POST /v1/chat/completions`：
1. 解析请求，非流式请求返回 400 提示只支持 stream=true
2. Admission.TryAcquire → 失败 429
3. 入队 → 失败 503
4. 设置 SSE header：`Content-Type: text/event-stream`，`X-Accel-Buffering: no`
5. 调用 tokenstream，逐帧 flush

### `internal/admin` — Admin API

| 方法  | 路径            | 功能                         |
|-------|-----------------|------------------------------|
| GET   | `/admin/config` | 返回当前配置快照（JSON）      |
| PATCH | `/admin/config` | JSON merge patch，热更新配置  |
| GET   | `/admin/stats`  | 当前并发数、队列深度、QPS     |

---

## 实现任务（分阶段提交）

### Phase 1 — 脚手架 ✅ 已提交 ec6b205
- [x] `go mod init`，引入 hertz 依赖
- [x] 提交：`chore: init go module with hertz dependency`

### Phase 2 — 协议 + 配置 ✅ 已提交 d5bca90
- [x] 实现 `pkg/openai`（协议结构体）
- [x] 实现 `internal/config`（Config + atomic 读写）
- [x] 提交：`feat: add openai protocol types and runtime config`

### Phase 3 — Admission + Queue ✅ 已提交 5b3f759
- [x] 实现 `internal/admission`（chan-based semaphore，TryAcquire/Release/Current）
- [x] 实现 `internal/queue`（bounded channel queue，context 取消，queue-wait timeout）
- [x] 提交：`feat: implement admission semaphore and request queue`

### Phase 4 — TokenStream ✅ 已提交 53edc0e
- [x] 实现 `internal/tokenstream`（lorem 流 + 速率/抖动/slowdown）
- [x] 单元测试 4 个全通过（EmitsDONE / ContainsLoremWords / CancelMidway / SlowdownReducesRate）
- [x] 提交：`feat: implement token stream with rate/jitter/slowdown control`

### Phase 5 — Handler + Server 🚧 进行中（未提交）
- [x] 实现 `internal/handler/chat.go`（Hertz SSE handler，串联三层管道，io.Pipe 接 SetBodyStream）
- [ ] **阻塞项**：`go mod tidy` 因网络超时未完成，go.sum 不完整，handler 包暂无法编译
  - go.mod 已手动写入 `require github.com/cloudwego/hertz v0.10.4`
  - 恢复后执行 `go mod tidy` 即可解除阻塞
- [ ] 实现 `cmd/server/main.go`
- [ ] 提交：`feat: wire up hertz server and SSE chat completions handler`

### Phase 6 — Admin API ⬜ 待开始
- [ ] 实现 `internal/admin`（GET/PATCH /admin/config，GET /admin/stats）
- [ ] 提交：`feat: add admin API for runtime config and stats`

### Phase 7 — 测试 ⬜ 待开始
- [ ] 单元测试：admission、queue
- [ ] 冒烟测试：curl 验证 SSE 流、429、503
- [ ] 提交：`test: add unit tests and smoke test instructions`

---

## 故障注入能力矩阵

| 行为               | 控制参数                                      |
|--------------------|-----------------------------------------------|
| 固定延迟           | `FixedDelayMs`                                |
| 随机抖动           | `JitterMs`                                    |
| 并发容量限制       | `MaxConcurrent`                               |
| 排队上限           | `MaxQueueDepth`                               |
| Token 吞吐速率     | `TokensPerSecond`                             |
| 越催越慢           | `SlowdownQPSThreshold` + `SlowdownFactor`     |
