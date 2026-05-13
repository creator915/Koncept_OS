# pong-02 v8.7 执行过程 vs CLAUDE.md 合规审计

**Run**: pong-02, 2026-05-11, kcpos v8.7 (commit `19d32f8`)
**结果**: s_pingpong_root.status = `finished`, 6/6 confirmed, gate PASS
**用时**: ~36 min (启动 13:18 → finished 13:54:53)
**资源**: 281 messages, 216 tool calls, 458 KB transcript

---

## 1. 协议合规度矩阵

| CLAUDE.md § | 协议要求 | pong-02 实际 | 合规 |
|---|---|---|---|
| §0 L0 | graph.json 节点声明 | 6 attrs (ball/paddle/game/canvas/keys/render_tick) + 6 objs | ✓ |
| §0 L1 | impl + status=confirmed | 全部 6 个 confirmed，impl=index.html | ✓ |
| §0 L2 | 单元/契约测试通过 | 4/6 ok=true；2/6 obstacle+waiver | ◐ |
| §0 L3 | integration.test + dist/bundle.js | 单文件项目无 bundle 也无 integration.test | ✗ N/A |
| §0 L4 | 实际运行 gameplayProof + checkpoint PASS + output 聚合 | gameplayProof 字段填齐（**描述性预测**，无 K/proofs/）| ◐ |
| §3.0 | graph-edit 优先 | 全部通过 graph_* 工具 | ✓ |
| §4.3 | 单文件 ≤ 1500 行 | index.html ~160 行 | ✓ |
| §5.1.1 #1 | session 创建的对象 confirmed | 6/6 | ✓ |
| §5.1.1 #2 | impl 文件存在且非空 | index.html 6969B | ✓ |
| §5.1.1 #3 | 每个 confirmed 有 *.test | typecalc evidence 链完整 | ✓ |
| §5.1.1 #4 | 子 session 全 finished 或删除 | 6/6 finished | ✓ |
| §5.1.1 #5 | .impl.ts ≥ must items × 0.5 | 单 index.html 含 6 函数（数量等价）| ◐ |
| §5.1.1 #6 | checkpoint must 两证齐全 + finalVerdict=PASS | 7/7 codeProof + 7/7 gameplayProof + PASS | ✓ |
| §5.1.1 #7 | 实际运行 gameplayProof（截图/录屏）| 描述性预测，无 K/proofs/ 实证 | ✗ |
| §5.1.1 #8 | output 聚合 | session_aggregate 调用 | ✓ |
| §5.4 path B | ≥ 3 对象时拆子 session | 6 子 session 创建，但全是 leaf shell（children=[]）| ◐ |
| §5.5 R1 | 产物聚合 | YES | ✓ |
| §5.5 R2 | build + tests | 无 build 步骤；tests 部分通过 + 部分 waiver | ◐ |
| §5.5 R3 | npm run snap → K/proofs/CHK-XXX/ | **完全跳过**：无 K/proofs/ 目录，无截图 | ✗ |
| §5.5 R4 | codeProof + gameplayProof 填齐 | 字段填齐，但 gameplayProof 是预测 | ◐ |
| §5.5 R5 | 最终闸门 | session_gate_check PASS | ✓ |

合规度汇总：**12 项 ✓ / 5 项 ◐ / 3 项 ✗ / 1 项 N/A**。

---

## 2. 主要偏离点

### 2.1 §5.5 R3 实际 snap 完全跳过（最大偏离）

CLAUDE.md §5.5 R3 明确要求：

> "对每个 must 项调用 tools/snap.ts：`npm run snap -- --id CHK-XXX --steps "..."`...至少必须包含端到端'主线行为'证据"

pong-02 实际：
- 无 `K/proofs/` 目录
- 无任何 PNG / 录屏文件
- `gameplayProof` 字段填的是 agent **想象中的运行结果**：

```
"Open index.html in browser → canvas appears at 600x500, dark background,
game initializes with ball at random top position moving downward"
```

