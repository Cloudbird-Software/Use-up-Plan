# internal/estimate

参数辨识（Intent §4）：把「厂商计量不可信」落实为可计算的似然面与估计器。

## 职责

- `Dataset`：从事件流提取观测点（pct/exact/wall 三类），`Predict(θ)` 用
  Recompute + LinearEV 重放产出与观测一一对应的预测值 μ
- `likelihood.go`：量化似然（Intent §4.1 原式）、精确计数似然、撞墙似然、
  先验对数密度——全部数值稳定（尾区 logΦ 渐近展开、logDiffExp 消去保护）
- `problem.go`：`ParamSpace`（物理 θ ↔ 无界 z 的变换：logit/exp/恒等）+
  `Problem`（负对数后验 + 数值梯度）
- `online.go`：`Estimate`——L-BFGS + MoreThuente 强 Wolfe 的在线点估计
- `offline.go`：`SamplePosterior`——自适应随机游走 Metropolis-Hastings 的
  离线全量后验（z 空间采样，预热期学样本协方差 + Robbins-Monro 步长，
  采样期 proposal 冻结保证正确平稳分布）；`ParamUpdates` 产出 reason=offline
  的 ParamUpdateEvent——与在线估计写同一事件流（Intent §4.6）
- `select.go`：`SelectStructure`——窗口 kind 的 BIC 结构模型选择
  （Intent §4.3：枚举候选 → 同数据集拟合 → logL_hat - ½k·ln n 打分 →
  softmax 后验）；`StructEvents` 产出 StructureUpdateEvent。类别型参数
  （discrete + categories）不进数值自由空间——它们是结构未知量，走
  选择/判别式路径，PriorLogProb 对其恒 -Inf

## 不变量（违反 = bug）

1. `Predict` 的 mus 与 `Obs` 下标一一对应（按事件 seq 对齐），长度必须相等。
2. 参数变换往返安全：`ToZ∘FromZ` 恒等（贴界值钳回界内，见
   TestParamSpaceRoundTrip）。
3. `Predict` 失败（θ 非法区）时 NLL 返回 +Inf 而非 error——优化器需要
   连续目标面，非法点的统一语义是「无限差」。
4. 估计用重放口径恒为 LinearEV（ceil 量化不可导）；记账核对才用 Exact。
5. 线搜索卡死（ErrLinesearcherFailure / ErrNoProgress）降级返回至今最优点
   而非报错——窄谷是量化似然的固有形状，warm-start 下轮继续 refine。
6. 结构选择打分只用纯似然（`Result.LogLikelihood`，不含先验）：结构候选
   共享同一参数先验，计入会重复；候选按 KindCandidates 声明序遍历 +
   softmax 用 log-sum-exp——逐位可复现。
7. `cloneSpecWithKind` 必须复制桶切片再改 KindPosterior——Window 是桶
   结构体的值字段，原地改会静默污染调用方的 spec。

## 已知数值特性（不是 bug）

- 量化观测的 MLE 偏向量化区间中心（±s/2 内无差异）：联合后验的谷是平的，
  单参数恢复精度 ~量化步长/√N_obs，测试容差由此定（C 恢复 12%）。
- 正参数经 exp 变换后 z 空间条件数大（先验宽、似然窄），L-BFGS 收敛
  末期慢是形状问题：FunctionConverge（1e-8 × 10 代）负责优雅早停。

## 验证

- `go test -race ./internal/estimate/...`
- 合成恢复：真值生成事件流 → 偏差初值估计 → 参数回到真值容差内
  （容量 12%、flat/weight 25%——量化信息量的物理极限附近）
- warm-start：结果精度与后验不劣化、求值次数 ≤2× 冷启动
