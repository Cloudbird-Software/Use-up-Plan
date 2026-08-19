# internal/ledger

事件溯源（Intent §3.3）：append-only 事件流是唯一事实源，一切状态可重建。

## 职责

- 六种事件类型（charge / observation / wall_hit / reset_observed / param_update /
  structure_update）+ JSONL 信封（seq + ts + type + 六选一负载）
- `Store` 深接口 + `JSONLStore` 文件实现（AR-7：JSONL 起步，SQL 后置 Phase 3+）
- 存储边界统一做负载校验与凭证脱敏（`Sanitize`：error_body 等入库前替换）

## 不变量（违反 = bug）

1. `ChargeEvent.Dims` 只存原始物理量（真实 token 数），绝不存已加权结果——重放的前提。
2. `ChargeEvent.BucketDeltas` 与 `ThetaVersion` 配对：θ 重估后可用新 θ 重放旧请求流。
3. `WallHitEvent.LedgerSnapshot` 必须非空——它是 Σwx=C 的方程，最高价值辨识数据。
4. 事件只追加不修改；序号严格递增（进程内连续）。
5. JSONL 尾部残行（崩溃半写）在打开时截断丢弃；文件中间坏行必须报错，不静默跳过。
6. 脱敏不改调用方原值（拷贝后替换），规则只命中高置信度凭证形态。

## 验证

- `go test -race ./internal/ledger/...`
- 往返：Append 后 Iterate 逐事件等价（六种事件全覆盖）
- 并发：多 goroutine Append，序号无重复无回退
- 恢复：重开库续号正确；残行截断后文件可继续追加
