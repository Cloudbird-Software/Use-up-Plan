# Use-up-Plan — LLM 订阅额度智能调度与真实份额审计系统

LLM 多 plan 额度管理与路由调度工具：额度审计、规则标准化描述、最优分配求解、
实时切换（项目定位见 [AGENTS.md](AGENTS.md) 与 [Intent.md](Intent.md)；
立项申报 [ADR-0024](https://github.com/Cloudbird-Software/archive/blob/main/adr/ADR-0024-use-up-plan-bootstrap-onboarding.md)）。

- **语言**：Go（应用层默认准入语言，[ADR-0028](https://github.com/Cloudbird-Software/archive/blob/main/adr/ADR-0028-use-up-plan-go-language-baseline.md)——TS 脚手架已移除，govulncheck 接入）
- **当前阶段**：骨架+核心模块迭代中（路线见 [docs/ROADMAP.md](docs/ROADMAP.md)）

## 快速开始

```bash
make setup   # 安装依赖
make check   # 提交前必须全绿
```

## 架构

见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

> 历史注记：本 README 此前承载 .github#83 P1-2 thread-resolution 死锁消除的
> 端到端测试载体（T1），该测试已随死锁修复合入完成使命（ADR-0031）。
