# internal/audit

端到端审计管线编排（ROADMAP B6）：把「真实份额审计」组装成一份可读报告。

## 职责

- `audit.go`：`IngestClaude`（collect.ClaudeTurn → ChargeEvent 入账，EXACT
  记账口径）、`ThetaFromPrior`（先验中位数组装 θ 快照）、`Run`（在线 MLE →
  离线后验 → gauge 读数 → 对账归因）
- `render.go`：`Report.Render`——人类可读报告（参数后验区间 / 等价 API 美元
  读数 / 残差归因表 / 链健康度诊断）

## 不变量（违反 = bug）

1. 纯编排：不改任何下层结果——解析归 collect、扣减归 semantics、存储与
   对账归 ledger、估计归 estimate。端到端测试即各层契约的集成验收。
2. `IngestClaude` 要求 turns 时间升序（append-only 事件流的 seq 序必须与
   时间序一致——replayer 的硬前提）；乱序显式报错。
3. 入账的 `ChargeEvent`：dims 原始物理量 + 按当时 θ 的 bucket_deltas +
   theta_version（θ 重估后 Recompute 重放重算，存量只作历史对照）。
4. 报告的可信区间一律来自离线后验（在线 MLE 只作众数/warm-start 起点）。

## 验证

- `go test -race ./internal/audit/...`
- 端到端验收：合成请求流 + 整数百分比观测 → C 的 90% 区间覆盖真值 →
  gauge 读数给出等价 API 美元 → 归因 = 量化噪声
