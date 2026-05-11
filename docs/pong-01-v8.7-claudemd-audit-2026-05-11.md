# pong-01 v8.7 执行过程 vs CLAUDE.md 合规审计

**Run**: pong-01, 2026-05-11, kcpos v8.7 (commit `19d32f8`)
**结果**: s_pingpong.status = `finished`, 3/3 confirmed, gate PASS
**用时**: ~52 min (启动 13:18 → finished 14:10 量级)
**资源**: 270 messages, 185 tool calls, 501 KB transcript

**最重要的对照**：v8.6 pong-01 在第 127 条消息死于 grep 6.8 MB 自爆，0/5 confirmed；**v8.7 pong-01 完成，3/3 confirmed**。

---

## 1. 协议合规度矩阵

| CLAUDE.md § | 协议要求 | pong-01 实际 | 合规 |
|---|---|---|---|
| §0 L0 | graph.json 节点声明 | 5 attrs + 3 objs created | ✓ |
| §0 L1 | impl + status=confirmed | 3/3 confirmed，impl=index.html | ✓ |
| §0 L2 | 单元/契约测试通过 | 1/3 ok=true；2/3 obstacle+waiver | ◐ |
| §0 L3 | integration.test + dist/bundle.js | 单文件项目无 bundle | ✗ N/A |
| §0 L4 | gameplayProof + checkpoint PASS + 聚合 | gameplayProof **0/11 未填**；finalVerdict=PASS；aggregated | ◐ |
| §3.0 | graph-edit 优先 | 全部通过 graph_* 工具 | ✓ |
| §4.1 | 命名规则 | InitGame/UpdateBall/UpdatePaddle (PascalCase) + attrs snake_case | ✓ |
| §4.3 | 单文件 ≤ 1500 行 | index.html 237 行 | ✓ |
| §5.1.1 #1 | 对象 confirmed | 3/3 | ✓ |
| §5.1.1 #2 | impl 文件非空 | index.html 6.8KB | ✓ |
| §5.1.1 #3 | tests 存在 | typecalc evidence 链完整 | ✓ |
| §5.1.1 #4 | 子 session 全 finished/删除 | 无子 session（path A）| ⚠ |
| §5.1.1 #5 | .impl.ts ≥ must items × 0.5 | 1 index.html / 11 must = 0.09 | ✗ |
| §5.1.1 #6 | checkpoint 两证齐全 | **11/11 codeProof + 0/11 gameplayProof** | ✗ |
| §5.1.1 #7 | 实际运行 gameplayProof | 完全未填 | ✗ |
| §5.1.1 #8 | output 聚合 | session_aggregate | ✓ |
| **§5.4** | **≥ 3 对象必须走路径 B** | **3 对象走了 path A**（root + 0 children）| **✗** |
| §5.5 R1 | 产物聚合 | YES | ✓ |
| §5.5 R2 | build + tests | 单文件无 build，tests 完成 | ◐ |
| §5.5 R3 | npm run snap → K/proofs/ | 完全跳过 | ✗ |
| §5.5 R4 | codeProof + gameplayProof 填齐 | 只填 code | ◐ |
| §5.5 R5 | 最终闸门 | gate PASS | ✓ |

合规度汇总：**11 ✓ / 4 ◐ / 5 ✗ / 1 ⚠ / 1 N/A**。

比 pong-02 多 2 个 ✗（path B 未走 + gameplayProof 未填），合规度略低。

---

## 2. 主要偏离点

### 2.1 §5.4 path B 路径选择错误（结构性偏离）

CLAUDE.md §5.4 硬阈值：
> "当前 session 的对象数 ≥ 3 个 或 累计 .impl.ts 行数预估 ≥ 400 行 或 task 跨越 ≥ 2 个需求文档章节 → **必须**走路径 B"

pong-01 有 3 个对象（InitGame, UpdateBall, UpdatePaddle），**恰好踩到阈值**。Agent 选择 path A：
```
s_pingpong (parent=None, children=[], status=finished)  ← 0 个子 session
```

对比 pong-02 同一规则下用了 path B（6 个子 session 虽是 shell 但形式上拆了）。pong-01 完全没拆。这意味着：
- 不能并行运行
- 单点失败回滚成本最高
- 一个 session 的 graphDiff 累计变大

但**输出结果质量没有受损**（这是因为 3 对象规模小，path A 反而效率高）。**协议设计假设 ≥3 即应拆，但实际 = 3 时收益有限**。建议 CLAUDE.md §5.4 阈值改为 "对象数 > 3"。

