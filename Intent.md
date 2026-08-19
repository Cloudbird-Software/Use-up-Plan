这是我的问题：这是我之前的问题: 我是重度ai用户，大量用ai进行开发、文字内容生成。为了节省成本，我对于模型本身进行了分级，期待能够接入不同的plan，以及包括一些做活动有免费额度的plan。不同的plan,规则不同，其额度一般都是有各种限制，过期清除。所以，需要在用量之下，有效规划，从而尽可能用好这些额度。并且，这还涉及到某种标准化测试。比如说，发出特定的全新的请求，确保没有缓存命中，计算api的输入输出，从而确定plan的真实额度情况审计。因为这些额度不一定每一个厂家都是愿意把它公开出来的。厂家可能也会用各种方法去想办法模糊这个中间的计算关系，从而让人搞不清楚它的真实价值。
一般来说，这些信息都在这些公司的网页页面里面。我也不可能天天自己去翻，所以还需要在有登录态以后，存放登录态，定期去检查重要的信息。
我个人也可能用2-3台自己手机号注册账号，因此也需要能够切换登录态。
然后根据不同公司制定的规则不一样，我们一方面是要去验证他声称的规则本身是不是真实，而且还要定期去检查有没有漂移。第2个，在确定了这些规则之后，我们要把它用一种方法进行标准化描述，进而在标准化描述之后，可以确定性求解其最佳的使用方法。这样就可以明白应该把它分配给什么样的工作，以及分配到哪个档位。而且本身这个API的路由也能够根据整个剩余用量以及相关的日期就能去进行灵活的调度。
最后呢，对于一些大量的在网上那种有免费额度的这些API,它一般来讲都会有某些限制，所以都有可能请求到中间，这个达到限制用量，这时候就断开了。所以这些免费档的本身我也会去做一些断开也无所谓的一些工作，只要到时候能够进行实时的瞬间切换就可以。
所以我的问题是，首先这样的一个东西现在有没有直接完全满足我需要的现成的？如果没有现成的，那么从技术角度来讲，目前整个这个东西最主要的难点是什么，是不是用现在现有的开源生态完全可以解决？而且的话，基本上可能到时候我只需要去找到一些各种plan的网站。接着我可能给AI示范一遍，怎么去进入网站，然后怎么去找到相关结构，剩下的他就可以去保存一个登录态，他自己的话，后续就可以自己再去登录了呢？
最后呢我也觉得可以根据完全用尽一个计划来估算这个计划它的真实的价值，进而可以用这个真实价值反过来重新评估一个模型的单位成本。最后呢，可以把这个和类似于像artificial analysis这样的一些网站上面的信息去进行比对。因为在这些网站上面，他用的都是API价格，但实际上真正的成本并不是API价格，而是应该去算把这个plan用尽以后的成本。这样子一来，我们就会知道真正的这种单位智能的价格。以下是你刚刚的回答: I'll research the current landscape before answering — there are several fast-moving pieces here (gateways, browser-agent auth, quota semantics).Let me dig into the subscription-plan/quota-tracking side and the browser-agent side.Now let me check the browser-automation/credential-persistence layer and the "self-healing scraper" state of the art.# 一、有没有现成的？没有。但有四块可复用的碎片

你描述的是一个完整系统，市面上存在的是它的四个互不相通的切片：

| 层 | 现成项目 | 覆盖你的需求 |
|---|---|---|
| 网关/路由/failover | LiteLLM、Bifrost、llmgateway.io、new-api/one-api | ✅ 基本够用 |
| 订阅额度读取 | ccusage、cc-usage-statusline、ai-usage-monitors | ⚠️ 只覆盖 Claude Code / Codex |
| 多账号轮换 | teamclaude、CC-Router、n9router 的 Token Rotate | ⚠️ 只覆盖单一厂商，无跨厂商统一 |
| 登录态持有 | Playwright storageState / persistent context、Stagehand、Skyvern | ✅ 机制成熟，但需自己搭 |

**最接近你整体构想的是 `n9router`**（9Router 的 fork）：它已经实现了「订阅档 → 便宜档 → 免费档」的分级 fallback、额度追踪、MITM 层多账号轮换。值得先读它的代码再决定自己写什么。

但**你需求里最核心的两样东西，没有任何现成项目做过**：

1. **额度规则的形式化建模 + 未知参数的实验辨识**（你说的"审计"）
2. **基于剩余额度与到期时间的最优分配求解**（你说的"确定性求解最佳使用方法"）

所有现有工具都把限额当**已知常量写死在 config 里**。你要做的是把它当**待估计的隐变量**。这个区别是本质的，也是这个项目真正的价值所在。

---

# 二、最主要的难点

我按难度排序，顺序和你直觉可能不同。

## 难点 1（最难）：额度的"计量单位"根本不可通约，且厂商故意不公开

这不是工程问题，是建模问题。看一下实际情况有多混乱：

- **Anthropic Claude Code**：5 小时滚动窗 + 7 天全局窗 + 按模型作用域的周窗（Opus / Sonnet / 新增的 Fable 各自独立），同一次请求会**同时扣减多个窗口**。官方 `/api/oauth/usage` 只返回 `percent`（整数），**绝对容量和 token→分数的加权系数完全不公开**。
- **OpenAI Codex**：连 usage endpoint 都没有。唯一办法是发一次最小推理请求，读 `x-codex-primary-used-percent` / `x-codex-secondary-*` 响应头。
- **免费档**：通常是 RPM/RPD/TPM/TPD 四维桶同时约束，硬断。
- **credits 制**：credits 与 token 的换算率往往动态、不公开。

所以你的 schema 必须能表达：资源维度（requests / tokens / credits / 内部加权分数 / 会话数）、窗口类型（日历固定窗 / 滚动窗 / 会话窗 / 一次性到期）、重置语义（清零 vs rollover）、作用域（账号 / key / 模型 / 模型族）、**多窗口并发扣减**、权重函数（Opus 算几倍？cache read 算 0.1 倍？）、溢出行为（硬断 / 降级 / 转按量 / 排队）。

**关键设计决策：把权重系数和窗口容量建模为带置信区间的可学习参数，而不是常量。** 这一条决定了整个系统的架构。

## 难点 2（技术含量最高，完全无现成方案）：在受量化的观测下、用受限预算做参数辨识

你说的「发全新请求、算 input/output、确定真实额度」，实际难度远超"数 token"。这是一个**带预算约束的系统辨识 / 最优实验设计**问题。

**核心障碍是观测量化**：官方只报整数百分比。你发一个请求，percent 从 39% 变成 39%——什么信息都没有。单次差分的信噪比极差。

几个有效的技术路径：

**(a) 用最大似然而非差分。** 设未知参数向量 $w$（各模型、各 token 类型的权重）和容量 $C$。每次探针给出一个观测 $\lfloor 100 \cdot \sum w_i x_i / C \rfloor$。把量化当作观测模型的一部分，对大量小探针做 MLE，而不是试图从单次差分反解。

**(b) 临界点实验的信息量远大于中间区域。** 跑到 99% 后用一系列已知大小的小请求逼近边界，观察何时翻到 100% / 触发硬断。**一个临界点观测的 Fisher 信息可能相当于几百个中间区域观测。**

**(c) 整数关系吸附。** 厂商内部系数通常是整齐的数（1、5、0.1、0.25）。用 PSLQ/LLL 类整数关系检测把浮点估计吸附到整齐分数上，可解释性和精度都大幅提升。

**(d) 审计成本≈0 的窍门（这是我认为最漂亮的设计点）：只在周期末尾用即将过期清零的残余额度做探针。** 那部分额度的边际成本本来就是零。这一条让"持续审计"从奢侈品变成免费品，应该作为架构的一等公民而非事后优化。

**(e) 漂移检测**：对 $(w, C)$ 的估计做时间序列，用 CUSUM 或 Page-Hinkley 变点检测。厂商偷改系数时告警。网页规则页做**语义 diff**（提取数字后比较）而非文本 diff。

## 难点 3（理论成熟，但无 LLM 领域实现）：在线分配应该用影子价格，不要用静态优先级

你说"确定性求解最佳使用方法"——但真实场景是**在线随机**的：你不知道未来会来什么任务。纯静态的多维背包解会在第一个任务到来时就失效。

正确框架是收益管理里的 **bid-price control**（航空业用了几十年）：

- 给每个额度桶算一个**影子价格**（LP 对偶变量），随剩余额度和剩余时间动态更新。
- 路由决策 = 在满足任务质量/可靠性要求的桶中，选影子价格最低的。
- 每小时跑一次小 LP（几十个桶 × 几十个任务类，CP-SAT / HiGHS 秒级），路由器只查表，延迟可忽略。

**这个框架自然产生你想要的行为**：快过期且用不完的额度，影子价格趋近 0，系统自动倾销；紧俏高价值额度影子价格高，只给高价值任务。你不需要手写"周期末清库存"的规则逻辑，它是对偶解的自然结果。

## 难点 4（生态成熟，最容易）：网关与瞬时切换

你的"免费档断了就瞬切"需求，LiteLLM 现成支持：`allowed_fails: 3` + `cooldown_time: 86400`（配额型失败冷却 24 小时）+ 有序 `fallbacks` 链。Bifrost 若在意延迟（Go，微秒级开销 vs Python 的毫秒级）。这一层不用自己写。

## 难点 5（最容易被低估）：ToS 风险必须进入你的价值模型

多账号轮换、把订阅 OAuth token 用于代理分发，**明确违反多数厂商 ToS**。Anthropic 官方文档直接写了：第三方工具应使用 API key，不应使用订阅计划的 OAuth token。后果是封号 + 额度全损 + 可能连带同支付方式的其他账号。

我的建议不是"别做"，而是：**把封号概率作为一个显式折损系数写进价值模型**。否则你算出的"真实价值"是系统性高估的。一个 $200/月的额度，若有 15% 月封禁概率，其风险调整后价值和一个 $170/月的稳定额度是不同的量级——尤其考虑到封号会同时摧毁你在该账号上积累的所有辨识参数。

## 难点 6（安全，容易被忽略）

这个系统会成为**单点：一处持有你全部厂商的凭证 + 邮箱访问权**。注意 LiteLLM 在 2026 年 3 月发生过确认的供应链攻击（PyPI 上分发带凭证窃取 payload 的后门版本）。务必：锁定版本 + 校验哈希、凭证独立加密存储（不要明文 JSON）、网关不暴露公网、专用邮箱与主邮箱隔离。

---

# 三、"示范一次，之后 AI 自己登录" —— 部分可行，但有个硬约束

先说能做到的部分（这些是成熟的）：

- **登录态持久化**：Playwright `storageState()` 可存 cookies + localStorage + IndexedDB + passkey(WebAuthn)；`persistent context` + `userDataDir` 保留完整 profile。Playwright MCP 默认就持久化登录态。
- **示范一次转成脚本**：Stagehand 的 act→cache、Skyvern 的 workflow、Playwright codegen 都能做。
- **最佳实践是双层**：先跑确定性脚本（快、免费、稳定），失败才唤起视觉 agent 重新定位元素并**回写更新脚本**。这个"确定性优先 + agent 自愈"模式确实能把维护成本降一个数量级。

**但"它自己后续能再登录"这个假设通常不成立**，原因：

1. **登录几乎都需要 OTP 或 magic link。** Claude 甚至根本没有密码（只有邮件 magic link）。要自动化就必须让系统能读你的邮箱/短信——这是很大的安全面扩张。可行的收窄方案：专用邮箱 + IMAP 只读 + 只暴露"提取验证码"这一个窄接口；短信用 Android 转发 app 或二次卡。
2. **Cloudflare / Turnstile / 设备指纹**：会话在新 IP 或新指纹下会被挑战。必须**一账号一固定 persistent profile + 一固定出口 IP**，绝不共用。共用是被封的最快路径。
3. **优先用 refresh token 而非 cookie**。OAuth refresh token 生命周期通常远长于 web session，且刷新是官方支持的路径（ccusage 就是用 `refreshToken` 自己刷新并回写 `.credentials.json`，与 Claude Code 保持同步）。

## 一个我认为你应该重新排序的判断

**响应头是被严重低估的信息源，应该优先做到极致，网页爬取降级为补充。**

几乎所有 provider 都在每次真实请求的响应头里免费附带了额度状态：
- Anthropic: `/api/oauth/usage`（OAuth）
- OpenAI Codex: `x-codex-primary-used-percent` / `x-codex-primary-reset-at`
- Groq: 每次响应都带 rate-limit 头，**不必等 429**
- OpenRouter: `/api/v1/key`
- 绝大多数 OpenAI 兼容 API: `x-ratelimit-*`
- LiteLLM 自身: `x-litellm-key-remaining-requests-{model}` / `-tokens-`

这些是**零额外请求、零成本、实时**的。而网页爬取是高维护、高风险、有延迟的。所以正确的信息架构是：

```
主通道：响应头 + usage 字段（每次请求免费搭载）→ 状态估计器
次通道：官方 usage endpoint / OAuth（低频轮询，几分钟一次）
末通道：网页爬取（每天 1 次，只取拿不到的：档位定义、活动规则、余额美元数）
```

把网页爬取放在主位是把最脆弱的环节放在了关键路径上。

---

# 四、"用尽额度反推真实单位成本" —— 思路对，但有三个会误导你的偏差

这个想法很有价值，Artificial Analysis 用 list price 确实系统性偏离真实成本。但直接算「月费 ÷ 用尽的 token 数」会得到一个**乐观且不可比**的数字。

**偏差 1：多维窗口下"用尽"不唯一。**
Claude 的 5h 窗和周窗，你很可能周窗还剩 40% 但因作息永远吃不完每个 5h 窗。所以真实分母不是理论额度，而是**你的实际使用模式下可达的额度**。建议同时跟踪三个数：

- `理论单位成本 = 月费 / 理论最大 token`（乐观上界，用于对比厂商宣传）
- `实际单位成本 = 月费 / 你实际消耗 token`（决策用的数）
- `利用率 = 实际 / 理论`

**利用率往往只有 30–60%。这个数字本身就是你整个项目最重要的发现**，而且它是你能通过调度直接改善的那一项——比换厂商的收益大得多。