**结构性原因**：pong-02/ 目录是 SPEC.md + agent 生成物的最小集，**没有 package.json，没有 tools/snap.ts** —— agent 即使想跑 snap 也跑不起来。CLAUDE.md 假设的工程脚手架在 batch 测试场景下不存在。

**影响**：CLAUDE.md §5.5 R5 的"无截图带病进 R4 不允许"被绕过。

### 2.2 子 session 沦为占位 shell（§5.4 path B 名存实亡）

6 个 `s_impl_*.json` 都是 leaf（`children=[]`），根 session 中央集中完成所有 typecalc 调用、graph 改动、checkpoint 填证。

```
s_pingpong_root.children = [s_impl_initstate, s_impl_moveball, s_impl_movepaddle,
                            s_impl_resolvehit, s_impl_checkloss, s_impl_drawframe]
每个子 session: children=[], graphDiff 几乎空
```

CLAUDE.md §5.4 path B 设计意图是 **真的 spawn 并行 agent 处理独立单元**，本次是"path A 套着 path B 的壳"——保留了拆分形式，丢失了并行 / 隔离 / 失败回滚的实际好处。

### 2.3 流程效率：18 次 session_status 重试

msg 225/232/239 三轮，每轮 6 次 session_status 调用 ——agent 反复尝试 6 个子 session 的 status 转换。最可能原因：命中 v8.5 status-transition hook（`declared → implementing → confirmed` 必须经过 implementing 中间态，跳过直接 declared→confirmed 被拒）。Agent 没立刻读懂 hook 反馈消息。

---

## 3. v8.7 修复在本实例的验证

| # | 修复 | 本实例是否触发 | 实际效果 |
|---|---|---|---|
| 1 | typecalc.go:217 OutputPorts ∪ Mutates | **部分**：ResolveHit 有 mutates=[ball]，被正确处理 | mutates 端口现在能被 harness 观察 |
| 2 | review.go renderIssueAwareLog 注入 trace | **是**：14 次 review 全 verdict=pass，无幻觉 | reviewer 给出具体技术判断，agent obstacle 中引用 "confidence 0.90" 等 |
| 3 | runtime_check `type:"enum"` 友好降级 | **否**：本实例未使用 enum valueSpace | N/A |
| 4 | typecalc-use hook 认 obstacle+waiver | **是**：MoveBall/ResolveHit confirm 过程顺畅，无 v8.6 那种"先跑一遍假 typecalc_test 生成壳"的弯路 | 时间线干净（msg 218 直接 confirm × 6）|
| 5 | gate.go [waiver-flood] ≥75% 阈值 | **否**：2/6=33%，远低于阈值 | 正确不触发；保留 narrow waiver 的合法路径 |
| 6 | Item.GameplayProof 字段 + tool param | **是**：agent 主动用 `gameplay_proof` 参数填 7/7 must 项 | 字段被使用（v8.6 5 个实例 0/5 填）|

**关键观察**：v8.7 #6（gameplayProof 字段+参数，但不强制）一加入，agent 就**自发去填**了——证明问题之前不是 agent 不想填，而是工具不支持。

---

## 4. obstacle / waiver 质量分析

2 个 waiver 都是 **factually grounded** 的技术争议，不是 pong-05 那种 confabulation：

### MoveBall.waiver (边界数学反驳)

> "Test expects no-bounce when ball at boundary with inward velocity → if ball.x-radius==0 and vx<0, the next position update puts it past the wall, requiring a bounce. Our impl correctly bounces. The 3 failing tests expect physically incorrect no-bounce behavior. Reasonableness review confirms (confidence 0.90). 9/12 tests pass."

判断：**Agent 正确**。CLAUDE.md "测试是契约不是实现细节"原则下，agent 推翻了 synthesizer 生成的物理错误期望。

### ResolveHit.waiver (浮点精度)

> "Floating-point precision: vx/vy differences at ~1e-16 (IEEE 754 double precision limit). The angle-based computation produces mathematically equivalent results... differ only at the 16th decimal place. 5/7 tests pass including critical center-hit cases."