### 2.2 gameplayProof 完全未填（§5.5 R3 + §5.1.1 #6/#7）

11 个 must item 全部只填了 codeProof，gameplayProof 字段空。pong-02 同一批同样 v8.7 binary 填了 7/7。

**为何同一二进制不同行为**：
- pong-02 agent 检视了 `checkpoint_fill` 工具的新 description（包含 gameplay_proof 参数），主动调用了两次（一次填 code，一次填 gameplay）
- pong-01 agent 没注意到新参数，按旧形式只调一次填了 code，没回填 gameplay

这是 LLM 调用不稳定性的体现：v8.7 #6 是"提供能力但不强制"，**能力被发现+使用的概率取决于 agent 的工具描述阅读深度**。

### 2.3 defs/ 路径漂移（§2 命名约定）

CLAUDE.md §2 项目结构说 TypeScript 类型定义在 `K/defs/`。pong-01 实际：
- msg 15, 26: 写 `K/defs/*.ts`（正确）
- msg 125, 145, 159: 写 `defs/*.ts`（漂移到根目录）

两套并存。graph.json 里的 `def` 字段实际指向 `K/defs/`，但 msg 125 起的 `write_file defs/*.ts` 创建了第二份不被引用的副本。冗余但不破坏功能。

---

## 3. v8.7 #1 修复的关键验证（最重要）

**这是 v8.7 最关键的回归测试**：v8.6 pong-01 死于 OutputPorts 漏 Mutates。v8.7 #1 在 [typecalc.go:217](kcpos/internal/tools/typecalc/typecalc.go#L217) 修复了它。

观察 pong-01 v8.7 的 runtime trace：

```
UpdateBall (mutates=[ball_state, game_state], 144 calls):
  outputs keys: ['ball_state', 'game_state']           ← 不再是空集！
  outputs sample: {"ball_state": {"x":200,"y":100,...},
                   "game_state": {"status":"menu","score":5}}

UpdatePaddle (mutates=[paddle_state], 55 calls):
  outputs keys: ['paddle_state']                       ← 不再是空集！
  outputs sample: {"paddle_state": {"x":100,"width":100}}
```

v8.6 pong-01 同样的 mutates 模式下，outputs 永远空，触发 runtime-output-missing × N，agent 转去 unlink consume 又 link consume 试图自愈，最后在 grep 上自爆。

v8.7 outputs 正常 → agent 拿到真实测试反馈 → 实际打开测试失败的卷宗发现是 IEEE 754 / Math.random 范围这类**真实但非实现缺陷**的问题 → 提交 obstacle + waiver → 收尾。

**结论：#1 修复完全有效。** pong-01 v8.7 没有走到 v8.6 那条死路。

---

## 4. obstacle / waiver 质量分析

2 个 waiver 都是**factually grounded 的技术争议**，理由完全可验证：

### InitGame.waiver

> "3 failures are test-synthesizer range assumption errors: expects ball_state.vx in [0,7] but random angle can produce negative vx; expects ball_state.x in [100,300] but the formula produces [100,300); expects x=100 exactly but Math.random() always produces value in [0,1) making x in [100,200). These are test synthesis bugs, not implementation defects."

可信度评估：
- "Math.random() always in [0,1)" 是 JS 标准事实
- 测试期望 vx≥0 但随机角度生成 vx 双向 — 设计意图上 SPEC 没规定 vx 必须 ≥0
- 合规：**真实问题，不是 confabulation**

### UpdateBall.waiver

> "IEEE 754 rounding at the 16th significant digit (e.g., Math.cos(Math.PI/3) returns 0.5000000000000001 ≠ 0.5). All 14 non-floating-point tests pass."

可信度评估：
- IEEE 754 双精度精度极限是事实
- `Math.cos(Math.PI/3)` 在 V8/JSC 上确实返回 0.5000000000000001
- 合规：**真实问题，不是 confabulation**

两个 reason 的 normalized key 差异巨大（"3 failures are test-synthesizer range assumption errors" vs "IEEE 754 rounding at the 16th significant digit"），即使触发 v8.7 [waiver-flood] 也会通过 reason-diversity probe。

但是注意：**2/3 = 67%**。如果 totalConfirmed ≥ 4 且 waiver ≥ 3，v8.7 阈值要求 75%。pong-01 有 67% < 75%，且 totalConfirmed=3 < waiverFloodMin=4，**双重不触发**。这是设计意图——3 对象规模太小，没法做统计判断。