**偏差 2：token 不可通约，$/Mtok 是有偏指标。**
不同 tokenizer 的 token 密度差异可达 20–30%（同样一段中文，Gemini 和 Claude 的 token 数不同）。直接比 $/Mtok 在跨厂商时是错的。

**建议主指标改为 `$/成功完成的标准任务`，或退一步用 `$/千字符输出`，token 只作辅助。** 这天然规避了计量口径问题，而且更接近你真正关心的东西。

**偏差 3：质量不是常量，订阅档常有隐藏降级。**
上下文窗口被砍、路由到量化版本、高峰期降级、thinking budget 被压缩——这些都不会写在页面上。所以必须把"每 token 的质量"纳入：维护一个 **20–50 题的私有 eval 集**（用你自己真实工作的代表性任务，带自动判分），定期在每个 plan/档位上跑，得到质量分。

最终指标：**单位有效智能成本 = 实际单位成本 / 质量分**

**与 AA 对比的正确口径**：不要直接比绝对值（口径不同必然对不上）。算 **`折扣率 = 你的实际单位成本 / AA 的 list 价`**。这个比率跨模型可比，也是最有说服力的输出形式——"订阅档相对 API 报价的实际折扣是 4.2×，但风险调整后只有 3.1×"。

**还要补一层风险调整**：免费档/活动档必须打可靠性折扣。一个随时会断的额度，对可中断任务价值≈1，对不可中断任务价值≈0。所以"真实价值"本质上是**按任务类条件化的价值向量，不是单一标量**。这正好和第三层的影子价格接上——影子价格就是这个向量的运行时体现。

---

# 五、建议的分阶段路线

技术上现有开源生态**能提供全部底层组件**（LiteLLM/Bifrost + Playwright + Stagehand + OR-Tools/HiGHS + Postgres/TimescaleDB + Temporal/Prefect），缺的只有中间那层"额度语义 + 辨识器 + 影子价格调度器"，量级大约 3000–8000 行，**难度在设计不在代码量**。

我现在的回复是: 1. 不用考虑我的特殊工作习惯问题，我有swarm的自动任务调度，任务是根本做不完的，额度一定能用尽。而且任务自动就会有分级。2. 我不是说自动帮我登录，而是我一次性登录以后，持久登录态。3. 因为我的使用量极其大，所以丝毫不担心会有项目开支无法覆盖节省的token成本问题。4. 这里确实，你说得对，核心是整个计算额度的语义。因此，我期待你为我设计一整个完整的、能够覆盖绝大多数情况的语义，用来描述整个的plan情况。 现在，我希望你为我设计这里面的本体论、schema, 语义和系统契约，明确说明有哪些值得用的开源项目，具体怎么开发。总之，从现在开始往后该如何全量开发出来，你能做到哪个程度就做到哪个程度，全部把工作做完输出给我，不要怕信息长消耗token多，明白了吗？

这是AI的回答：
---

# §0 你的澄清带来的三个结构性简化

## 0.1 「需求无限、必然打满」消灭了在线随机优化

我上一轮建议的 bid-price control 是为「不确定未来会不会用完」设计的。你有 swarm、任务做不完、额度一定用尽 —— 那么：

- 「快过期要倾销」的逻辑不需要了，你天然always在倾销。
- 影子价格不再需要预测未来到达流，退化为**当前周期的静态分配对偶价**。
- 优化目标从「期望收益最大化」变成**确定性的多维装箱/指派问题**：给定这一周期各桶的容量向量和各任务类的资源消耗向量，最大化总价值。

这从随机动态规划降级为一个每小时重解一次的中等规模 LP。**难度下降了一个数量级。**

## 0.2 「必然打满」让参数辨识几乎免费

上一轮我说最贵的是「跑到边界的临界点实验」。但你每个周期都会自然撞到每个桶的边界 —— 也就是说：

> **你每周期会免费获得每个桶的一个精确边界观测：`Σ w·x = C`。**

这是辨识里信息量最大的观测类型。你不需要设计昂贵的主动探针，只需要**把撞墙那一刻的完整累积消耗账本记录下来**。主动探针从「核心机制」降级为「边界附近的细化工具」（在 97%→100% 之间用小请求做二分，只为提高 C 的精度）。

架构含义：**Ledger（消耗账本）的完整性与精度，比探针系统重要得多。** 你所有的审计能力都来自「能不能把撞墙前的每一次请求的每一维消耗量精确记下来」。这是第一优先级的工程投入。

## 0.3 一个我必须先讲的数学事实：只观测百分比时，参数不可辨识

这个会决定你整个 schema 的设计，所以放在最前面。

Anthropic 只给你 `utilization: 37.0`。观测模型是：

$$y = \text{quantize}\left(100 \cdot \frac{\sum_d w_d x_d}{C}\right)$$

对任意 $\lambda>0$，参数 $(\lambda w, \lambda C)$ 给出完全相同的观测。**权重 $w$ 和容量 $C$ 只能辨识到一个公共尺度因子。** 无论你做多少实验、跑多少次撞墙，这个自由度都消不掉。硬断点也不行：撞墙只告诉你 $\sum w x = C$，仍是比值。

这不是坏消息，是好消息。因为它逼出了唯一自然的规范化选择，而这个选择恰好就是你想要的经济量：

> **把 $w$ 锚定到该厂商官方按量 API 的价目表（每维美元单价），于是 $C$ 的单位自动变成「等价 API 美元」。**

也就是说：**可辨识性分析的结论直接产出「把这个 plan 用尽等于买了多少美元的 API」。** 你要的「真实价值」不是事后算出来的，它就是辨识问题的规范化常数。

更妙的是这给了你一个**可检验假设**：如果厂商订阅桶的内部权重确实与 API 价目表成比例，那么用 API 价格作为 $w$ 拟合后残差应该很小。如果残差系统性偏离（比如 Opus 在订阅桶里的相对惩罚比 API 价差更狠、或 cache_read 折扣在订阅里不给），**残差本身就是「厂商模糊计价关系」的定量证据**，而且它精确告出偏离了多少倍。这是你审计的核心输出物之一。

我把这个叫 **calibration gauge（标度规范）**，它是 schema 里的一等公民。

## 0.4 一个立刻可变现的推论：结构套利 >> 价格套利

因为 charge rule 的**结构**在厂商之间根本不同：

| 桶的计量方式 | 典型厂商 | 一个「大上下文、长输出」任务的成本 | 一个「短问短答」任务的成本 |
|---|---|---|---|
| `per_request` 计 1 次 | GLM / Qwen / MiniMax / Kimi 的 prompt 配额 | **几乎免费**（一次就是一次） | 昂贵（浪费一整个配额单位） |
| `per_token` 加权 | Anthropic / Codex 的不透明分、credits 制 | 昂贵 | 便宜 |

所以：

- **把重上下文、大输出、长 agent 轨迹丢进 per-request 计量的桶** —— 你在那里的边际 token 成本是 0。
- **把高频短请求丢进 per-token 计量的桶** —— 你在那里的边际请求成本是 0。
- **prompt caching 的价值在 per-token 桶里极高（cache_read 常按 0.1× 计），在 per-request 桶里恰好为零**。所以缓存策略必须按桶类型条件化，不能全局开。

这一条是纯结构性的、确定的、与价格无关的收益。**我估计它带来的节省超过「换一个更便宜的厂商」。** 而它只有在你把 charge rule 形式化之后才看得见 —— 这就是做 ontology 的直接回报。

同时它带来一个后果：per-request 桶的「等价美元价值」**内生依赖于你往里塞什么**。所以你不能先独立算出每个 plan 的价值再排序，必须**价值评估与分配决策联合求解**。这在 §6 的 LP 里自然处理。

---

# §1 本体论（Ontology）

## 1.1 实体层次

```
Vendor ─┬─ PlanCatalog        (厂商声称的规则，版本化，可漂移)
        └─ RateCard           (官方按量价目表 → calibration gauge)

Account ─┬─ Credential*       (oauth / api_key / cookie-session)
         ├─ Subscription      (account × plan × 计费周期实例)
         └─ IdentityBinding   (browser profile, egress IP, phone, email)

Subscription ─┬─ Grant*        (基础额度 / 活动赠送 / 加购 / rollover)
              └─ Channel*      (CLI 通道 / OpenAI 兼容通道 / web)

Channel ─┬─ ModelBinding*      (哪些模型可用、别名、能力开关)
         └─ AdmissionPolicy    (准入约束：非累积)

Bucket  ─┬─ Window            (窗口语义)
         ├─ ChargeRule        (扣减函数，含未知参数)
         ├─ Scope             (挂在谁身上：account/model/family/pool)
         ├─ ObservabilityBinding*  (怎么读到状态)
         └─ OverflowPolicy    (满了之后怎样)

Parameter*  (每个未知数：prior / posterior / 来源 / 置信区间 / 漂移状态)
Observation* (每一次读数，带来源、时刻、量化精度)
ChargeEvent* (每一次请求的每一维消耗，append-only)
```

一次请求命中的是 **BucketSet**（多个桶同时扣减），不是单个桶。这是 schema 的中心事实。

## 1.2 关键区分：结构未知 vs 数值未知

这是我这轮最想强调的概念升级。

| | 例子 | 处理方法 |
|---|---|---|
| **数值未知** | 容量 $C$、权重 $w_d$、模型倍率 | 参数估计（MLE / 贝叶斯） |
| **结构未知** | 5h 窗到底是 anchored-tumbling 还是 true-sliding？周窗是 rolling 还是账号固定重置？桶是 per-model 还是共享池？cache_read 是否单独计维？ | **模型选择**（枚举候选结构，按边际似然 / BIC 打分） |

为什么这很重要：**结构决定了是否存在套利**，比数值精度值钱得多。

具体例子：Anthropic 的 5 小时窗。

- 若是 **anchored tumbling**（首次请求启动一个固定 5h 窗，到期整体清零）→ 存在**窗口边界套利**：在旧窗末尾提交大任务，窗口立即翻新，等于在几分钟内获得两个窗的额度。
- 若是 **true sliding**（每笔消耗各自在 5h 后过期）→ 边界套利不存在。

我搜到的公开描述在这一点上是**互相矛盾的**（有的说 "rolling window renews 5 hours later from your first message"，这实际描述的是 anchored tumbling；有的直接叫 rolling）。这种矛盾恰好证明：**不能相信文档，必须把窗口语义当作待辨识的离散结构参数**。

好消息是这个结构极易辨识：观察 `resets_at` 时间戳序列 + `utilization` 的下降形状。anchored tumbling 表现为**整体断崖归零**且 `resets_at` 在窗口内保持恒定；true sliding 表现为**阶梯式衰减**且 `resets_at` 持续前移。几天的被动观测就能定论，零成本。

同理，周窗是「账号固定重置时刻」还是「rolling 7 天」：前者 `resets_at` 恒定到日历时刻，后者随使用前移。也是被动可辨识。

所以 schema 里每个 Window 都要有 `kind_candidates` 和 `kind_posterior`，而不是一个写死的 `kind`。

## 1.3 计量维度分类学（Dimension Taxonomy）

必须穷尽，因为漏一维就意味着这个厂商建不出模型。

**计数类（integer counting）**
- `requests` — HTTP 请求数
- `turns` — 用户轮次（**关键**：一个 turn 在 agentic 场景可能触发 20 个 requests。GLM/Qwen 说的「prompts」到底是哪一个，是必须辨识的高价值结构问题，倍率差 10~50×）
- `steps` — agent 步 / 工具调用轮
- `sessions` — 会话总数
- `tasks` — 云端任务数（Codex cloud）

**Token 类（必须细分，因为权重不同）**
- `input_tokens`（未缓存输入）
- `cache_write_tokens`（写缓存，常 1.25×）
- `cache_read_tokens`（读缓存，常 0.1×）
- `output_tokens`
- `reasoning_tokens`（**独立维**：常与 output 分开计价或分开计量）
- `total_tokens`（只在厂商只暴露这个聚合量时使用，标记为 derived）

**时间/算力类**
- `reasoning_seconds` — 真实存在的语义。Codex 社区实测把 5h 窗描述为「约 40 分钟推理时间」，说明其内部计量可能是时间而非 token，或时间是 token 的强代理。
- `wall_seconds`
- `gpu_seconds`

**货币类**
- `credits` — 厂商虚拟币（Codex 现在是 credits per Mtok 的费率卡）
- `currency_usd` — 真钱（overage / PAYG）

**多模态类**
- `images_in`, `image_pixels_in`, `images_out`
- `audio_seconds_in/out`, `video_seconds`
- `characters`（部分中文厂商按字符计价，与 token 不可通约）

**瞬时约束类（非累积！不是桶，是 admission）**
- `concurrency` — 并发请求数
- `context_tokens_peak` — 单次上下文上限
- `max_output_tokens`

**不透明类**
- `opaque_units` — 厂商内部加权分，**唯一可观测是百分比**。Anthropic 全部走这条。所有辨识工作的战场。

## 1.4 窗口语义分类学（Window Taxonomy）

```
TUMBLING_ANCHORED_ON_FIRST_USE   首次请求启动固定长度窗，到期整体归零   (Claude 5h?)
TUMBLING_ACCOUNT_ANCHORED        账号被分配固定重置时刻，周期性归零     (Claude 周窗 Pro/Max)
TUMBLING_CALENDAR                日历对齐 (UTC 00:00 / 账单日)          (多数免费档 RPD)
SLIDING_EXACT                    真滚动，每笔消耗独立过期               (Claude 周窗 Enterprise?)
TOKEN_BUCKET_CONTINUOUS          按速率连续补充 (r tokens/s, burst=B)   (多数 RPM/TPM 的真实实现)
BILLING_CYCLE                    跟随订阅计费周期
ONE_SHOT_EXPIRING                一次性额度 + 绝对过期时刻              (活动赠送 credits)
NEVER                            纯余额，不重置
```

**重要细节**：大多数 RPM/TPM 实际是 token bucket 而非离散窗口。区别在于**能否 burst**。token bucket 允许你攒满 burst 后瞬间打出去，离散窗口不允许。对你的 swarm 调度有直接影响（决定并发爬升曲线）。这也是被动可辨识的：发一串等间隔请求，看 429 出现的位置。

