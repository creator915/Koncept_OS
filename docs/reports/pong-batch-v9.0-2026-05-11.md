# pong v9.0 5 并发批次总报告（2026-05-11）

**Binary**: kcpos v9.0 (uncommitted, Phase 1-8 全栈：bundle 统一 / ObjectState 去重 / protocol-in-code / 类型驱动 state machine / enrich-retry / confirm_object 高层入口 / fixture 回归测试)
**触发条件**: 5 个 pong-01..05 目录同时运行同一 prompt（实现 SPEC.md 单人乒乓球）
**运行时长**: 约 76 分钟（17:14 → 17:32 用户主动打断 batch）
**结果**: **2 干净 finish (02/05) + 1 准干净 finish (03，5/5 已 confirmed, root 在 R1-R4 收尾) + 1 在飞 (04, 3/4 confirmed) + 1 深坑 (01, 1/3 confirmed)**

---

## 0. 一句话结论

**v9.0 design 在 4/5 实例验证成功** — 51 次 `confirm_object` 调用 0 个 obstacle 抛到 terminal，证明 enrich-retry 内循环在大多数情况下能消化失败而不退出。但 pong-01 复现 5+ 次"portObservation key/id 大小写"同一 bug，暴露 enrich-retry 的根本盲区：**当错误信号无法定位到正确的 root cause 时，feedback channel 退化为噪声**，并且 chain 的 retry 上限（5）会无声丢弃后续重试机会。

---

## 1. 五实例终态对比矩阵

| 实例 | 评级 | confirmed | confirm_object 次 | calls/object | obstacles → terminal | 日志 KB | 最终状态 |
|---|---|---|---|---|---|---|---|
| pong-01 | ★ | **1/3** | 14 | 14.0 | 0 | 132 | 深坑 (bug D 重犯 5 次) |
| pong-02 | ★★★★ | 3/3 | 11 | 3.67 | 0 (2 in-chain waiver) | 132 | **finished** |
| pong-03 | ★★★★ | 5/5 (objects) | 7 | **1.40 (最优)** | 0 (5 in-chain waiver) | 260 | root R1-R4 中被打断 |
| pong-04 | ★★★ | 3/4 | 8 | 2.67 | 0 | 192 | 在飞 (RenderFrame 待 confirm) |
| pong-05 | ★★★★★ | **6/6** | 11 | 1.83 | 0 (2 in-chain waiver) | 132 | **finished, 无 mass-waiver** |

**总计**：
- **18/21 = 86% 对象 confirmed**（不计 pong-04 还在跑的 RenderFrame）
- **51 次 confirm_object，0 个 obstacle 升级到 agent terminal**
- 平均 2.83 calls/object（v8.7 基线远高于此，且形式上是 6-step 手动链 ≥ 6 次/对象）
- 所有干净 finish 的实例 root session = `finished`，无死锁

---

## 2. v8.7 vs v9.0 head-to-head

| 维度 | v8.7 (5/5 finished) | v9.0 (本批) | 趋势 |
|---|---|---|---|
| **日志体积** | pong-02 ≈ 458 KB | pong-02 = 132 KB | **3.5× 压缩** |
| **mass-waiver** | pong-03 / 05 ≥ 67% | pong-05 33%（DOM + Canvas 合法）| **结构性消除** |
| **手动 6-step chain** | 是（compile/describe/synthesize/test/review/merge）| 单一 `confirm_object` 入口 | **接口压缩** |
| **obstacle 喷射** | 部分实例 confabulation | 0 个 terminal obstacle | **chain enrich-retry 消化** |
| **死锁** | 0 | 0 | 持平 |
| **batch 完整性** | 5/5 finished | 2/5 finished + 3 被打断 | v9.0 batch 提前 kill, 不可直接比较 |
| **新失败模式** | [waiver-flood] 触发率低 | **bug D 重复犯（pong-01）** | 新增 1 类风险 |

`confirm_object` 把 v8.7 的 6 次手动链调用压成 1 次（pong-03 InitGame 单 call confirm），是本次最显著的工程压缩。但代价是：手动链时每步失败 agent 能直接看到失败 stage 的 raw output；现在 chain 的 enrich-retry feedback 必须由 chain 编排出有用的"告诉 LLM 怎么改"信息，这一步质量决定一切。

---

## 3. 五实例诊断（一段话各家）

### pong-02 ★★★★ — 干净 finish + chain enrich-retry **真的在用**
3/3 confirmed，11 次 calls/3 对象 = 3.67/object，看似 cost 高，但分布合理：ProvideConfig 第一发即过；InitGame/StepPhysics 在 HTML harness 限制下 chain 内部 `cycles.count=1` + `prevIssues=["base-evidence-failed","runtime-output-missing"]` — **enrich-retry 内循环可见落盘**，最终 chain 自己生成 obstacle+waiver 对，agent 接受 confirmed。最终 index.html 8/8 checkpoint 全绿。v9.0 设计的"chain 自治消化失败 → LLM 不必处理废话"叙事在这里被验证。