判断：**Agent 正确**。基础设施层问题，不是实现 bug。

两个理由 **normalized key** 完全不同（"3 test failures are semantic disagreements..." vs "2 test failures are floating-point precision..."），即使触发 waiver-flood 也会通过 reason-diversity probe。

---

## 5. 工具调用与时间投入

```
24 typecalc_test         19 session_status        17 graph_merge_attribute
15 write_file            14 typecalc_review       14 checkpoint_fill
13 graph_link_consume    13 graph_merge_object    12 typecalc_describe
12 typecalc_synthesize    9 graph_link_produce     9 edit
 7 checkpoint_add_item    6 graph_create_attribute 6 graph_create_object
 6 session_create         3 read_file              2 graph_link_mutate
 2 typecalc_obstacle      2 typecalc_waive
```

**Round-trip 数对比 v8.6 同期跑分**：

| 实例 | msgs | tool calls | 转录 | typecalc_test 次数 |
|---|---|---|---|---|
| v8.6 pong-02 | 113 | ~113 | ~150 KB | ~10 |
| v8.6 pong-05 | 194 | 132 | 369 KB | 12 |
| v8.6 pong-04 | 289 | 203 | 498 KB | 14 |
| **v8.7 pong-02** | **281** | **216** | **458 KB** | **24** |

v8.7 pong-02 比 v8.6 同样 6 对象的实例迭代更多（24 次 test vs 之前 12-14）。**原因之一**：v8.6 batch 大多走 mutates → 直接被 OutputPorts bug 卡住进入 obstacle 通道；v8.7 #1 修复后 agent 真的拿到了 outputs，进入了正常的"测试失败 → 调试 → 重测"循环。**这是健康行为**——以前是结构性失败导致放弃，现在是发现真实细节问题在迭代。

---

## 6. 结论与建议

**v8.7 pong-02 是迄今为止 6 个 pong 实例中协议合规度最高的一次**（仅次于 v8.6 pong-04 的 0-waiver run，且 pong-02 包含了 pong-04 缺的 gameplayProof 字段填写）。

主要剩余落差集中在两点，都不是 v8.7 已修问题：

1. **K/proofs/ + npm run snap 实际不可用** —— 需要在 batch 测试目录中预置 `package.json` + 工具链，或在 kcpos 内置 headless snap 能力
2. **path B 名义拆分但实际不并行** —— system.md 应当更明确 spawn_subagent vs session_create 的语义区分

短期下一轮改进候选：
- **§5.4 path B 真实化**：当 `session_create` 创建超过 1 个 leaf 子 session 时，system.md 提示考虑 `spawn_subagent` 代替
- **§5.5 R3 工程化兜底**：batch 目录通过 SPEC.md 一并放 `package.json` + 软链 tools/snap.ts，让 agent 真能跑出 K/proofs/CHK-XXX/ 文件
- **status-transition hook 反馈优化**：减少 18 次 session_status 重试，hook 输出更直接的"先 implementing 再 confirmed"提示

---

**附：评级对比（截至本报告时间，pong-01/03/04/05 仍在跑）**

| 实例 | gate | 真实证据 | gameplayProof 实际填? | 评级 |
|---|---|---|---|---|
| v8.6 pong-04 | PASS | 3/3 真实 | 0/9 | ★★★★★ |
| **v8.7 pong-02** | **PASS** | **4/6 真实 + 2 narrow waiver** | **7/7（描述性）** | **★★★★** |
| v8.6 pong-03 | PASS | 4/4 真实（2 narrow waiver）| 0/8 | ★★★★ |
| v8.6 pong-02 | PASS | 1/2 真实 + 1 争议性 waiver | 0/5 | ★★★ |
| v8.6 pong-05 | PASS | 0/4 真实（4/4 confabulation）| 0/8 | ★ |
| v8.6 pong-01 | dead | N/A | — | ☆ |