**重置语义**（与窗口类型正交）：
```
ZERO                  归零
REFILL_TO_FULL        补满
ROLLOVER_CAPPED       结转，上限 = k × 周期额度
ROLLOVER_UNCAPPED     无限结转
DECAY_EXPONENTIAL     按比例衰减
```

## 1.5 作用域（Scope）—— 共享池必须显式建模

桶挂在什么上：

```
account | organization | workspace | credential(key) | subscription
model | model_family | capability_class | effort_tier
channel | endpoint | feature
CROSS_PRODUCT_POOL        ← 最容易被漏掉、后果最严重
ip | device
```

`CROSS_PRODUCT_POOL` 必须建模，因为：

- Claude 的额度**在 Claude Code、claude.ai 网页、Desktop 之间共享**。你手动开个网页聊天就在偷 swarm 的额度。
- Codex 的额度池与 ChatGPT 的若干 agentic 功能共享。

这引出一个必须建模的噪声源：**`exogenous_drain`（外生消耗）** —— 该桶会被系统外的你本人消耗。后果：

1. Ledger 会出现「无法归因的消耗」，估计器必须容忍（把它建成一个带先验的隐变量，而不是当成模型误差）。
2. 需要检测：如果 observed_utilization 增长快于 ledger 预测，且差值不能由参数误差解释，报「外生消耗」告警。这同时是**厂商偷偷改系数**和**你自己手动用了**的鉴别问题 —— 靠时间模式区分（人类手动使用有 burst 和昼夜节律，系数漂移是持续偏置）。

## 1.6 扣减规则（ChargeRule）—— 设计为「分段线性 + 量化」

我建议**刻意限制表达力**，换取可求解性：

```
charge_b(request) =
    flat_b                                    # 每请求固定扣
  + m_b(model) · e_b(effort) · Σ_d  w_{b,d} · q_d(x_d)      # 加权 token 项
  , 然后 max(·, floor_b)                       # 每请求最低扣
  , 然后 q_b(·)                                # 桶级量化（向上取整到 1k 等）
```

其中：
- `q_d` 是维度级量化（ceil to 1000 tokens 之类）
- `m_b(model)` 模型倍率（GLM 的「高级模型消耗更多配额」）
- `e_b(effort)` 努力级倍率（thinking budget / reasoning effort）
- **所有系数都可以是 `ParamRef`（待估参数）而非常数**

这个形式覆盖我见过的所有真实计价规则，且**对 LP 是线性的**（`max` 和 `ceil` 在规划阶段用期望值线性近似，在实际扣减记账时用精确形式）。这个「规划用线性近似、记账用精确」的分离是关键工程决策，写进契约。

不支持的（刻意）：任意非线性、跨请求状态依赖（除了缓存命中，那个通过 `cache_read_tokens` 维显式表达）。如果真遇到，用**分段定义多个 charge rule + 条件选择**表达。

## 1.7 溢出瀑布（Overflow Waterfall）

OpenAI 自己把这套叫「waterfall」：rate limit → credits → PAYG。必须显式建模成有序列表：

```
overflow:
  - action: SPILL_TO_BUCKET     target: codex_credits
  - action: SPILL_TO_PAYG       target: openai_payg     guard: MUST_BE_EXPLICITLY_ENABLED
  - action: HARD_BLOCK
```

**`SPILL_TO_PAYG` 必须默认禁止并需要显式开关。** 这是防止一夜之间被计费几千美元的唯一保险。这个 guard 是安全契约，不是可选项。

其他 action：
```
HARD_BLOCK_429 | HARD_BLOCK_WITH_RESET_HINT
DEGRADE_MODEL(target) | DEGRADE_SPEED(factor) | TRUNCATE_CONTEXT
QUEUE(max_wait) | SILENT_QUALITY_DROP        ← 最阴险，只能靠 eval 检测
```

`SILENT_QUALITY_DROP` 必须在 ontology 里有位置，即使你无法直接观测它 —— 它的存在决定了你必须跑私有 eval（§11）。

## 1.8 可靠性与风险 profile

免费档的核心属性不是价格是**可中断性**。

```
reliability:
  interruption_hazard_per_hour: 0.15      # 单位时间被切断概率
  interruption_granularity: MID_STREAM | BETWEEN_REQUESTS
  resume_supported: false
  latency_p50_ms / p99_ms
  availability_slo: null

risk:
  tos_violation_class: NONE | GREY | EXPLICIT_BREACH
  ban_hazard_monthly: 0.05
  ban_blast_radius: [account_ids...]      # 连坐范围（同支付方式/同 IP）
  loss_on_ban: 已充值余额 + 已辨识参数资产
```

`ban_blast_radius` 很重要：多账号如果共用支付方式或出口 IP，封一个可能连坐全部。这个字段强制你显式面对拓扑，而不是含糊地「感觉风险不大」。

---

# §2 QDL：Quota Description Language

文件格式 `*.qdl.yaml`，用 Pydantic v2 校验。下面是可直接用的实现。

## 2.1 Pydantic 模型（核心，可直接落地）

```python
# qdl/model.py
from __future__ import annotations
from enum import Enum
from typing import Literal, Annotated
from datetime import datetime, timedelta
from pydantic import BaseModel, Field, model_validator

# ─────────────────────────── 维度 ───────────────────────────
class Dim(str, Enum):
    REQUESTS = "requests"; TURNS = "turns"; STEPS = "steps"
    SESSIONS = "sessions"; TASKS = "tasks"
    INPUT_TOKENS = "input_tokens"
    CACHE_WRITE_TOKENS = "cache_write_tokens"
    CACHE_READ_TOKENS = "cache_read_tokens"
    OUTPUT_TOKENS = "output_tokens"
    REASONING_TOKENS = "reasoning_tokens"
    TOTAL_TOKENS = "total_tokens"            # derived only
    REASONING_SECONDS = "reasoning_seconds"
    WALL_SECONDS = "wall_seconds"
    CREDITS = "credits"; CURRENCY_USD = "currency_usd"
    IMAGES_IN = "images_in"; AUDIO_SECONDS_IN = "audio_seconds_in"
    CHARACTERS = "characters"
    OPAQUE_UNITS = "opaque_units"

# 瞬时约束维（不进桶，进 admission）
class InstantDim(str, Enum):
    CONCURRENCY = "concurrency"
    CONTEXT_TOKENS_PEAK = "context_tokens_peak"
    MAX_OUTPUT_TOKENS = "max_output_tokens"
    MIN_INTERVAL_MS = "min_interval_ms"

# ─────────────────────── 参数（一等公民）───────────────────────
class Provenance(str, Enum):
    VENDOR_DOC = "vendor_doc"            # 厂商公开声称
    VENDOR_API = "vendor_api"            # 厂商 API 返回
    ESTIMATED = "estimated"              # 我们辨识出来的
    ASSUMED = "assumed"                  # 拍的
    GAUGE = "gauge"                      # 标度规范固定的

class Distribution(BaseModel):
    kind: Literal["lognormal","normal","uniform","point","discrete"]
    params: dict[str, float] | dict[str, list]

class Parameter(BaseModel):
    id: str                              # 全局唯一, e.g. "anthropic.max20.w5h.output"
    unit: str                            # "opaque_units_per_token" / "usd_per_Mtok"
    prior: Distribution
    posterior: Distribution | None = None
    provenance: Provenance = Provenance.ASSUMED
    bounds: tuple[float|None, float|None] = (0.0, None)
    # 整数关系吸附：期望它是这些"整齐值"之一
    snap_candidates: list[float] | None = None
    frozen: bool = False                 # gauge 固定的不再更新
    drift: DriftState | None = None

class DriftState(BaseModel):
    detector: Literal["cusum","page_hinkley","bocpd"] = "cusum"
    statistic: float = 0.0
    last_changepoint_at: datetime | None = None
    alarm: bool = False

ParamRef = Annotated[str, Field(description="Parameter.id")]
Coeff = float | ParamRef            # 常数 或 指向待估参数

# ─────────────────────────── 窗口 ───────────────────────────
class WindowKind(str, Enum):
    TUMBLING_ANCHORED_ON_FIRST_USE = "tumbling_anchored_on_first_use"
    TUMBLING_ACCOUNT_ANCHORED = "tumbling_account_anchored"
    TUMBLING_CALENDAR = "tumbling_calendar"
    SLIDING_EXACT = "sliding_exact"
    TOKEN_BUCKET_CONTINUOUS = "token_bucket_continuous"
    BILLING_CYCLE = "billing_cycle"
    ONE_SHOT_EXPIRING = "one_shot_expiring"
    NEVER = "never"

class ResetPolicy(str, Enum):
    ZERO="zero"; REFILL_TO_FULL="refill_to_full"
    ROLLOVER_CAPPED="rollover_capped"; ROLLOVER_UNCAPPED="rollover_uncapped"
    DECAY_EXPONENTIAL="decay_exponential"

class Window(BaseModel):
    # 结构未知 → 候选集 + 后验，而不是单值
    kind_candidates: list[WindowKind]
    kind_posterior: dict[str, float] | None = None   # kind -> prob
    @property
    def kind(self) -> WindowKind:                    # MAP 估计
        if self.kind_posterior:
            return WindowKind(max(self.kind_posterior, key=self.kind_posterior.get))
        return self.kind_candidates[0]

    length: timedelta | None = None                  # 窗长
    anchor_utc: str | None = None                    # "WED 20:00" 账号锚点
    calendar_align: Literal["utc_midnight","local_midnight","billing_day"] | None = None
    refill_rate: Coeff | None = None                 # token_bucket: 单位/秒
    burst: Coeff | None = None
    expires_at: datetime | None = None               # one_shot
    reset: ResetPolicy = ResetPolicy.ZERO
    rollover_cap_multiple: float | None = None

# ─────────────────────────── 作用域 ───────────────────────────
class Scope(BaseModel):
    level: Literal["account","organization","workspace","credential",
                   "subscription","cross_product_pool"]
    models: list[str] | None = None           # None = 全部
    model_families: list[str] | None = None
    effort_tiers: list[str] | None = None
    channels: list[str] | None = None
    endpoints: list[str] | None = None
    pool_id: str | None = None                # cross_product_pool 的共享标识
    shared_with_products: list[str] = []      # ["claude.ai","desktop","cowork"]

# ─────────────────────── 扣减规则 ───────────────────────
class Quantize(BaseModel):
    mode: Literal["none","ceil","floor","round"] = "none"
    step: float = 1.0

class Term(BaseModel):
    dim: Dim
    coeff: Coeff
    quantize: Quantize = Quantize()

class ChargeRule(BaseModel):
    flat: Coeff = 0.0                             # 每请求固定扣
    terms: list[Term] = []
    model_multiplier: dict[str, Coeff] = {}       # model_id / family -> mult
    effort_multiplier: dict[str, Coeff] = {}
    floor: Coeff = 0.0                            # 每请求最低扣
    quantize: Quantize = Quantize()               # 桶级量化
    # 规划期线性近似的说明（契约要求显式）
    linearization: Literal["exact_linear","expected_value","upper_bound"] = "exact_linear"

# ─────────────────────── 可观测性绑定 ───────────────────────
class Semantic(str, Enum):
    USED_PCT="used_pct"; REMAINING_PCT="remaining_pct"
    USED_ABS="used_abs"; REMAINING_ABS="remaining_abs"; LIMIT_ABS="limit_abs"
    RESET_AT_EPOCH_MS="reset_at_epoch_ms"; RESET_AT_ISO="reset_at_iso"
    RESET_AFTER_S="reset_after_s"; WINDOW_MINUTES="window_minutes"
    REASON="reason"; PLAN_TYPE="plan_type"

class Quantization(BaseModel):
    kind: Literal["exact","integer","decimals","unknown"] = "unknown"
    decimals: int | None = None

class ObsBinding(BaseModel):
    source: Literal["response_header","usage_endpoint","error_body",
                    "local_log","web_dom","sdk_field"]
    locator: str                 # header 名 / jsonpath / regex / css selector
    semantic: Semantic
    quantization: Quantization = Quantization()
    attribution_lag_s: float = 0.0
    trust: float = 1.0           # 0..1，观测噪声权重
    # usage_endpoint 专用
    url: str | None = None
    auth: Literal["oauth_bearer","api_key","cookie"] | None = None
    extra_headers: dict[str,str] = {}
    poll_interval_s: int | None = None

# ─────────────────────── 溢出 ───────────────────────
class OverflowStep(BaseModel):
    action: Literal["SPILL_TO_BUCKET","SPILL_TO_PAYG","HARD_BLOCK",
                    "DEGRADE_MODEL","DEGRADE_SPEED","TRUNCATE_CONTEXT",
                    "QUEUE","SILENT_QUALITY_DROP"]
    target: str | None = None
    factor: float | None = None
    max_wait_s: int | None = None
    requires_explicit_enable: bool = False   # PAYG 必须 True

# ─────────────────────────── 桶 ───────────────────────────
class Bucket(BaseModel):
    id: str
    unit: Dim                                 # 桶的计量单位
    capacity: Coeff                           # 常为 ParamRef（未知！）
    window: Window
    scope: Scope
    charge: ChargeRule
    observability: list[ObsBinding] = []
    overflow: list[OverflowStep] = [OverflowStep(action="HARD_BLOCK")]
    exogenous_drain: bool = False              # 会被系统外消耗
    exogenous_rate_param: ParamRef | None = None
    notes: str | None = None

# ─────────────────────── 准入约束 ───────────────────────
class AdmissionPolicy(BaseModel):
    limits: dict[InstantDim, Coeff] = {}
    allowed_models: list[str] | None = None
    denied_models: list[str] = []
    required_capabilities: list[str] = []      # tool_use / vision / caching
    forbidden_features: list[str] = []

# ─────────────────────── 通道 / 计划 ───────────────────────
class ModelBinding(BaseModel):
    logical_model: str            # 你的内部统一名 "tier1-reasoner"
    vendor_model_id: str
    family: str
    aliases: list[str] = []
    capabilities: list[str] = []
    tokenizer: str | None = None  # 用于跨厂商 token 归一
    context_window: int | None = None
    quality_scores: dict[str, float] = {}   # 你私有 eval 的结果

class Channel(BaseModel):
    id: str
    protocol: Literal["anthropic_messages","openai_chat","openai_responses",
                      "gemini","custom_cli","web"]
    base_url: str | None = None
    auth: Literal["oauth_bearer","api_key","cookie_session"]
    models: list[ModelBinding]
    admission: AdmissionPolicy = AdmissionPolicy()
    reliability: Reliability
    spoofing_required: SpoofingSpec | None = None   # 需要伪装成官方 CLI 才能用

class Reliability(BaseModel):
    interruption_hazard_per_hour: float = 0.0
    interruption_granularity: Literal["mid_stream","between_requests"] = "between_requests"
    resume_supported: bool = False
    latency_p50_ms: float | None = None
    latency_p99_ms: float | None = None

class SpoofingSpec(BaseModel):
    user_agent: str | None = None
    required_headers: dict[str,str] = {}
    system_prompt_prefix_required: bool = False
    tos_note: str = ""

class RiskProfile(BaseModel):
    tos_violation_class: Literal["NONE","GREY","EXPLICIT_BREACH"] = "NONE"
    ban_hazard_monthly: float = 0.0
    ban_blast_radius: list[str] = []
    prepaid_at_risk_usd: float = 0.0

class Grant(BaseModel):
    """一笔额度授予：基础/赠送/加购/结转"""
    id: str
    kind: Literal["base","promo","topup","rollover","referral"]
    bucket_id: str
    amount: Coeff
    granted_at: datetime | None = None
    expires_at: datetime | None = None
    conditions: str | None = None            # 自然语言原文，供 diff

class CalibrationGauge(BaseModel):
    """打破 (w, C) 尺度不可辨识性的规范化选择"""
    mode: Literal["anchor_to_vendor_ratecard",
                  "anchor_to_reference_model_usd",
                  "anchor_to_observed_absolute"] = "anchor_to_vendor_ratecard"
    ratecard_usd_per_unit: dict[Dim, float] = {}   # 官方 API 价目
    reference_model: str | None = None
    note: str = ""

class PlanSpec(BaseModel):
    id: str                                 # "anthropic/max20@2026-08"
    vendor: str
    plan_name: str
    price_usd_per_period: float
    period: Literal["month","year","week","one_off"]
    spec_version: str                       # 你的 QDL 版本号
    vendor_doc_snapshot_hash: str | None = None   # 页面语义 diff 用
    effective_from: datetime
    effective_until: datetime | None = None

    buckets: list[Bucket]
    channels: list[Channel]
    grants: list[Grant] = []
    parameters: list[Parameter]
    gauge: CalibrationGauge
    risk: RiskProfile = RiskProfile()

    @model_validator(mode="after")
    def _check_refs(self):
        ids = {p.id for p in self.parameters}
        # 遍历所有 Coeff，若为 str 必须在 ids 中（略：实际实现递归检查）
        return self
```

