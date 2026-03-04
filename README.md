初衷：低成本地测试[我的这个项目](https://github.com/blacksheepaul/prompt_endgame)
需求：
- 轻量
- OpenAI兼容格式的API
- 可编程速率
- 能换着花样折磨网关，包括
  - 模拟抖动(固定延迟、随机延迟）
  - 并发容量限制（固定、动态）
  - RPM限制、TPM限制
  - 动态的latency，越催越慢
  - 背压检测(optional)
  - 资源泄漏检测(optional)
  - 其他故障注入