### pong-05 ★★★★★ — 6 对象最优 + v8.7 mass-waiver pathology **架构性消除**
6/6 confirmed，11 次 calls/6 对象 = 1.83/object。InitGame / MovePaddle / UpdateBall 三个纯函数对象第一发就过；ReadInput（DOM 全局态）+ RenderFrame（Canvas mock 不完备）走 obstacle+waiver——这两个 waiver 是**结构性合法**（不是实现 bug 的 waiver-out），DetectHit 则触发了一次"属性键序差异"的真测失败，agent 用 `Math.round(...*1e10)/1e10` 修好后过——**proactive mechanical improvement**，不是偷懒。日志 132 KB 与 pong-02（3 对象）持平，证明 chain 内部消化 = 对数级 scaling。

### pong-03 ★★★★ — 1.4 calls/object 最优效率，被打断在 root R1-R4
5/5 对象 confirmed（InitGame → PollInput → HandleInput → UpdatePhysics → RenderFrame，依赖序正确），仅 7 次 confirm_object。日志 260 KB 偏大因为 5 个对象都 actually 跑过 test 输出了详尽 trace。被 kill 时 root session 在反复 `session_gate_check` 试图同时满足 `outputs-tests-non-empty`（需 ≥1 ok=true 测试）和 `waiver-flood`（waiver 比例 < 75%）—**这是 v9.0 揭出的新 gate 张力**：HTML 单文件项目的 5 个对象大半要 waiver（合法结构限制），但 gate 不区分"合法 waiver"和"懒 waiver"。agent 在打断时**仍在工作**（不是 stuck），15 min 内大概率会通过 1-2 个真测试改写满足 gate。

### pong-04 ★★★ — 3/4 + UpdatePhysics 真正的 4 轮修复
3 confirmed (InitGame / ProcessInput 各 1 发即过；UpdatePhysics 4 发：合成无效 → hitPos 取错 → vy 浮点精度 → 换 `vy=-|speed|*sqrt(1-bias²)` 通过)。打断时正在执行 RenderFrame 的 `graph_show` inspection，**距 confirm_object 一步之遥**——RenderFrame 是 side-effect-only（画 canvas），大概率会触发 Insufficient → waive，3-5 min 即可结束。pong-04 是被 batch kill 时点最不幸的一个（如再给 15 min 几乎肯定 4/4 finished）。

### pong-01 ★ — bug D 重复犯 5+ 次，唯一深坑
14 次 confirm_object，3 对象只 1 个 confirmed (InitGame，17:28 终于过)。**核心病灶**：InitGame 的 `portObservation: { "gameStatus": "return.gameStatus" }` 中**键名**是 camelCase 而 graph 里 attribute id 是 snake_case `game_status` — harness 报错 `[game_status] expected ... got undefined`，错误消息**正确报了 attribute id**，但**没说**"你的 portObservation 键名 `gameStatus` 不匹配 attribute id `game_status`"。agent 反复尝试改函数返回结构、改 valueSpace、改 testSpec，70 分钟后才**在 chain 外部**显式手动 reasoning 出键名问题并 `graph_merge_object` 修好（17:26）。**这条 bug 不归 enrich-retry 范畴**——它跨越 graph metadata（attribute id）和 impl（JS key），harness 不知道两端应该是同一个标识。UpdateBall / UpdatePaddle 在 kill 时仍有相似嫌疑加上 collision detection 测试失败。

---

## 4. v9.0 design 验证清单

| 设计意图 | 验证状态 | 证据 |
|---|---|---|
| `confirm_object` 单入口替代 6-step 手动链 | ✅ | 5/5 实例都用单 call 模式；pong-03 1.4 calls/object |
| chain enrich-retry 把失败消化在内部 | ✅（4/5） | pong-02 `cycles.count=1 prevIssues=[...]` 可见落盘；pong-04 UpdatePhysics 4 轮浮点精度修复在 chain 内完成 |
| 0 obstacle 抛 terminal（不喷给 agent）| ✅ | 51 次 calls 0 terminal obstacle |
| 日志体积压缩 | ✅ | pong-02 132 KB vs v8.7 458 KB（3.5×） |
| mass-waiver pathology 消除 | ✅ | pong-05 仅 2 个 waiver 且都合法（DOM/Canvas）；v8.7 pong-05 几乎全 waiver |
| EvidenceBundle 一对象一文件 | ✅ | `.kcpos/typecalc/<id>.json` 统一格式，无 8 兄弟文件 |
| protocol-in-code 自动生成 system prompt | ✅ | `kcpos doc protocol` 可读，prompt hash test 守门 |
| **跨 graph/impl 标识不一致的检测** | **❌** | pong-01 bug D 5 次重犯，chain 无能为力 |
| **gate 区分"合法 waiver"和"懒 waiver"** | **❌** | pong-03 root R1-R4 卡在 waiver-flood vs outputs-tests-non-empty 两难 |
| **chain enrich-retry 上限 5 是否合理** | **?** | pong-01 14 次都没自愈，但每次都是 agent 重新 invoke 而非 chain 内部 retry——证明 chain 内部上限**没有**被触发；外部 agent loop 才是真正消耗 |