## 2.2 一个完整的 QDL 实例：Anthropic Max 20x

这个例子把「结构未知」「不透明单位」「共享池」「标度规范」全部体现出来。

```yaml
id: anthropic/max20@2026-08
vendor: anthropic
plan_name: Max 20x
price_usd_per_period: 200.0
period: month
spec_version: qdl/1.0
effective_from: 2026-08-01T00:00:00Z

gauge:
  mode: anchor_to_vendor_ratecard
  note: >
    opaque_units 无绝对单位。规范化：令 opaque_units 与官方 API 美元
    等价。即 w_input := ratecard_input_usd_per_token。于是所有 capacity
    参数的单位自动为「等价 API 美元」，直接给出 plan 用尽的真实价值。
    若拟合残差显著，说明订阅内部权重≠API价目比例，残差即厂商模糊定价的证据。
  ratecard_usd_per_unit:      # 需按当期官方价目表填，务必版本化
    input_tokens: 3.0e-6
    cache_write_tokens: 3.75e-6
    cache_read_tokens: 0.3e-6
    output_tokens: 15.0e-6

parameters:
  # ---- 5 小时窗容量（未知，单位=等价API美元）----
  - id: anthropic.max20.C_5h
    unit: usd_equivalent
    prior: {kind: lognormal, params: {mu: 2.7, sigma: 1.0}}   # 中位≈$15
    provenance: assumed
    bounds: [0.1, 500]
  # ---- 7 天全局窗容量 ----
  - id: anthropic.max20.C_7d
    unit: usd_equivalent
    prior: {kind: lognormal, params: {mu: 5.5, sigma: 1.0}}   # 中位≈$245
    provenance: assumed
  # ---- 7 天 Sonnet 专用窗 ----
  - id: anthropic.max20.C_7d_sonnet
    unit: usd_equivalent
    prior: {kind: lognormal, params: {mu: 5.2, sigma: 1.2}}
  # ---- 7 天 Opus 专用窗（可能为 null/不存在，见 kind_posterior）----
  - id: anthropic.max20.C_7d_opus
    unit: usd_equivalent
    prior: {kind: lognormal, params: {mu: 4.5, sigma: 1.3}}

  # ---- 模型倍率（相对 gauge 的偏离；1.0 = 与API价目一致）----
  - id: anthropic.max20.mult_opus
    unit: dimensionless
    prior: {kind: lognormal, params: {mu: 0.0, sigma: 0.4}}
    snap_candidates: [1.0, 1.25, 1.5, 2.0, 3.0, 5.0]
    provenance: assumed
  - id: anthropic.max20.mult_sonnet
    unit: dimensionless
    prior: {kind: point, params: {value: 1.0}}
    frozen: true          # 作为 gauge 的一部分固定，打破尺度自由度
  - id: anthropic.max20.mult_haiku
    unit: dimensionless
    prior: {kind: lognormal, params: {mu: 0.0, sigma: 0.5}}
    snap_candidates: [0.2, 0.25, 0.3, 1.0]

  # ---- cache_read 折扣是否在订阅桶里生效（高价值未知数）----
  - id: anthropic.max20.cache_read_ratio
    unit: dimensionless
    prior: {kind: uniform, params: {low: 0.05, high: 1.0}}
    snap_candidates: [0.1, 0.25, 0.5, 1.0]
    provenance: assumed
    # 若=0.1，缓存在订阅里同样极度划算；若=1.0，厂商取消了订阅内的缓存折扣

  # ---- 外生消耗率（你手动用网页/桌面端偷掉的额度）----
  - id: anthropic.max20.exo_rate_5h
    unit: usd_equivalent_per_hour
    prior: {kind: lognormal, params: {mu: -2.0, sigma: 1.5}}

buckets:
  - id: b_5h
    unit: opaque_units
    capacity: anthropic.max20.C_5h
    exogenous_drain: true
    exogenous_rate_param: anthropic.max20.exo_rate_5h
    window:
      # ★ 结构未知：这是 anchored tumbling 还是 true sliding？
      kind_candidates:
        - tumbling_anchored_on_first_use
        - sliding_exact
      kind_posterior: {tumbling_anchored_on_first_use: 0.6, sliding_exact: 0.4}
      length: PT5H
      reset: zero
    scope:
      level: cross_product_pool
      pool_id: anthropic_subscription_pool
      shared_with_products: ["claude_code", "claude.ai", "desktop", "cowork"]
    charge:
      terms:
        - {dim: input_tokens,       coeff: 3.0e-6}
        - {dim: cache_write_tokens, coeff: 3.75e-6}
        - {dim: cache_read_tokens,  coeff: anthropic.max20.cache_read_ratio}  # ×input价
        - {dim: output_tokens,      coeff: 15.0e-6}
        - {dim: reasoning_tokens,   coeff: 15.0e-6}
      model_multiplier:
        claude-opus-*:   anthropic.max20.mult_opus
        claude-sonnet-*: anthropic.max20.mult_sonnet
        claude-haiku-*:  anthropic.max20.mult_haiku
    observability:
      - source: usage_endpoint
        url: https://api.anthropic.com/api/oauth/usage
        auth: oauth_bearer
        extra_headers: {anthropic-beta: oauth-2025-04-20}
        locator: "$.five_hour.utilization"
        semantic: used_pct
        quantization: {kind: unknown}       # 观测到 37.0/26.0/1.0，疑似整数，需实测
        poll_interval_s: 180
        trust: 0.95
      - source: usage_endpoint
        url: https://api.anthropic.com/api/oauth/usage
        auth: oauth_bearer
        locator: "$.five_hour.resets_at"
        semantic: reset_at_iso
      - source: local_log
        locator: "~/.claude/projects/**/*.jsonl"
        semantic: used_abs               # 精确 token 账本，用于 ledger 校验
        trust: 1.0
    overflow:
      - action: SPILL_TO_BUCKET
        target: b_extra_credits
      - action: HARD_BLOCK_WITH_RESET_HINT

  - id: b_7d_all
    unit: opaque_units
    capacity: anthropic.max20.C_7d
    window:
      kind_candidates: [tumbling_account_anchored, sliding_exact]
      kind_posterior: {tumbling_account_anchored: 0.7, sliding_exact: 0.3}
      length: P7D
      anchor_utc: "UNKNOWN"       # 需从 resets_at 序列反推
      reset: zero
    scope: {level: cross_product_pool, pool_id: anthropic_subscription_pool}
    charge: {"$ref": "#/buckets/b_5h/charge"}    # 假设同一 charge rule（待检验！）
    observability:
      - source: usage_endpoint
        url: https://api.anthropic.com/api/oauth/usage
        locator: "$.seven_day.utilization"
        semantic: used_pct
        quantization: {kind: unknown}

  - id: b_7d_sonnet
    unit: opaque_units
    capacity: anthropic.max20.C_7d_sonnet
    window: {kind_candidates: [tumbling_account_anchored], length: P7D, reset: zero}
    scope: {level: model_family, model_families: ["claude-sonnet"]}
    charge: {"$ref": "#/buckets/b_5h/charge"}
    observability:
      - {source: usage_endpoint, locator: "$.seven_day_sonnet.utilization", semantic: used_pct}

  - id: b_7d_opus
    unit: opaque_units
    capacity: anthropic.max20.C_7d_opus
    window: {kind_candidates: [tumbling_account_anchored], length: P7D, reset: zero}
    scope: {level: model_family, model_families: ["claude-opus"]}
    charge: {"$ref": "#/buckets/b_5h/charge"}
    observability:
      - {source: usage_endpoint, locator: "$.seven_day_opus.utilization", semantic: used_pct}
    notes: >
      观测到该字段可能为 null。null 的语义歧义：(a) 该窗不存在于本 plan；
      (b) 存在但未使用故不报。必须靠"用一点 Opus 后是否出现"来鉴别。
      这是一个结构辨识任务，不是数值任务。

  - id: b_extra_credits
    unit: credits
    capacity: PARAM_FROM_OBSERVATION       # 从 extra_usage.monthly_limit 直读
    window: {kind_candidates: [billing_cycle], reset: refill_to_full}
    scope: {level: subscription}
    charge: {terms: [{dim: currency_usd, coeff: 1.0}]}
    observability:
      - {source: usage_endpoint, locator: "$.extra_usage.monthly_limit", semantic: limit_abs}
      - {source: usage_endpoint, locator: "$.extra_usage.used_credits",  semantic: used_abs}
      - {source: usage_endpoint, locator: "$.extra_usage.is_enabled",    semantic: plan_type}
    overflow:
      - {action: SPILL_TO_PAYG, requires_explicit_enable: true}
      - {action: HARD_BLOCK}

channels:
  - id: claude_code_oauth
    protocol: anthropic_messages
    auth: oauth_bearer
    reliability: {interruption_hazard_per_hour: 0.01, resume_supported: true}
    spoofing_required:
      tos_note: >
        Anthropic 要求第三方工具使用 API key，不得以订阅 OAuth token 驱动
        第三方 agent 框架。将此通道用于 swarm 属明确 ToS 违反。
    models:
      - {logical_model: T1_REASONER, vendor_model_id: claude-opus-4-6,
         family: claude-opus, tokenizer: anthropic, context_window: 200000}
      - {logical_model: T2_WORKHORSE, vendor_model_id: claude-sonnet-4-6,
         family: claude-sonnet, tokenizer: anthropic}
      - {logical_model: T3_CHEAP, vendor_model_id: claude-haiku-4-5,
         family: claude-haiku, tokenizer: anthropic}

risk:
  tos_violation_class: EXPLICIT_BREACH
  ban_hazard_monthly: 0.08
  ban_blast_radius: [acct_anthropic_1, acct_anthropic_2]   # 同支付方式 → 连坐
```

## 2.3 第二个实例：per-request 计量的桶（结构套利的另一半）

```yaml
id: zai/glm-coding-max@2026-08
vendor: zai
plan_name: GLM Coding Max
price_usd_per_period: 80.0
period: month
spec_version: qdl/1.0
effective_from: 2026-08-01T00:00:00Z

gauge:
  mode: anchor_to_reference_model_usd
  reference_model: glm-5.1
  note: >
    该桶以 prompt 计数，capacity 单位是「次」，不可直接换美元。
    价值 = capacity × E[你实际塞进去的单次请求的等价API美元]。
    ★ 因此该 plan 的价值内生依赖分配策略 → 必须在 LP 中联合求解，
    不可先独立定价再排序。

parameters:
  - id: zai.max.C_5h_prompts
    unit: prompts
    prior: {kind: normal, params: {mu: 1600, sigma: 300}}
    provenance: vendor_doc          # 官方声称 ~1600/5h，需验真
    snap_candidates: [1200, 1500, 1600, 2000]
  - id: zai.max.mult_advanced
    unit: dimensionless
    prior: {kind: uniform, params: {low: 1.0, high: 5.0}}
    snap_candidates: [1.0, 1.5, 2.0, 3.0, 5.0]
    provenance: assumed
    # 官方措辞"高级模型按更高倍率消耗配额"，倍率不公开 → 必须辨识
  # ★ 最高价值的结构未知数：一个"prompt"是 turn 还是 request？
  - id: zai.max.prompt_granularity
    unit: categorical
    prior: {kind: discrete, params: {values: ["turn","request","step"],
                                     probs: [0.4, 0.4, 0.2]}}
    provenance: assumed

buckets:
  - id: b_5h_prompts
    unit: turns                      # 或 requests，由 prompt_granularity 决定
    capacity: zai.max.C_5h_prompts
    window:
      kind_candidates: [tumbling_anchored_on_first_use, tumbling_calendar]
      length: PT5H
      reset: zero
    scope: {level: account}
    charge:
      flat: 1.0                      # ★ 每请求扣 1，与 token 数完全无关
      terms: []                      # ★ 空！token 在此桶边际成本为 0
      model_multiplier:
        glm-5.1:       zai.max.mult_advanced
        glm-5-turbo:   1.0
    observability:
      - source: web_dom
        locator: "[data-testid=quota-remaining]"
        semantic: remaining_abs
        quantization: {kind: exact}
        attribution_lag_s: 60
      - source: error_body
        locator: 'quota.*?(\d+)\s*/\s*(\d+)'
        semantic: used_abs
    overflow: [{action: HARD_BLOCK_WITH_RESET_HINT}]

channels:
  - id: glm_anthropic_compat
    protocol: anthropic_messages     # 多数国产 plan 提供 Anthropic 兼容端点
    base_url: https://open.bigmodel.cn/api/anthropic
    auth: api_key
    reliability: {interruption_hazard_per_hour: 0.05, latency_p50_ms: 1800}
    admission:
      limits: {concurrency: 5, context_tokens_peak: 200000}
    models:
      - {logical_model: T2_WORKHORSE, vendor_model_id: glm-5.1,
         family: glm, tokenizer: glm}

risk:
  tos_violation_class: NONE
  ban_hazard_monthly: 0.005
```

