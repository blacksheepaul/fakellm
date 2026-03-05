# fakellm / mockllm / llm faker/mocker

一个可编程的 OpenAI 兼容 Mock LLM，用于低成本压测网关。

初衷：测试 [prompt_endgame](https://github.com/blacksheepaul/prompt_endgame)

## 快速开始

```bash
go run ./cmd/server -addr :8080
```

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"mock","messages":[{"role":"user","content":"hi"}],"stream":true}'
```

## 故障注入

运行时热更新：

```bash
# 限制并发，触发 429
curl -X PATCH localhost:8080/admin/config -d '{"max_concurrent":1}'

# 越催越慢：QPS>50 时速率降为 50%
curl -X PATCH localhost:8080/admin/config -d '{"slowdown_qps_threshold":50,"slowdown_factor":0.5}'

# 加抖动和固定延迟
curl -X PATCH localhost:8080/admin/config -d '{"jitter_ms":200,"fixed_delay_ms":100}'
```

可调参数：并发上限、队列深度、token 速率、延迟抖动、越催越慢阈值。

## 端点

- `POST /v1/chat/completions` — SSE 流式响应（仅支持 `stream=true`）
- `GET/PATCH /admin/config` — 查看/热更新配置
- `GET /admin/stats` — 并发数、队列深度、QPS
