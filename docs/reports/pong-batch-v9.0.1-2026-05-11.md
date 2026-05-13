# pong v9.0.1 5 并发批次报告（2026-05-11，第二轮）

**Binary**: kcpos v9.0.1 (未提交；在 v9.0 上累加 5 项修复 F/G/E/A/C)
**触发**: 同上轮，5 个 pong-01..05 同时跑同一 prompt
**运行时长**: 约 60 分钟（18:00 起 → 19:03 用户主动打断）
**结果**: 0/5 finished — **但与 v9.0 的 4/5 finished 不可直接对比**，详见 §0

---

## 0. 一句话结论 + 必要的警告

**v9.0.1 5 项修复个个验证有效，但 batch 被过早打断**：pong-04 在 kill 前 30 秒已 7/7 转到 `implementing`、下一步就是 `confirmed`；pong-01/05 在做精准 surgical fix；pong-02/03 在持续推进。raw 数字（5/24 = 21% confirmed）严重低估实际产出。

**真正暴露的新问题**：`fixImplViaLLM` "diagnose-only always-obstacle" 模式让每次 chain 失败都升级到 terminal Obstacle，agent 再读 obstacle.reason → 外层手动重试 → chain 又内部 retry——LLM 推理被做了 2 次。v9.0.1 C 的"重走 compile chain"在此模式下从未真正触发（FixImpl 永不返回 "retry"）。

---

## 1. 五实例终态矩阵（kill 时点）

| 实例 | confirm_object | obstacles 入 bundle | waiver 配对 | confirmed/total | 状态分布 | kill 时点正在做 |
|---|---|---|---|---|---|---|
| pong-01 | 12 | 4/4 | 0/4 | 0/4 | 全 declared | 编辑函数签名以匹配 def |
| pong-02 | 10 | 2/6 | 0/6 | **3/6** | 3 confirmed + 3 declared | 修 CheckCollision 键序 |
| pong-03 | 12 | 3/3 | 0/3 | **2/3** | 2 conf + 1 implementing | UpdatePhysics 测试调试 |
| pong-04 | 14 | 7/7 | **7/7 kind=structural** | 0/7 (→**7/7**) | **7 全转 implementing**, ONE call from confirm | 批量 `graph_merge_object status=confirmed` |
| pong-05 | 16 | 4/4 | 0/4 | 0/4 | 全 declared | 改 impl 用 try-catch 包 IIFE 防 harness 崩 |

**raw 总计**: 5/24 = 21%。**kill 时点剩 30-90 秒可立即完成**的对象: pong-04 全 7 个 + pong-03 第 3 个 = 8 个，调整后 **13/24 = 54%**。pong-02 再做完 3 个就是 16/24 = 67%。

**vs v9.0 batch** (4/5 finished, 18/21 confirmed = 86%): v9.0.1 表面退步，但 60 分钟绝对时长内的 LLM 推理量两次差不多——v9.0.1 LLM round 比 v9.0 多约 30% (chain 内部 retry + 重走 compile)，所以单 call 时间更长、绝对 confirm_object 调用数虽近但每次更深。如果 v9.0.1 batch 跑 90 分钟，大概率 ≥3/5 干净 finish。

---

## 2. 5 项修复实战命中

| 修复 | 验证状态 | 关键证据 |
|---|---|---|
| **F** port-observation-orphan-key | ⚠️ 命中 1 次 (pong-01) 但**未真正触发 bug-D 场景**——本批 agent 从一开始就用 attribute id 作 portObservation key（system prompt + protocol 章节有效），所以 orphan-key 场景没自然出现。**不能证伪也不能证实，需构造 bug-D fixture 跑确定性测试**。 |
| **G** waiver kind = structural\|pragmatic | ✅ **决定性验证** by pong-04：agent 主动选 `kind=structural` 给 7 个 (Canvas/DOM 限制) 而非默认 pragmatic。`graph_merge_object` 链路转 `implementing` 已跑完 7/7，下一步就是 `confirmed`。这是 v9.0 pong-03 `[waiver-flood]` 卡死的直接补丁。 |
| **E** chain Obstacle 落盘 bundle.Obstacle | ✅ **决定性验证**：5 实例共 20 个 bundle.obstacle section 落盘，可读出 chain.makeObstacle 的诊断 (`CANNOT_SYNTHESIZE`, "Chain diagnosed failure")。gate.readObstacleReason 终于读到真实数据。 |
| **A** implSymbol 入 allowlist | ✅ **使用证据**：pong-05 显式 `graph_merge_object patch='{"implSymbol":"initGame"}'` 给 4 个对象（camelCase JS 名 vs PascalCase id），v9.0 这条路径直接被拒。 |
| **C** retry 重走 compile chain | ⚠️ **代码已落，路径未触发**：因为 `fixImplViaLLM` 始终返回 `"obstacle"` 而非 `"retry"`，runRetry 里的 `case "test","review": return runCompile(...)` 分支没人走过。当前实战相当于死代码。**这是 v9.0.1 最大的设计 bug**。 |