**看这两个 YAML 的对比：** `b_5h` 的 `terms` 满而 `flat=0`；`b_5h_prompts` 的 `terms` 空而 `flat=1`。**这个结构差异就是套利来源，而且它现在是机器可读的。** 路由器只要看 charge rule 的形状，就知道该把大上下文任务送哪。

## 2.4 第三类：免费档 / 活动档（多维硬桶 + 高中断率）

```yaml
id: generic/free-tier-template@2026-08
vendor: "{{vendor}}"
price_usd_per_period: 0.0
period: month

parameters:
  - id: "{{v}}.free.rpm"
    unit: requests_per_minute
    prior: {kind: discrete, params: {values: [3,5,10,15,30,60], probs: [.1,.2,.3,.2,.1,.1]}}
    provenance: vendor_doc
  - id: "{{v}}.free.rpd"
    unit: requests_per_day
    prior: {kind: discrete, params: {values: [50,100,200,500,1000,1500], probs: [...]}}
  - id: "{{v}}.free.tpm"
    unit: tokens_per_minute
    prior: {kind: lognormal, params: {mu: 11.5, sigma: 1.0}}

buckets:
  - id: b_rpm
    unit: requests
    capacity: "{{v}}.free.rpm"
    window:
      # ★ 是离散分钟窗还是 token bucket？决定能否 burst → 直接影响 swarm 并发策略
      kind_candidates: [token_bucket_continuous, tumbling_calendar]
      kind_posterior: {token_bucket_continuous: 0.7, tumbling_calendar: 0.3}
      length: PT1M
      refill_rate: "{{v}}.free.rpm"     # /60 per sec
      burst: "{{v}}.free.rpm"
    scope: {level: credential}
    charge: {flat: 1.0}
    observability:
      - {source: response_header, locator: x-ratelimit-remaining-requests,
         semantic: remaining_abs, quantization: {kind: exact}}
      - {source: response_header, locator: x-ratelimit-reset-requests,
         semantic: reset_after_s}
    overflow: [{action: HARD_BLOCK}]

  - id: b_rpd
    unit: requests
    capacity: "{{v}}.free.rpd"
    window: {kind_candidates: [tumbling_calendar], calendar_align: utc_midnight,
             length: P1D, reset: zero}
    scope: {level: credential}
    charge: {flat: 1.0}
    overflow: [{action: HARD_BLOCK}]

  - id: b_tpm
    unit: input_tokens
    capacity: "{{v}}.free.tpm"
    window: {kind_candidates: [token_bucket_continuous], length: PT1M}
    scope: {level: credential}
    charge:
      terms: [{dim: input_tokens, coeff: 1.0}, {dim: output_tokens, coeff: 1.0}]

channels:
  - id: free_openai_compat
    protocol: openai_chat
    auth: api_key
    reliability:
      interruption_hazard_per_hour: 0.45          # ★ 极高
      interruption_granularity: mid_stream        # ★ 会在流中断
      resume_supported: false
    admission: {limits: {concurrency: 1}}
```

关键点：`interruption_granularity: mid_stream` + `resume_supported: false` 这两个字段直接决定了**只有幂等、可丢弃、可重跑的任务类**能路由到这里。这不是启发式，是 LP 的硬约束（§6）。

---

# §3 形式语义（Formal Semantics）

Schema 只是数据。语义是「给定 QDL + 请求流，状态如何演化」。必须定义到可确定性重放的程度，否则辨识和反事实分析都做不了。

## 3.1 状态

```
BucketState = (
    u:          float          # 当前累积消耗（桶单位）
    anchor:     datetime|None  # 窗口锚点（tumbling_anchored 用）
    ledger:     [(t, Δu)]      # sliding 用，需保留明细
    reset_at:   datetime|None  # 来自观测或推算
    last_obs:   Observation|None
)
SystemState = { bucket_id -> BucketState } ∪ { param_id -> Distribution }
```

## 3.2 三个核心函数（这就是你要的 system contract 的核心）

```python
# 1) 时间推进：纯函数，可重放
def advance(state: BucketState, spec: Bucket, t_from, t_to) -> BucketState: ...

# 2) 扣减：给定完整请求+响应，返回每桶的 Δ
def charge(spec: PlanSpec, req: Request, resp: Response,
           theta: ParamPoint) -> dict[bucket_id, float]: ...

# 3) 准入：能否发这个请求，以及发了会不会撞墙
def admit(spec: PlanSpec, state: SystemState, req: Request,
          theta: ParamPoint) -> Admission: ...
    # Admission = ALLOW | DENY(reason, retry_after) | ALLOW_WITH_RISK(p_break)
```

### `advance` 的语义按 WindowKind 分派

```
TUMBLING_ANCHORED_ON_FIRST_USE:
    if anchor is None:              # 尚未启动
        u = 0
    elif t_to >= anchor + length:
        u = 0; anchor = None        # ★ 整体归零，下一次请求重新起锚
    # 否则 u 不变

TUMBLING_ACCOUNT_ANCHORED:
    k = floor((t_to - epoch_anchor) / length)
    if k > last_k: u = 0            # 跨过重置时刻则归零

SLIDING_EXACT:
    u = Σ{ Δu : t_to - t < length } # 逐笔过期

TOKEN_BUCKET_CONTINUOUS:
    u = max(0, u - refill_rate * (t_to - t_from))     # 连续回补

ONE_SHOT_EXPIRING:
    if t_to > expires_at: u = capacity   # 视为耗尽（额度作废）

ROLLOVER_CAPPED (作用于 reset 时刻):
    carry = min(capacity - u, cap_multiple * capacity)
    u = -carry   # 负 u 表示有结转余量
```

**契约要求**：`advance` 必须是纯函数且满足 `advance(advance(s,a,b),b,c) == advance(s,a,c)`（可组合性）。这条性质让你能任意粒度重放历史。写单元测试强制它。

### `charge` 的两种模式（重要的工程分离）

```python
CHARGE_MODE_EXACT      # 记账用：ceil / floor / max 全部精确应用
CHARGE_MODE_LINEAR_EV  # 规划用：量化替换为期望值，max 替换为线性上界
```

规划器（LP）只能用后者，记账器只能用前者。两者的差值累积成 `linearization_residual`，定期上报。如果残差偏大（>3%），说明量化效应显著，需要在 LP 里加校正项。

**这个双模式必须写进契约**，否则你会遇到「LP 说还有 5% 余量，实际已经撞墙」的诡异 bug —— 因为 ceil 效应在小请求高频场景可以造成 20% 以上偏差（想象每请求 ceil 到 1k token，而你的请求平均 300 token → 实际消耗是名义的 3.3 倍）。

### `admit` 必须区分三种拒绝

```
DENY_ADMISSION   # 违反准入（context 超长、并发满）→ 换桶或改请求
DENY_QUOTA       # 桶满 → 换桶，并写入撞墙观测（★高价值辨识数据）
ALLOW_WITH_RISK  # 预测会在流中途撞墙，p_break=0.3 → 仅可中断任务可接受
```

`ALLOW_WITH_RISK` 是免费档的正常工作模式。返回 `p_break` 让上层决定。

## 3.3 事件溯源（Event Sourcing）—— 不可妥协

所有状态必须可从 append-only 事件流重建：

```
ChargeEvent(t, request_id, plan_id, channel_id, model, 
            dims: dict[Dim,float],           # 精确的每维原始量
            bucket_deltas: dict[str,float],  # 按当时 θ 计算的扣减
            theta_version: str)              # ★ 用了哪个参数快照
ObservationEvent(t, bucket_id, semantic, raw_value, quantization, source, trust)
WallHitEvent(t, bucket_id, error_body, reset_hint, 
             ledger_snapshot: dict[Dim,float])   # ★★ 撞墙时的完整累积账本
ResetObservedEvent(t, bucket_id, prev_u, new_u, reset_at_reported)
ParamUpdateEvent(t, param_id, posterior_before, posterior_after, evidence_ids)
StructureUpdateEvent(t, bucket_id, field, posterior_before, posterior_after)
```

**为什么必须 event-sourced**：θ 会被反复重估，你需要能用**新的 θ** 重放**旧的请求流**，才能做 (a) 参数辨识的迭代收敛 (b) 反事实分析「如果当初这样路由能省多少」(c) 漂移检测（同一批请求在不同时期的观测是否一致）。如果你只存聚合状态，这三件事全都做不了。

`dims` 必须存**原始物理量**（真实 token 数），不存已加权的结果。这是重放的前提。

`WallHitEvent.ledger_snapshot` 是你最宝贵的数据 —— 它就是那个「$\sum w x = C$」的方程。每个都要完整保留，永不删除。

## 3.4 与观测的对账（Reconciliation）

每次拿到 `ObservationEvent`，做：

```
predicted_pct = 100 * u_ledger / C_hat
residual      = observed_pct - predicted_pct
```

`residual` 的分解（这是诊断的核心）：

| residual 模式 | 归因 |
|---|---|
| 零均值随机、幅度 ≈ 量化步长/2 | 正常，量化噪声 |
| 持续正偏（观测 > 预测） | `exogenous_drain`（你手动用了）或 θ 低估 或 有未建模的桶维 |
| 阶跃式正偏，有变点 | **厂商改了系数** → 触发 CUSUM 告警 |
| 与某个 dim 相关 | 该 dim 的权重估错 → 定向重估 |
| 与 model 相关 | 模型倍率估错 |
| 与请求大小相关（小请求偏大） | 存在未建模的 `flat` 或 `floor` 或 `quantize` |
| 观测重置但 ledger 未重置 | **窗口结构判断错误** → 触发结构重选 |

把这张表做成自动归因器。它是整个系统的诊断中枢，比任何 dashboard 都有用。

---

# §4 参数与结构辨识

## 4.1 观测似然（含量化）

对不透明百分比桶：

$$P(y \mid \theta) = \Phi\!\left(\frac{y+\tfrac{s}{2} - 100\frac{\sum w x}{C}}{\sigma}\right) - \Phi\!\left(\frac{y-\tfrac{s}{2} - 100\frac{\sum w x}{C}}{\sigma}\right)$$

$s$ = 量化步长（1.0 若整数百分比，0.1 若一位小数），$\sigma$ = 观测噪声（含 attribution lag 造成的错位）。

**这就是把量化当作观测模型的一部分**，而不是试图从单次差分反解。关键收益：单个观测信息量很低但**不为零**，成千上万个观测叠加起来收敛得很好。

对撞墙观测（信息量最大）：

$$P(\text{wall} \mid \theta) = \mathbb{1}\!\left[\textstyle\sum w x \geq C\right] \cdot (1-\epsilon) + \epsilon$$

配合「撞墙前最后一个成功请求」给出的 $\sum w x < C$，你得到 **C 的一个夹逼区间**，宽度 = 最后一个请求的大小。所以：**在接近边界时故意改用小请求，可以把 C 夹到很窄。** 这是唯一值得做的主动探针设计，成本几乎为零（那些小请求本来也要发）。

对精确计数桶（免费档、credits）：直接 exact likelihood，参数几步就收敛。

## 4.2 尺度不可辨识性的处理（gauge fixing）

如 §0.3。实现上：

1. 选一个参考模型（如 Sonnet），令其 `model_multiplier = 1.0` 且 `frozen: true`。
2. 令 token 维权重 = 官方 API 价目表（`frozen`）。
3. 于是所有 `C` 的单位是「等价 API 美元」，所有其他模型的 `mult` 是「相对 API 价目比的偏离倍数」。

**输出的可解释性变得极好：**
- `C_7d = 245` → 这个周窗值 $245 的 API。
- `mult_opus = 2.0` → Opus 在订阅里被额外惩罚 2 倍（超出 API 价差之外），**这是厂商在悄悄劝你别用 Opus 的定量证据**。
- `cache_read_ratio = 1.0` → 订阅内不给缓存折扣，**缓存优化在这里白做**，应该把缓存密集任务移走。

这三条都是可直接行动的结论。

## 4.3 结构辨识（模型选择）

对每个结构未知量，枚举候选，算边际似然：

```python
def select_structure(bucket, events, candidates):
    scores = {}
    for cand in candidates:
        spec = bucket.with_structure(cand)
        # Laplace 近似或 nested sampling 的对数边际似然
        logZ = marginal_likelihood(spec, events)      
        scores[cand] = logZ - 0.5 * n_params(spec) * log(len(events))   # BIC 惩罚
    return softmax(scores)
```

但对最重要的几个结构问题，有**不需要统计的确定性判别式**，优先用：

| 结构问题 | 判别式 | 数据需求 |
|---|---|---|
| 5h 窗：anchored tumbling vs sliding | `resets_at` 在窗内是否恒定；`utilization` 是断崖归零还是阶梯衰减 | 2~3 天被动观测 |
| 周窗：账号锚定 vs rolling | `resets_at` 是否始终落在同一星期几同一时刻 | 2 周被动观测 |
| RPM：token bucket vs 离散窗 | 静默 60s 后能否瞬间打出 N 个请求 | 一次实验，1 分钟 |
| `prompt` = turn vs request | 发 1 个含 5 次工具调用的 turn，看配额掉 1 还是 5 | 一次实验，极高价值 |
| `seven_day_opus: null` 语义 | 用 Opus 消耗一点后该字段是否出现 | 一次实验 |
| Opus 是否与 all 共池 | 只用 Opus，看 `seven_day` 是否同步上涨 | 一次实验 |
| 是否存在未公开的第 N 个桶 | 长时间高强度使用后是否出现无法归因的硬断 | 被动，长期 |

