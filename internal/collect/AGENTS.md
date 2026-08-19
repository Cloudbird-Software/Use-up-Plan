# internal/collect

观测采集三通道（Intent §5.1，优先级不可颠倒）：响应头/usage 字段（主）→
usage endpoint 轮询（次）→ 网页 DOM（末）。

## 职责

- `claudejsonl.go`：Claude Code 会话日志（`~/.claude/projects/**/*.jsonl`）解析
  ——主通道的本地日志形态，精确 token 账本，ledger 校验的基准。
  产出 `ClaudeTurn`（原始物理量），供 audit/入账层转成 ChargeEvent。
- （后续 PR）headers 声明式解析、usage endpoint 轮询、web 层。

## 不变量（违反 = bug）

1. `ClaudeTurn.Dims` 只存原始物理量（真实 token 数），绝不存已加权结果——
   重放的前提（Intent §3.3）。
2. JSON 语法坏行 / assistant 行缺时间戳 → 报错，绝不静默跳过：半份账本
   比没有账本更危险（会得出错误的外生消耗结论）。
3. sidechain（subagent）消息保留——它们同样是真实计费请求。
4. usage 全零的 assistant 记录跳过：无计量信息的空事件只会膨胀事件流。
5. 解析器不做扣减计算、不写事件库——分层保证可独立 fuzz（T-04）。

## 验证

- `go test -race ./internal/collect/...`
- fuzz 种子：`go test ./internal/collect -fuzz FuzzParseClaudeJSONL -fuzztime 10s`