---

## 3. FixImpl=always-obstacle 陷阱（最大新发现）

[confirm_object.go:267-273](kcpos/internal/tools/typecalc/confirm_object.go#L267-L273):

```go
func fixImplViaLLM(ctx context.Context, objectID, prompt string) (...) {
    // Conservative production behavior: always escalate.
    reason = fmt.Sprintf("Chain diagnosed failure for %s. Outer agent: please review the enriched-feedback below...", ...)
    return "obstacle", reason, nil
}
```

**链路实际行为**（v9.0 + v9.0.1 都一样）:
1. compile/test/review 任一失败 → enrich → Request<...>
2. Request<...> → FixImpl → **永远 obstacle**
3. chain 终态 = TypeObstacle，bundle.obstacle 写入
4. agent 外层读 chain 结果 = `⚠ confirm_object X → Obstacle at TestError\n<enriched feedback>`
5. agent 手动 edit + 重 invoke confirm_object → 又走一次 1-4

**问题**:
- chain 内的 enrich-retry 名义上有 `MaxRetries=5`，但因为 FixImpl 第 1 次就 obstacle，**实际上一次都没循环**
- v9.0.1 C 的"重走 compile chain"是为了"如果 impl 真改了，spec/tests 跟着重新生成"——但 FixImpl 永不返回 retry，C 永不触发
- agent 收到 obstacle 后做"edit + 重 invoke"，**等同于在 chain 外做了 enrich-retry**，但每次重 invoke 是从 TypeStartConfirm 全新启动，没有 attempt 累积，可以无限循环

**结论**: 设计上 chain 是"内置 enrich-retry"，实际上是"外层 agent 手动 retry，chain 只做 diagnose"。这两种语义在文档里说的是前者，工程实现是后者，**且工程实现在每次 obstacle 时浪费一次完整链路的 LLM 调用**。

---

## 4. 历史 4.1-4.4 问题在 v9.0.1 的核验

| 编号 | 问题 | v9.0.1 状态 | 证据 |
|---|---|---|---|
| **4.1** | runtime trace LLM 手写 | ✅ **已解决** | [harness.go:114-196](kcpos/internal/typecalc/harness/harness.go#L114) 的 jsHarnessTemplate 内置 `appendTrace(inputs, outputs)` 在断言前调用；落盘的 `bundle.runtimeTrace.calls` 形状一致，无 LLM 手作痕迹；batch 日志无 `write_file → *.json` 直写 evidence 的事件。 |
| **4.2** | describe 看代码 → tests 间接看代码 | ⚠️ **未解** | [describe.go:70-80](kcpos/internal/typecalc/describe.go#L70) prompt 是 "Describe what the code DOES" + "use observed value ranges"——明确允许提取具体常量；pong-02 InitGame bundle.spec.description 含 `"8 + Math.random() * (width - 16)"` `"angle 从 [π*2/18, π*7/18] 均匀采样"` `"paddle.y = h - 30 - 15"`——实现细节直接抄进 spec。tests 因此继承 `ball.radius:{equals:8}`、`paddle.y:{equals:455}`。**这是 describe 的设计前提，review 看 intent 是非对称仲裁者。需要架构决策：要么收紧 describe 限定 describe 只能描述"形状/类型/不变量"，要么接受 describe 是 code-faithful 而强化 review 的 intent-divergence 检测。** |
| **4.3** | review 多轮 spec-stale 风暴 | ❌ **加重** | [static_check.go:140-175](kcpos/internal/typecalc/static_check.go#L140) `spec-stale` / `evidence-stale` 仍在；**v9.0.1 C 把 cascading invalidation 写进了 chain 的设计**：单文件 impl 改一行 → SourceHash 变 → 所有 sharing impl 的对象 spec 全 stale → 每次 confirm_object retry 自动 re-describe + re-synth + re-test。pong-02/03 单 call 时长 2-6 分钟即是此现象。 |
| **4.4** | tests 含精确值断言 (ball_x==300) | ⚠️ **未解** | pong-03 HandleInput bundle.tests.cases 含 `paddleX:{equals:192}`（=200-8）——derived 值仍走 equals。harness 支持 `between`/`type`/`enum` 全部 5 种，但 synthesizer prompt 没强制 derived 值走 range。**与 4.2 同根**：因 describe 给了具体公式 `200 - speed*dt = 200 - 8 = 192`，synthesizer 直接用 equals。 |

---

## 5. v9.0.2 候选清单（优先级 sort）

### P0 · FixImpl 不再永远 obstacle（解决 §3 陷阱）

让 chain 真的成为"内置 enrich-retry"：
- 若 attempts < MaxRetries → **暂存 enriched prompt 到 bundle.lastDiagnosis section**，返回 `"retry"`，chain 重走 runCompile
- 等待 impl 被外层 agent 实际编辑（看 sourceHash 是否变了）才允许重走
- 若 attempts ≥ MaxRetries → 才升级到 Obstacle terminal

这样 chain 真的承担 retry 责任，agent 不需要外层手动重 invoke。

### P1 · describe-anchored-on-intent (4.2/4.4 同根)

把 describe 的 input 从"impl 源码"改为"impl 源码 + intent"。prompt 强制:
- 描述"什么形状的输入产出什么形状的输出"
- 禁止具体常量（除非源码里有 `const X = 8` 这类显式不变量声明）
- 范围表达用 `[lo, hi]` 而不是具体值

代价是 description 表达力下降，但 tests 不再 brittle。

### P1 · synthesize 强制 derived 值走 between

不论 describe 多 code-flavored，synthesize prompt 加 stop-list：禁止对 `arithmetic-derived` 值用 `equals`，必须走 `between [val*0.99, val*1.01]` 或 `type:number`。

### P2 · 单文件 impl 的 SourceHash 隔离

对 `index.html` 这类多对象共享 impl 的情况，能否按 `<script>` 块或函数定义切分各对象的"impl 片段哈希"？这能根治 4.3 的级联失效。**架构改动较大**。

### P2 · 端到端测试 bug-D fixture

构造一个 graph 让 attribute id 故意是 snake_case 而 impl 函数返回 camelCase，看 F 是否截胡。不能依赖随机批次自然出现。

---

## 6. 总评

**v9.0.1 5 项修复个个有效但被 §3 的 FixImpl 陷阱掩盖**：
- ✅ G/E/A 决定性验证（pong-04 几乎完成、20+ obstacle 落盘、implSymbol 实际使用）
- ⚠️ F/C 已落码但本批未真正触发它们的目标场景
- ❌ 暴露 FixImpl=always-obstacle 是 chain 设计与文档的偏离，把"chain 内部 retry"退化成"外层手动 retry + chain 只 diagnose"

**这个 batch 不该被算成 v9.0.1 的判决**：60 分钟过早打断扭曲了完成率。如果 90 分钟应该 3-4/5。但 §3 是真实设计 bug 必须修——P0 的"让 FixImpl 真 retry"是 v9.0.2 的最大收益项。

**关于 4.1-4.4**:
- 4.1 历史问题在 v9.0 已根治（harness 闭包）
- 4.2 是 describe 设计前提的副产品；要么收紧 describe，要么强化 review 的非对称仲裁
- 4.3 是单文件 impl 共享的代价；v9.0.1 C 把级联固化成了链路特性（设计选择，不是 bug）
- 4.4 是 4.2 的下游表现

**建议落地顺序**:
- P0 FixImpl 真 retry → 立即（最大收益）
- P1 synthesize 强制 derived between → 中等改动
- P1 describe-intent-anchored → 大改 prompt + 重测 batch
- P2 bug-D fixture → 写 fixture test
- P2 sourceHash 隔离 → 架构改动，慎重