**这张表应该做成一个「结构探针剧本库」，每个 plan 接入时跑一遍。** 总成本大约几十次请求，收益是整个模型的正确性。这比精调数值参数重要得多 —— 结构错了，数值再准也没用。

## 4.4 整数吸附

厂商内部系数几乎总是整齐值。步骤：

1. 得到连续后验点估计 $\hat{w}$。
2. 在 `snap_candidates` 中找最近的，若在后验 90% 可信区间内 → 吸附并 `frozen`。
3. 无候选时用 `mpmath.pslq` 找 $\hat{w}$ 与 1 的低阶整数关系，看是否为简单分数。
4. 吸附后重新拟合剩余参数（自由度减少 → 其余参数精度大幅提升）。

这是**级联式精度提升**：吸附一个参数，其他所有参数的置信区间都收窄。所以按「吸附置信度」降序逐个吸附。

## 4.5 漂移检测

对每个参数维护 CUSUM：

$$S_t = \max(0, S_{t-1} + (r_t - \mu_0 - k))$$

$r_t$ = 标准化残差。$S_t > h$ → 告警 + 触发局部重估 + 记录 `StructureUpdateEvent`。

**同时对厂商文档做语义 diff**：不是文本 diff，而是「抽取页面中所有数字 + 所有 plan 名 + 所有模型名，做结构化比较」。文本 diff 会被无关改版淹没。

**双通道交叉验证的价值**：若「文档变了 + 参数漂移」同时发生 → 确认是厂商调整（高置信）。若「参数漂移但文档没变」 → 厂商静默调整（这是最有价值的发现，也是最该公开的东西）。若「文档变了但参数没漂移」 → 文档改的是别的东西或尚未生效。

这个 2×2 表是你审计产出的核心格式。

## 4.6 实现选型

| 任务 | 库 | 备注 |
|---|---|---|
| 贝叶斯后验（含量化似然） | `numpyro`（JAX，快）或 `pymc` | 量化似然写成 custom `dist.Distribution`，用 NUTS。参数量 20~100，秒级 |
| 快速点估计（在线） | `scipy.optimize.minimize(method="L-BFGS-B")` + bounds | 每次新观测做 warm-start 增量更新，毫秒级 |
| 凸投影（保证 w>0, 单调性约束） | `cvxpy` | 只在需要硬约束时用 |
| 整数关系 | `mpmath.pslq` | |
| 变点检测（离线） | `ruptures` | |
| 变点检测（在线） | `river`（`drift.PageHinkley`/`ADWIN`） | |
| 结构模型选择 | 自己写 BIC / 用 `dynesty` 算边际似然 | 候选数少（<10），暴力枚举足够 |

**架构建议**：在线用 L-BFGS 增量点估计（低延迟，供路由器用），离线每小时用 NUTS 跑全量后验（供审计与置信区间用）。两者写入同一 `ParamUpdateEvent` 流，标注 `estimator: online|offline`。

---

# §5 观测层与登录态

## 5.1 三通道信息架构（优先级不可颠倒）

```
主通道 ─ 响应头 + usage 字段          零成本、零延迟、搭载每次真实请求
  ↓ 覆盖不到的
次通道 ─ 官方 usage endpoint / OAuth  低频轮询（3 分钟）
  ↓ 覆盖不到的
末通道 ─ 网页 DOM                     每日 1 次，只取前两者拿不到的
```

**末通道只应该负责这些**（而不是额度状态）：
- plan 档位定义与价格
- 活动规则原文（供语义 diff）
- 美元余额 / credits 余额（有些只在网页显示）
- 账单历史

**已确认可用的主/次通道**（务必以你自己实测为准，这些会变）：

| 厂商 | 通道 | 位置 | 精度 |
|---|---|---|---|
| Anthropic | 次 | `GET api.anthropic.com/api/oauth/usage`，`Authorization: Bearer <oauth>`，`anthropic-beta: oauth-2025-04-20`。返回 `five_hour / seven_day / seven_day_opus / seven_day_sonnet / extra_usage`，各含 `utilization` + `resets_at` | 疑似整数百分比，需实测 |
| Anthropic | 主 | 本地 JSONL `~/.claude/projects/**/*.jsonl` | 精确 token 账本（ledger 校验基准） |
| OpenAI Codex | 主 | 响应头 `x-codex-primary-used-percent`、`-window-minutes`、`-reset-at`(epoch ms)、`x-codex-secondary-*`、`x-codex-plan-type`、`x-codex-active-limit`、`x-codex-rate-limit-reason` | **一位小数**（如 65.5）→ 信息量比 Anthropic 高 10 倍 |
| 通用 OpenAI 兼容 | 主 | `x-ratelimit-limit/remaining/reset-{requests,tokens}` | 精确 |
| Groq 等 | 主 | 每次响应都带 rate-limit 头，**不必等 429** | 精确 |
| OpenRouter | 次 | `GET /api/v1/key` | 精确 |
| LiteLLM 自身 | 主 | `x-litellm-key-remaining-requests-{model}` / `-tokens-` | 精确 |

**Codex 的一位小数是重要资产**：量化步长 0.1 而非 1.0，同样数量的观测能把参数估到 10 倍精度。所以 Codex 侧的参数会最先收敛，可以用它验证整套辨识管线正确性，再迁到 Anthropic。**这决定了开发顺序：先做 Codex，后做 Anthropic。**

## 5.2 登录态：一次性人工登录 + 持久化 + refresh

你的要求是「一次性登录后持久化」，这完全可行且干净。

**优先级排序**：

```
1. OAuth refresh token          ← 首选，生命周期最长，官方支持路径
2. 长期 API key                  ← 次选，最稳定但很多订阅档不给
3. Cookie / storageState         ← 末选，最易失效
```

**refresh token 的自维护模式**（`ccusage` 的 Python 版已验证此路可行）：读 `~/.claude/.credentials.json`，access token 约 1 小时过期，用 `refreshToken` 自行刷新并**写回同一文件**，与官方 CLI 保持同步、互不冲突。这个模式应该照抄，并推广到所有支持 OAuth 的厂商。

**Playwright 侧的持久化**：

```python
# 一次性人工登录（有头，你亲自操作）
from playwright.sync_api import sync_playwright

PROFILE = "/secure/profiles/acct_anthropic_1"   # ★ 一账号一 profile，绝不共用

with sync_playwright() as p:
    ctx = p.chromium.launch_persistent_context(
        user_data_dir=PROFILE,        # 保留完整 profile：cookies/localStorage/
                                      # IndexedDB/passkey/设备指纹/service worker
        headless=False,
        proxy={"server": "http://固定出口IP:port"},   # ★ 与该账号绑定，永不变
        viewport={"width":1440,"height":900},
        locale="zh-CN", timezone_id="Asia/Shanghai",
    )
    page = ctx.new_page()
    page.goto("https://claude.ai/login")
    input("请在浏览器里完成登录（含 OTP / magic link），完成后回车...")
    ctx.storage_state(path=f"{PROFILE}/state.json")   # 额外导出一份便携态
    ctx.close()

# 后续无头复用（同一 profile，同一代理）
with sync_playwright() as p:
    ctx = p.chromium.launch_persistent_context(
        user_data_dir=PROFILE, headless=True,
        proxy={"server": "http://固定出口IP:port"},
    )
    ...
```

**三条不可违反的规则**：

1. **一账号 = 一 persistent profile = 一固定出口 IP**。共用 profile 或 IP 是被封最快的路径。这不是优化建议，是硬约束。
2. **不要用无头默认指纹去访问带 Cloudflare/Turnstile 的站**。持久 profile 已经积累了通过挑战的信任信号，但如果被挑战，就需要 `patchright`（反检测 Playwright fork）或 `camoufox`。不过：**你是真人真账号，正确策略是降低访问频率、保持指纹稳定，而不是加强伪装**。伪装是给爬别人数据用的，你只是读自己的账号。
3. **凭证加密静态存储**。不要明文 JSON。用 `age`/`sops` 加密，或 OS keychain。这个系统会成为「一处持有你全部厂商凭证」的单点，必须当作最高敏感资产。

## 5.3 何时必须人工介入（设计成优雅降级）

不要追求 100% 自动。设计成：

```
凭证健康度检查（每小时）
  ├─ refresh 成功           → OK
  ├─ refresh 失败 401/403   → 标记 credential.state = NEEDS_HUMAN
  │                           发通知（Telegram/邮件）
  │                           ★ 该 credential 从 LP 的可用集中移除
  │                           ★ 其他账号继续正常工作
  └─ 网页 DOM 出现登录页     → 同上
```

关键设计：**单个凭证失效必须不影响全局**。LP 每小时重解，失效的桶容量置 0，流量自动流向别处。你在方便时手动重登一次，几分钟的事。这比构建自动读邮箱/短信的 OTP 管线安全得多，也简单得多 —— 而且你明确说了不需要自动登录，所以这条路是对的。

## 5.4 网页采集的「示范一次 → 生成脚本 → 自愈」

分层，确定性优先：

```
第 1 层  确定性 Playwright 脚本         快、免费、稳定  → 99% 的日常运行
第 2 层  脚本失败 → 视觉 agent 重定位    贵、慢         → 元素变了时
第 3 层  agent 成功后回写更新脚本 + PR   人工 review    → 收敛回第 1 层
```

**生成第 1 层脚本的方式**（按推荐度）：
- `playwright codegen` 录制你的操作 → 最可靠，零 LLM 依赖
- `Stagehand`：`act()` 有 action cache，第一次用 LLM 定位，之后复用缓存的选择器 → 天然就是这个分层模式
- `Skyvern` / `browser-use`：适合第 2 层的自愈 agent

**关键工程细节**：选择器要用**语义锚**而不是 CSS 路径。优先级：`getByRole` > `getByText` > `data-*` 属性 > CSS 路径。前者在改版中存活率高一个数量级，能显著减少触发第 2 层的频率。

另外：**提取值时同时提取上下文标签**，做「值 + 标签」配对校验。例如不只取 `1600`，而是确认它旁边写着「prompts / 5 hours」。这样改版把两个数字换位置时你能发现，而不是静默读错。这一条能防住最危险的失败模式（静默读到错的数字，污染整个参数估计）。

---

# §6 调度：LP + 影子价格 + 结构套利

## 6.1 问题形式化

**决策变量**：$x_{jk}$ = 分配给 (任务类 $j$, 通道/桶组合 $k$) 的任务数量（连续松弛）。

因为需求无限，不需要「任务数上限」约束 —— 只有容量约束和质量约束。

$$\max_{x \ge 0} \sum_{j,k} v_{jk} \, x_{jk}$$

s.t.

$$\underbrace{\sum_{j,k} a^b_{jk} x_{jk} \le \hat{C}_b^{\text{eff}}(t) \quad \forall b}_{\text{每个桶的容量}}$$

$$\underbrace{\sum_{j,k} \rho_{jk} x_{jk} \le \Theta}_{\text{总吞吐/并发上限}}$$

$$\underbrace{x_{jk} = 0 \ \text{ if } \ \text{compat}(j,k) = \text{false}}_{\text{质量/可靠性/能力硬约束}}$$

其中：

**$a^b_{jk}$ = 任务类 $j$ 在组合 $k$ 上对桶 $b$ 的单位消耗**，由 `charge(CHARGE_MODE_LINEAR_EV)` 用任务类的**画像分布**（平均 input/output/cache/steps）算出。**这里就是结构套利的数学入口**：
- per-request 桶：$a^b = 1$，与任务大小无关 → 大任务的 $a$ 相同但 $v$ 更高 → LP 自动把大任务送进来
- per-token 桶：$a^b \propto$ token 数 → 小任务的 $a$ 小 → LP 自动把小任务送进来

**你不需要写任何「大任务走 GLM」的规则。这是 LP 的自然解。** 这是形式化 charge rule 的全部回报。

**$v_{jk}$ = 价值**：

$$v_{jk} = \text{business\_value}_j \times \text{quality}_{jk} \times (1 - p^{\text{break}}_{jk} \cdot \text{redo\_cost}_j) \times (1-\text{ban\_hazard}_k \cdot \text{blast}_k)$$

- `quality_jk` 来自你的私有 eval（§11）—— 任务类 $j$ 在通道 $k$ 的模型上的得分
- `p_break` 来自 `reliability.interruption_hazard_per_hour × 预期时长`
- **可中断任务的 `redo_cost` 接近 0，不可中断任务接近 1** → 免费档的 $v$ 对前者几乎不打折，对后者归零。这自动实现了你说的「断了也无所谓的工作放免费档」。
- `ban_hazard` 项让 ToS 风险进入目标函数，而不是靠你记着

**$\hat{C}_b^{\text{eff}}(t)$ = 有效剩余容量**：

$$\hat{C}_b^{\text{eff}} = \max\!\left(0,\ \text{quantile}_\alpha(\hat{C}_b) - u_b - \text{exo\_forecast}_b \right) \times \frac{T_{\text{remaining in window}}}{T_{\text{window}}}\Big|_{\text{仅 sliding/tumbling 视情况}}$$

用**低分位数**（如 $\alpha=0.2$）而不是均值 —— 因为 $C$ 是估计量，用保守分位数避免频繁撞墙。$\alpha$ 本身是可调的风险偏好参数：$\alpha$ 越低越保守（少撞墙但浪费额度），越高越激进。**因为你需求无限，撞墙其实不可怕（只是切换），所以 $\alpha$ 可以调得比较高（0.5~0.7），让利用率接近 100%。** 这是你的场景特有的优化。

## 6.2 影子价格

解 LP 得到对偶变量 $\lambda_b \ge 0$（桶 $b$ 容量约束的影子价格），单位是「每单位桶资源的价值」。

**路由规则退化为查表 + 比较**：