---

## 5. v8.7 修复在本实例的命中

| # | 修复 | 命中 | 实际效果 |
|---|---|---|---|
| 1 | OutputPorts ∪ Mutates | **是（核心）** | UpdateBall/UpdatePaddle mutates 端口在 outputs 出现 144+55 次。**v8.6 pong-01 死锁的根因被修掉了** |
| 2 | reviewer renderIssueAwareLog 注入 trace | **是** | 10 次 review 全 verdict=pass；reasonableness reasons 引用真实测试数据，无 pong-05-style hallucination |
| 3 | type:"enum" 友好降级 | 否 | 本实例 valueSpace 未使用 enum |
| 4 | typecalc-use hook 认 obstacle+waiver | **是** | msg 209 directly merge_object status=confirmed 没出现 v8.6 那种"先跑个假 typecalc_test 把文件壳生成出来"的弯路 |
| 5 | gate.go [waiver-flood] ≥75% | 否（正确不触发）| 2/3=67%，且 totalConfirmed=3 < 4 (双重未达阈值) |
| 6 | GameplayProof 字段 + tool param | **否（关键漏过）** | agent 没用新参数，11/11 must 项 gameplayProof 空 |

---

## 6. 工具调用与时间投入

```
27 graph_merge_attribute    21 typecalc_test         14 write_file
14 typecalc_describe        13 edit                  12 graph_merge_object
11 checkpoint_add_item      11 checkpoint_fill       10 typecalc_review
 6 bash                      6 typecalc_synthesize    5 graph_link_produce
 5 graph_create_attribute    4 graph_link_consume     3 graph_link_mutate
 3 graph_create_object       3 graph_validate         3 read_file
 2 typecalc_obstacle         2 typecalc_waive
```

**对比 v8.6 pong-01**:

| | v8.6 pong-01 | v8.7 pong-01 |
|---|---|---|
| msgs | 128 | 270 |
| tool calls | 91 | 185 |
| 转录 | **7.4 MB**（grep 自爆）| 501 KB |
| typecalc_test | 9 | 21 |
| 结局 | **死锁** | **finished** |
| confirmed | 0/5 | 3/3 |

资源消耗增加了一倍但**完成度从 0 跳到完整通过**。增加的工具调用主要来自 21 次 typecalc_test 迭代（v8.6 只有 9 次，但全是空 outputs），现在每次 test 拿到真实数据再迭代。

---

## 7. 5 个实例 v8.7 中间结果（pong-01 / pong-02 已完成）

| 实例 | 状态 | 评级 | gameplayProof | 主要偏离点 |
|---|---|---|---|---|
| **v8.7 pong-01** | **finished** | ★★★ | **0/11**（v8.7 #6 漏过）| Path A 用错，gameplayProof 字段不知 |
| v8.7 pong-02 | finished | ★★★★ | 7/7 描述性 | Path B 名实分离，K/proofs/ 跳过 |
| v8.7 pong-03 | 跑中 | — | — | — |
| v8.7 pong-04 | 跑中 | — | — | — |
| v8.7 pong-05 | 跑中 | — | — | — |

**pong-01 vs pong-02 路径质量差异**：同样 v8.7 binary，pong-02 用了 gameplayProof 参数、pong-01 没用。说明 v8.7 #6 是"机会型"而非"强制型"修复。如果想要可重复合规，需要：
- 在 system.md / tool description 中更强调
- 或在 gate 加入软提示（不 FAIL 但 warn）

---

## 8. 结论

**v8.7 修复 #1 在本实例完美生效**——pong-01 没有重蹈 v8.6 的覆辙。这是本批次最关键的回归测试。

**剩余偏离**集中在两点：
1. **gameplayProof 字段被 agent 视而不见** —— 提供了能力但 agent 不一定会用。可考虑下次让 gate 在 codeProof 已填但 gameplayProof 空时输出 hint（非 FAIL）
2. **path B 阈值边界（=3）** —— CLAUDE.md 写"≥3"但 agent 选 path A，实际效果也不坏。建议 CLAUDE.md 改写"> 3 必须 path B；= 3 可选"，让规则更贴合实际成本

**评级理由 ★★★**：
- 完成度高（gate PASS, 3/3 confirmed）
- 证据真实（reviewer pass、obstacle reason 都是技术性的）
- 但 gameplayProof 未填 + path B 未走，两项明显偏离 CLAUDE.md 让等级不到 4 星
