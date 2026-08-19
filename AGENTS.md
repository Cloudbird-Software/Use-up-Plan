# AGENTS.md（索引型——只放不可推断的约束，细节按需读索引）

## 命令

- `make setup` 安装 / `make check` 提交前必跑（lint+arch+test）/ `make test <文件>` 单测

## 硬规则（违反 = PR 打回）

1. 认证：一切 push/PR 用 cloudbrid-agent App 令牌，禁个人 PAT。获取（脚本 pin 到已审阅提交，
   升级先比对 .github main 再换 SHA——禁 `curl|bash` 浮动 main 指针，ADR-0021）：
   `GH_TOKEN=$(REPO=template-service bash <(curl -sS https://raw.githubusercontent.com/Cloudbird-Software/.github/487fd930c46a86bf3fb6865a7223287f8e3446e2/scripts/gh-app-token.sh))`
2. 不改 `.github/workflows/**`、`Makefile` 的 check 目标（App 无此权限，人类专属）
3. 新依赖先报"名称/用途/许可证/标准库可否替代"等人批；禁 AGPL/GPL-3.0/SSPL
4. 密钥、客户名、连接串不进仓库，用 `.env.example` 占位
5. 一个 PR 一件事，diff < 400 行；bug 修复先写复现失败测试
6. 对外接口变更写 CHANGELOG.md；提交信息用 Conventional Commits

## 索引（用到再读，不要全读）

| 场景                | 读这个                                                                                                                       |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| 建模块 / 动模块边界 | [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)                                                                                 |
| 选语言 / 选库       | [governance/policy/languages.yaml](https://github.com/Cloudbird-Software/.github/blob/main/governance/policy/languages.yaml) |
| 写测试 / 上新测试   | [governance/policy/testing.yaml](https://github.com/Cloudbird-Software/.github/blob/main/governance/policy/testing.yaml)     |
| 治理措施总清单      | [governance/GOVERNANCE.yaml](https://github.com/Cloudbird-Software/.github/blob/main/governance/GOVERNANCE.yaml)             |
| 模块内工作          | 该模块目录下的 AGENTS.md                                                                                                     |