$$\text{score}(j,k) = v_{jk} - \sum_b \lambda_b \, a^b_{jk}$$

选 `score` 最大且 > 0 的 $k$。

这个式子的美妙之处：
- $\lambda_b = 0$ 的桶（容量过剩用不完）→ 使用它完全免费 → 优先倾销
- $\lambda_b$ 大的桶（紧俏）→ 只有高 $v$ 任务能负担
- **同一个任务在不同桶的选择自动包含了「这个桶的机会成本」**

## 6.3 在线执行

```
每小时（或桶状态变化 >5% 时）：
    刷新 θ 后验 → 重算 C_eff → 解 LP → 写入 shadow_price 表 (bucket → λ)

每个请求（微秒级）：
    读 shadow_price 表 (内存/Redis)
    枚举兼容的 (channel, bucket_set) 组合，算 score
    带 hysteresis 选最优（避免抖动：只有 score 差 >10% 才切换）
    admit() 检查 → 发出 → 记 ChargeEvent
```

LP 规模：桶数 $\times$ 任务类数。即使 50 个桶 × 50 个任务类 = 2500 变量，HiGHS 毫秒级。完全不是瓶颈。

**Hysteresis 很重要**：没有它，路由会在两个 score 接近的桶之间高频抖动，破坏 prompt cache 局部性（缓存是按 provider 分的，抖动 = 缓存全失效 = 成本暴涨）。所以路由目标里应该加一项「缓存亲和性奖励」：如果某任务的上下文已在通道 $k$ 有热缓存，$v_{jk}$ 加成。

## 6.4 中断恢复（免费档专用）

```
任务提交时标记 interruptible: true/false + checkpoint_granularity
免费档只接 interruptible 任务
mid_stream 中断 → 已产出的 partial output 保留 → 
    若任务是「可续写」型 → 用另一通道续写
    若任务是「原子」型 → 丢弃重跑（这就是 redo_cost）
```

**关键**：`redo_cost` 应该从实际观测中学习，而不是拍。记录每次中断的重跑成本，更新该任务类的 `redo_cost` 估计。这让 LP 随时间自动校准对免费档的信任度。

## 6.5 求解器选型

| 用途 | 库 |
|---|---|
| 主 LP（连续松弛 + 对偶价） | `highspy`（HiGHS，Apache-2.0，快，直接给对偶值）或 `scipy.optimize.linprog(method="highs")` |
| 需要整数分配（如「整个任务只能给一个桶」） | `ortools` CP-SAT。但**优先用连续松弛**：任务量大时松弛解几乎无损，且才有对偶价 |
| 建模层 | `cvxpy`（可读性好）或直接构造稀疏矩阵（更快） |

**建议直接用 `highspy` 手搓稀疏矩阵**，因为你需要对偶值，而且要跑得很频繁。`cvxpy` 的建模开销在这个频率下会成为瓶颈。

---

# §7 系统契约（Service Contracts）

明确的服务边界。这决定了你能不能分阶段开发、能不能替换组件。

```
┌──────────────────────────────────────────────────────────┐
│ 你的 swarm (任务生产者)                                     │
└───────────────┬──────────────────────────────────────────┘
                │ OpenAI/Anthropic 兼容协议 + 自定义头
                │   x-task-class, x-interruptible,
                │   x-min-quality, x-max-latency-ms
                ▼
┌──────────────────────────────────────────────────────────┐
│ DATA PLANE: Gateway (LiteLLM / Bifrost / 自写 Go 代理)      │
│  职责：协议适配、认证注入、流式转发、重试、cooldown             │
│  ★ 不含任何额度语义 —— 保持 dumb                            │
│  向控制面：pre_call_hook 查路由决策 / post_call 上报用量        │
└──┬───────────────────────────────────────────────┬───────┘
   │ RouteRequest / RouteDecision (gRPC, <1ms)     │ ChargeEvent (async)
   ▼                                               ▼
┌──────────────────────────────────────────────────────────┐
│ CONTROL PLANE (这是你要自己写的 3000~8000 行)                │
│                                                          │
│  Router      读 shadow_price 表 + admit() → 返回决策        │
│  Ledger      append-only 事件存储 + 状态重建 (advance)       │
│  Estimator   在线 L-BFGS + 离线 NUTS → ParamUpdateEvent    │
│  Structurer  结构模型选择 + 探针剧本调度                     │
│  Planner     每小时解 LP → shadow_price 表                  │
│  Reconciler  残差自动归因 + CUSUM 漂移告警                   │
│  Collector   响应头解析 / usage endpoint 轮询 / 网页采集      │
│  CredMgr     凭证加密存储 + refresh + 健康度 + 人工介入告警     │
│  Valuator    真实单位成本 / 折扣率 / 与 AA 对比报表            │
└──────────────────────────────────────────────────────────┘
```

## 7.1 核心接口（这是你要冻结的契约）

```protobuf
// 路由（同步，热路径，必须 <1ms）
message RouteRequest {
  string task_class = 1;
  bool interruptible = 2;
  double min_quality = 3;               // 0..1，来自私有 eval 量纲
  int32 est_input_tokens = 4;
  int32 est_output_tokens = 5;
  int32 est_cache_read_tokens = 6;
  int32 est_steps = 7;
  repeated string required_capabilities = 8;
  string cache_affinity_key = 9;        // 用于缓存亲和奖励
  int32 max_latency_ms = 10;
}
message RouteDecision {
  string channel_id = 1;
  string credential_id = 2;
  string vendor_model_id = 3;
  repeated string bucket_ids = 4;       // 会扣哪些桶
  double expected_cost_usd_equiv = 5;   // ★ 用 gauge 换算的等价美元
  double p_break = 6;                   // 中途撞墙概率
  repeated RouteDecision fallbacks = 7; // 有序备选（瞬时切换用）
  string decision_trace_id = 8;         // 可解释性：为什么选它
}

// 用量上报（异步，允许丢失但不允许重复 → idempotency_key）
message ChargeReport {
  string request_id = 1;
  string idempotency_key = 2;
  string channel_id = 3; string credential_id = 4; string vendor_model_id = 5;
  map<string, double> dims = 6;         // ★ 原始物理量，不是加权结果
  map<string, string> response_headers = 7;   // 全量原始头，供解析器演进
  int32 http_status = 8;
  string error_body = 9;                // 撞墙时的完整 body（高价值）
  int64 t_start_ms = 10; int64 t_end_ms = 11;
  bool interrupted = 12;
  double reasoning_seconds = 13;
}
```

**两条契约要点**：

1. **`dims` 必须是原始物理量**。网关不做任何加权。所有加权在控制面用当时的 θ 做，且记录 `theta_version`。这样 θ 更新后能重放。
2. **`response_headers` 全量上报**。你现在不知道哪个头有用；半年后发现某厂商新增了一个额度头，你希望能回溯解析历史数据。存全量原始头的成本极低，价值极高。这是我见过最常被跳过、最后最后悔的设计决策。

## 7.2 Gateway 侧的最小接入（LiteLLM）

```python
# control_plane/litellm_hook.py
from litellm.integrations.custom_logger import CustomLogger
import litellm, grpc, cp_pb2, cp_pb2_grpc

_stub = cp_pb2_grpc.ControlPlaneStub(grpc.insecure_channel("127.0.0.1:50051"))

class QuotaAwareRouter(CustomLogger):
    async def async_pre_call_hook(self, user_api_key_dict, cache, data, call_type):
        meta = data.get("metadata", {})
        req = cp_pb2.RouteRequest(
            task_class=meta.get("task_class", "default"),
            interruptible=meta.get("interruptible", False),
            min_quality=float(meta.get("min_quality", 0.0)),
            est_input_tokens=_estimate_input_tokens(data),
            est_output_tokens=int(data.get("max_tokens") or 2048),
            cache_affinity_key=meta.get("cache_key", ""),
        )
        d = _stub.Route(req, timeout=0.05)
        # 改写请求指向被选中的 deployment
        data["model"] = d.vendor_model_id
        data["metadata"]["cp_decision_trace_id"] = d.decision_trace_id
        data["metadata"]["cp_bucket_ids"] = list(d.bucket_ids)
        # 把控制面给的有序 fallback 交给 LiteLLM 做瞬时切换
        data["fallbacks"] = [f.vendor_model_id for f in d.fallbacks]
        return data

    async def async_log_success_event(self, kwargs, response_obj, start_time, end_time):
        _report(kwargs, response_obj, start_time, end_time, status=200)

    async def async_log_failure_event(self, kwargs, response_obj, start_time, end_time):
        _report(kwargs, response_obj, start_time, end_time, status=_status(response_obj))

def _report(kwargs, resp, t0, t1, status):
    hdrs = (kwargs.get("response_headers") or
            getattr(resp, "_hidden_params", {}).get("additional_headers", {}) or {})
    u = getattr(resp, "usage", None)
    dims = {}
    if u:
        dims["input_tokens"] = getattr(u, "prompt_tokens", 0) or 0
        dims["output_tokens"] = getattr(u, "completion_tokens", 0) or 0
        ptd = getattr(u, "prompt_tokens_details", None)
        if ptd: dims["cache_read_tokens"] = getattr(ptd, "cached_tokens", 0) or 0
        ctd = getattr(u, "completion_tokens_details", None)
        if ctd: dims["reasoning_tokens"] = getattr(ctd, "reasoning_tokens", 0) or 0
        # Anthropic 特有
        dims["cache_write_tokens"] = getattr(u, "cache_creation_input_tokens", 0) or 0
    dims["requests"] = 1
    _stub.ReportCharge(cp_pb2.ChargeReport(
        request_id=kwargs["litellm_call_id"],
        idempotency_key=kwargs["litellm_call_id"],
        vendor_model_id=kwargs.get("model",""),
        dims=dims,
        response_headers={k.lower(): str(v) for k, v in hdrs.items()},  # ★ 全量
        http_status=status,
        t_start_ms=int(t0.timestamp()*1000), t_end_ms=int(t1.timestamp()*1000),
    ))

litellm.callbacks = [QuotaAwareRouter()]
```

同时在 LiteLLM config 里配好瞬时切换（这层不用自己写）：

```yaml
router_settings:
  allowed_fails: 2
  cooldown_time: 3600          # 配额型失败冷却 1 小时
  allowed_fails_policy:
    RateLimitError: 1          # 429 立即冷却
    AuthenticationError: 1     # 凭证失效立即冷却
  num_retries: 1
  retry_policy:
    RateLimitError: 0          # ★ 429 不重试，直接 fallback（重试是浪费）
litellm_settings:
  drop_params: true
  cache: true
  cache_params: {type: redis, ttl: 3600}
```

## 7.3 参数快照契约

```
theta_version = hash(所有 frozen 参数值 + 所有 posterior 的 MAP + 所有 structure MAP)
```

每个 `ChargeEvent` 带 `theta_version`。`ParamUpdateEvent` 记录 version 变迁。**这让「用新 θ 重放旧流量」成为一个确定性操作**，是审计与反事实分析的基础。

---

# §8 开源选型（明确取舍）

## 8.1 直接用（不要自己写）

| 层 | 项目 | 理由 / 注意 |
|---|---|---|
| 网关（Python 生态） | `BerriAI/litellm` | provider 适配最全、hook 点齐、fallback/cooldown 现成。**必须锁版本 + 校验哈希**：该项目历史上出现过 PyPI 供应链投毒事件 |
| 网关（低延迟） | `maximhq/bifrost` | Go，微秒级开销。若你 QPS 高、在意 p99，用它做数据面 |
| 网关（多渠道管理 UI） | `Calcium-Ion/new-api` / `songquanpeng/one-api` | 中文生态、渠道管理和 key 池成熟。可作为"多账号 key 池"的现成 UI |
| LP 求解 | `ERGO-Code/HiGHS` (`highspy`) | 快、免费、**直接给对偶值** |
| 整数分配 | `google/or-tools` CP-SAT | 只在必须整数时用 |
| 贝叶斯辨识 | `pyro-ppl/numpyro` | JAX，量化似然可自定义，NUTS 快 |
| 变点检测 | `deepcharles/ruptures`（离线）+ `online-ml/river`（在线） | |
| 整数关系 | `mpmath` (PSLQ) | |
| 浏览器 | `microsoft/playwright` | persistent context 是核心机制 |
| 反检测（仅在被挑战时） | `Kaliiiiiiiiii-Vinyzu/patchright` / `daijro/camoufox` | **优先靠稳定指纹+低频，而非伪装** |
| 网页 agent（自愈层） | `browserbase/stagehand`（有 action cache，最贴合分层模式） | `browser-use` / `Skyvern-AI/skyvern` 备选 |
| 私有 eval | `UKGovernmentBEIS/inspect_ai` | 设计干净、适合自建题库 + 自动判分。`promptfoo` 更轻量 |
| token 计数 | `openai/tiktoken` + `huggingface/tokenizers` + 各厂官方 count_tokens 端点 | 跨厂商归一必需 |
| 时序/分析 | `TimescaleDB`（写）+ `DuckDB`（分析） | 事件量大，Timescale 的压缩很有用 |
| 编排 | `PrefectHQ/prefect` 或 `APScheduler` + Postgres advisory lock | **不建议 Temporal**：对这个规模过重 |
| 凭证加密 | `getsops/sops` + `FiloSottile/age` | 简单、可审计、无服务依赖 |
| 观测 | OpenTelemetry + Prometheus + Grafana | |
| 参考实现（读代码） | `wakamex/ccusage`（Python，OAuth usage + refresh token 回写）；`ryoppippi/ccusage`（JSONL 解析）；`Maciek-roboblog/Claude-Code-Usage-Monitor` | 前者演示了 refresh 自维护，后两者演示了本地账本解析 |

## 8.2 明确不要用 / 要自己写

- **不要把额度逻辑塞进网关**。网关是 dumb data plane。原因：网关会升级、会换、有 bug；你的额度语义是核心资产，必须独立、可测试、可重放。
- **不要依赖任何「多账号轮换」的第三方 fork**。这类项目生命周期极短（常因 ToS 压力删库），且它们把限额当写死常量 —— 恰好是你要打破的假设。可以读代码借鉴，不要作为依赖。（我上一轮提到的某个 router fork，这次未能重新验证其存在，请勿依赖。）
- **控制面必须自己写**。§7 的 8 个组件没有现成替代品，这就是那 3000~8000 行。