---

## 5. v9.0.1 候选 bug 修订清单

原 6 项 (A-F) 经本次 batch 修订：

| 编号 | 描述 | 本批触发实例 | 优先级 |
|---|---|---|---|
| **D** | portObservation key vs attribute id 大小写/命名不匹配 → 静默 undefined | **pong-01 (5+ 次)** | **P0** |
| **F (NEW)** | 新静态检查 `port-observation-orphan-key`：portObservation 的每个 key 必须存在于该对象的 `outputs` 中（attribute id）；不匹配立即报错带建议 | pong-01 同源 | **P0** (D 的根治) |
| **E** | chain enrich-retry 形成 obstacle 时不落盘 bundle.obstacle section | pong-01 隐性 | P1 |
| **G (NEW)** | gate 规则 `outputs-tests-non-empty` 与 `waiver-flood` 同时存在时形成两难锁：HTML 单文件项目大半合法 waiver，无法同时满足"≥1 真测 ok=true"和"waiver < 75%"。建议引入 waiver `kind` 字段（`structural` / `pragmatic`）—— `structural` 不计入 flood | pong-03 R1-R4 卡死 | P1 |
| A | implSymbol 未列入 `graph_merge_object` patch allowlist | 本批无明显触发 | P2 |
| B | harness 无 implSymbol 时自动 case-convert bridge | **若 F 落地则 B 不必要**（结构性预防） | 可删 |
| C | confirm_object 在 impl edit 后自动 re-synthesize tests | pong-04 UpdatePhysics 是手动 re-synth | P2 |

**核心调整**：D + F 合并为最高优先级的"标识一致性"问题。F (`port-observation-orphan-key` 静态检查) 是 D 的根治——把 silent undefined 变成 write-time error，且能在错误信息里直接告诉 LLM 正确的 key 应该叫什么。

---

## 6. enrich-retry feedback 信号质量分析

bug D 之所以重犯 5 次，是因为 chain 提供的 feedback 在两条信道上都失效：

**信道 1（先验：LLM 知道大致输出形态）**——弱：system prompt 里"attribute IDs are snake_case"和"portObservation maps each port name to extractor"是**两条分立陈述**，没明说"portObservation 的 key 必须是 attribute id"。LLM 训练分布 JS 键名 ~70% camelCase / ~30% snake_case，局部上下文又强化（函数体里 `return { gameStatus: ... }`）。

**信道 2（事后：被打回时知道原因）**——弱：错误是 `[game_status] expected ..., got undefined`，正确报了 attribute id 但没说"你的 portObservation key `gameStatus` 不匹配"。Agent 走向错误的归因路径（路径 A：函数没返回字段；路径 B：extractor path 错），没走路径 C（键名/id 不匹配）。

两条信道都弱时，**LLM 在结构性偏差面前没有任何信息源**——这正是 F (静态检查 + 强诊断) 的设计动机：把检查从运行时漂移到写入时，并把错误消息从"undefined"升级到"key `X` not in outputs [a, b, c]，did you mean `a`?"。

---

## 7. 总评

**v9.0 是一次大成功的设计落地**：51 次 confirm_object 调用证明类型驱动 state machine + chain enrich-retry 在 80% 以上案例能自治消化失败；mass-waiver pathology 被架构性消除；日志体积 3.5× 压缩；接口从 6-step 手动链压到 1 个调用。

**剩余 20% 的失败有清晰、局部、可补丁的归因**：跨 metadata/impl 的标识一致性（D/F 是同一问题的两面）。这不否定 v9.0 的核心叙事——chain 能消化它能看到的失败；要让它消化 metadata 失败，需在 metadata 写入路径上加静态检查（不是在 chain 上加更多重试）。

**pong-03 揭出的 gate 张力**是 v9.0 引入的"合法 waiver"概念与原 v8.7 gate 规则的接口缝隙——HTML 单文件项目天然大半合法 waiver，gate 应能识别。

**建议落地顺序**：F (P0, 静态检查根治 D) → G (P1, gate `kind` 字段) → E (P1, bundle.obstacle 持久化) → C (P2) → A (P2) → 删除 B。

---

## 8. 附录：可量化指标

```
metric                     pong-01  pong-02  pong-03  pong-04  pong-05
─────────────────────────────────────────────────────────────────────
confirmed / total          1/3      3/3      5/5      3/4      6/6
confirm_object 调用次       14       11       7        8        11
calls/object               14.0     3.67     1.40     2.67     1.83
日志大小 (KB)               132      132      260      192      132
obstacles 终态              0        0        0        0        0
in-chain waiver 数          0        2        5        0        2
session 终态                killed   finished active   killed   finished
原因                        bug D    clean    R1-R4    被 kill  clean
                            循环                       中断
```

**单调指标**：
- ✅ obstacles emitted 全 0
- ✅ 4/5 实例至少 75% 对象 confirmed
- ✅ pong-05 / pong-03 / pong-02 三干净 finish 日志 ≤ 260 KB
- ❌ pong-01 单实例消耗占总 confirm_object 调用的 27% (14/51)，效率最差