## 8.3 一个反直觉的建议：先不要网关

第一阶段**不要接网关**。原因：Ledger 和 Estimator 的正确性是一切的基础，而验证它们只需要「离线消费你现有的请求日志」。先做一个纯离线的 `ledger + estimator + reconciler`，喂进历史数据，看能不能把参数估出来、残差是否合理。**这个阶段零风险、零延迟压力、可以快速迭代**。等参数收敛、残差归因表能正确工作了，再接网关做在线路由。

否则你会同时调试「路由为什么抖动」和「参数为什么不收敛」两个纠缠的问题。

---

# §9 目录结构与开发路线

## 9.1 仓库布局

```
quotaforge/
├── qdl/
│   ├── model.py              # §2.1 全部 Pydantic 模型
│   ├── loader.py             # YAML → PlanSpec，$ref 解析，交叉引用校验
│   ├── schema.json           # 导出的 JSON Schema（供编辑器补全）
│   └── plans/                # ★ 你的核心资产
│       ├── anthropic/max20@2026-08.qdl.yaml
│       ├── openai/codex-pro@2026-08.qdl.yaml
│       ├── zai/glm-max@2026-08.qdl.yaml
│       ├── moonshot/kimi-code@2026-08.qdl.yaml
│       ├── minimax/coding-starter@2026-08.qdl.yaml
│       ├── alibaba/qwen-coder-lite@2026-08.qdl.yaml
│       └── free/_template.qdl.yaml
├── semantics/
│   ├── window.py             # advance()  按 WindowKind 分派
│   ├── charge.py             # charge()   EXACT / LINEAR_EV 双模式
│   ├── admit.py              # admit()    三种拒绝
│   └── replay.py             # 事件流 → 状态；支持指定 theta_version 重放
├── ledger/
│   ├── events.py             # 事件定义 + 序列化
│   ├── store.py              # Postgres/Timescale append-only
│   └── reconcile.py          # 残差自动归因表（§3.4）
├── estimate/
│   ├── likelihood.py         # 量化似然、撞墙似然、精确似然
│   ├── online.py             # L-BFGS 增量点估计
│   ├── offline.py            # numpyro NUTS 全量后验
│   ├── snap.py               # 整数吸附 (PSLQ + snap_candidates)
│   ├── structure.py          # 模型选择 + 判别式剧本
│   └── drift.py              # CUSUM / PageHinkley
├── probe/
│   ├── playbooks/            # ★ 结构探针剧本（§4.3 的表）
│   │   ├── window_kind_5h.yaml
│   │   ├── prompt_granularity.yaml
│   │   ├── token_bucket_vs_window.yaml
│   │   ├── opus_pool_shared.yaml
│   │   └── boundary_bisect.yaml
│   └── runner.py             # 探针执行 + 结果写回 ObservationEvent
├── plan/
│   ├── lp.py                 # highspy 构造 + 求解 + 提取对偶
│   ├── taskclass.py          # 任务类画像（token 分布、可中断性、价值）
│   └── prices.py             # shadow_price 表读写 (Redis)
├── route/
│   ├── server.py             # gRPC ControlPlane 服务
│   ├── scoring.py            # score = v - Σλa，hysteresis，缓存亲和
│   └── litellm_hook.py       # §7.2
├── collect/
│   ├── headers.py            # 响应头 → ObservationEvent（声明式映射表）
│   ├── endpoints.py          # usage endpoint 轮询器
│   ├── locallog.py           # Claude Code JSONL / Codex 日志解析
│   └── web/
│       ├── profiles.py       # persistent context 管理（一账号一 profile）
│       ├── scripts/          # 每站点的确定性脚本
│       ├── healer.py         # 第 2 层：Stagehand 自愈 + 回写 PR
│       └── semdiff.py        # 文档语义 diff（抽数字+标签配对）
├── cred/
│   ├── store.py              # sops/age 加密存储
│   ├── refresh.py            # OAuth refresh（按厂商实现）
│   └── health.py             # 健康度 + NEEDS_HUMAN 告警
├── value/
│   ├── gauge.py              # 等价美元换算
│   ├── report.py             # 单位有效智能成本 / 折扣率 / 利用率
│   └── aa_compare.py         # 与 Artificial Analysis list price 比对
├── evals/
│   ├── tasks/                # 20~50 题私有题库（用你真实工作抽样）
│   └── run.py                # inspect_ai 驱动，按 plan×档位 跑分
└── ops/
    ├── migrations/
    ├── dashboards/
    └── docker-compose.yml
```

## 9.2 分阶段路线（含验收标准）

**Phase 0：QDL + 语义内核（1 周）**

做：`qdl/model.py`、`loader.py`、`semantics/*`。

验收：
- 手写 3 个 plan 的 QDL（Anthropic、一个 per-request 国产、一个免费档）并通过校验
- `advance` 的可组合性性质通过 property-based test（用 `hypothesis`）
- `charge` 的 EXACT / LINEAR_EV 在合成数据上差值符合预期
- **没有任何网络调用** —— 纯离线可测

**Phase 1：Ledger + 离线辨识（2 周）★ 最关键**

做：`ledger/*`、`estimate/*`，喂入你**已有的历史日志**（Claude Code JSONL、Codex 日志、任何你有的 API 日志）。

验收：
- 能从历史 JSONL 重建 5h/7d 桶的消耗曲线
- 用 `/api/oauth/usage` 的历史观测（若无历史，采集 3 天）做拟合
- **`C_5h` 和 `C_7d` 的后验 90% 区间宽度 < 中位数的 40%**
- 残差自动归因表能正确分类合成的注入故障（人工注入「权重改变」「外生消耗」「未建模 flat」三种，看能否分对）
- 输出第一份「等价 API 美元」估计

**建议先做 Codex 而非 Anthropic**：其响应头是一位小数百分比，量化步长小 10 倍，参数收敛快得多，可以先验证管线正确性。

**Phase 2：结构辨识剧本（1 周）**

做：`probe/playbooks/*`、`estimate/structure.py`。

验收（这些是明确的、二值的结论，做完就永久受益）：
- 5h 窗判定为 anchored tumbling 或 sliding（给出后验概率 >0.9）
- 周窗判定为账号锚定或 rolling，若锚定则确定锚点时刻
- 每个国产 plan 的「prompt」判定为 turn / request / step
- 免费档的 RPM 判定为 token bucket 或离散窗
- `seven_day_opus: null` 的语义确定
- Opus 是否与 all 共池确定

**这一阶段产出的是永久性知识，且直接决定套利空间。优先级高于精调数值。**

**Phase 3：观测采集三通道（1.5 周）**

做：`collect/*`、`cred/*`。

验收：
- 主通道：所有响应头进 Ledger，全量原始头存档
- 次通道：所有 usage endpoint 3 分钟轮询，凭证自动 refresh
- 末通道：每个 plan 的网页脚本能取到 plan 定义 + 余额，且「值 + 标签配对校验」生效
- 一账号一 profile 一固定 IP 的隔离已落实
- 单个凭证失效 → 告警 + 不影响其他

**Phase 4：LP + 影子价格（1.5 周）**

做：`plan/*`、`value/*`。

验收：
- LP 求解 <100ms（50 桶 × 50 任务类）
- 影子价格随桶消耗单调上升
- **反事实验证**：用历史流量重放，比较「LP 路由」vs「实际发生的路由」的总成本，节省应显著（我预期 30%+，主要来自结构套利）
- 输出「等价美元价值 / 利用率 / 折扣率」报表

**Phase 5：在线路由（1.5 周）**

做：`route/*`、接 LiteLLM。

验收：
- 路由决策 p99 < 2ms
- 免费档中断 → 瞬时切换成功率 >99%（不可中断任务不路由到免费档）
- 无路由抖动（hysteresis 生效，缓存命中率不因路由下降）
- `SPILL_TO_PAYG` 的 guard 经过验证：故意打满，确认不会意外产生 PAYG 账单

**Phase 6：eval + 漂移监控 + 报表（持续）**

做：`evals/*`、`estimate/drift.py`。

验收：
- 20~50 题私有题库，每周自动在每个 plan×档位跑一遍
- 质量分进入 LP 的 $v_{jk}$
- CUSUM 能在合成注入的系数变更后 24h 内告警
- 文档语义 diff + 参数漂移的 2×2 交叉表自动生成

总计约 8~10 周单人工作量。Phase 1 和 Phase 2 是价值密度最高的，即使只做到 Phase 2 就停，你也已经拿到了「真实额度审计」这个核心产出。

---

# §10 需要立刻注意的三个陷阱

## 10.1 公开数字全部不可信，包括我上面引用的

我在搜索中看到大量互相矛盾的具体数字（Pro 是 45 prompts/5h？44,000 tokens/5h？周窗 40-80 Sonnet 小时？GLM Max 是 1600 还是 2000 prompts？MiniMax 是 100 还是 1500 requests/5h？）。这些来自二手博客，彼此冲突，且厂商已停止公布确切数字。

**处理方式**：所有厂商声称值只作为 `prior`（带宽的先验），provenance 标 `vendor_doc`，**永不作为 `frozen`**。你的系统的全部意义就是用实测替换它们。

一个具体的高价值观察：我看到 Codex 社区有用户报告「约 3M total_tokens 让周配额动了 1 个百分点，推出周额度只有约 300M tokens，与 Pro 预期不符」。这正是你的辨识器要回答的问题类型 —— 而且它暗示可能存在**未建模的计量维度**（比如 reasoning_tokens 被重罚，或 cache_read 未打折）。这种「用户觉得不对但说不清哪不对」的情形，你的残差归因表能直接定位。

## 10.2 ToS 与封号必须进目标函数

用订阅 OAuth token 驱动第三方 swarm，多数厂商明确禁止（Anthropic 文档直接写了第三方工具应用 API key）。我不建议你不做，但建议：

- `risk.ban_hazard_monthly` 和 `ban_blast_radius` 必须填真实估计，进 $v_{jk}$
- **`ban_blast_radius` 促使你做支付方式和出口 IP 的拓扑隔离** —— 这是最高 ROI 的风险控制，成本低效果好
- 被封会同时摧毁「已充值余额 + 已辨识参数资产 + 已积累的 profile 信任度」。后两者常被忽略但很值钱：一个跑了三个月、参数收敛很好的账号，其信息资产不可快速重建
- 因此：**在多个厂商间分散，不要在单一厂商上堆太多账号**。这个结论直接来自 blast radius 建模

## 10.3 安全：这个系统是单点

它会持有你全部厂商的凭证 + 浏览器 profile + 可能的邮箱访问。必须：

- 网关和控制面**不暴露公网**，只监听 127.0.0.1 或私有网段
- 凭证 `sops`/`age` 加密静态存储，进程内解密，不落盘明文
- **锁定所有依赖版本 + 校验哈希**，特别是 LiteLLM（有过 PyPI 供应链投毒历史）
- 每个账号用独立邮箱，与你主邮箱隔离
- 事件库里的 `error_body` 和 `response_headers` 可能含敏感信息，存储前做 PII/token 脱敏

---

# §11 价值核算：把「真实单位成本」算对

因为你需求无限、必然打满，`利用率 ≈ 100%`，这消掉了我上一轮担心的最大偏差。剩下三个必须处理：

## 11.1 主指标

$$\text{真实单位成本}_{\text{plan}} = \frac{\text{月费}}{\hat{C}_{\text{周期总量}} \text{（等价 API 美元）}}$$

这是一个**无量纲的折扣率的倒数**。直接可解释为「花 1 美元订阅买到多少美元的 API」。

对 per-request 桶，$\hat{C}$ 需要用你的实际请求画像换算：

$$\hat{C}^{\text{usd}}_{\text{request-bucket}} = C_{\text{requests}} \times \mathbb{E}_{\text{你送进去的任务分布}}[\text{API 等价成本/请求}]$$

**注意这是策略依赖的**，所以要报告两个数：
- `当前策略下的实现价值`
- `最优策略下的潜在价值`（LP 的目标函数值）

两者差距就是你还能榨出来的空间。这个差距本身是最有行动价值的数字。

## 11.2 与 Artificial Analysis 对比的正确口径

**不要比绝对值**（口径不同必然对不上，比了会误导）。算：

$$\text{折扣率}_{\text{model}} = \frac{\text{AA list price (\$/Mtok)}}{\text{你的实际有效单价 (\$/Mtok)}}$$

这个比率跨模型、跨厂商可比。输出形式：

> Anthropic Max 20x 相对 API 报价的实际折扣 = 4.2×；风险调整后（扣 8% 月封禁 hazard）= 3.9×；质量调整后（私有 eval 相对基准 0.94）= 3.7×。
> GLM Max 名义折扣 11×，但仅在「单请求 >50k input token」的任务上成立；在短请求上退化为 0.8×（即比 API 更贵）。

第二句是这个系统最有价值的输出类型 —— **它是条件化的，而不是一个标量**。任何把 plan 价值报成单一数字的比较（包括所有现有的比较网站）都是错的。

## 11.3 三个必须做的修正

**（a）token 不可通约。** 不同 tokenizer 对同一段中文的 token 数差 20~30%。所以：
- 主指标用 `$/成功完成的标准任务`（来自私有 eval 题库）
- 辅指标用 `$/千字符输出`
- `$/Mtok` 只在同一 tokenizer 内部使用

**（b）质量不是常量。** 订阅档存在隐藏降级（上下文被砍、量化版本、高峰降级、thinking budget 压缩），这些不会写在页面上。所以必须跑私有 eval：

```
20~50 题，从你真实工作中抽样，带自动判分
每周在每个 plan × 每个档位 × 每个模型上跑一遍
→ quality_score[plan][model][task_class]
→ 直接进 LP 的 v_jk
```

**这个 eval 同时是「静默降级」的唯一探测手段**（§1.7 的 `SILENT_QUALITY_DROP`）。质量分的时间序列做变点检测，掉了就告警。**这是你能发现「厂商悄悄换了量化版本」的唯一办法**，我认为它的价值被严重低估。

**（c）最终指标**

$$\text{单位有效智能成本} = \frac{\text{真实单位成本}}{\text{质量分}} \times \frac{1}{1 - \text{风险折损}}$$

按任务类分别报告，不要聚合成一个数。
