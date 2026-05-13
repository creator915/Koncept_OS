# pong-03 v8.7 执行过程 vs CLAUDE.md 合规审计

**Run**: pong-03, 2026-05-11, kcpos v8.7 (commit `19d32f8`)
**结果**: s_pong.status = `finished`, 4/4 confirmed, gate PASS（经 **8 轮** gate_check）
**用时**: ~82 min（启动 13:18 → finished ~14:40 量级）
**资源**: **367 messages, 247 tool calls, 1275 KB transcript**（**本批最重**）

**最重要的发现**：**pong-03 触发了 v8.7 #5 [waiver-flood] 节流**，是本批唯一触发的实例。Agent 正确响应——把一个 waivered 对象转回真实测试评据，最终通过 gate。**v8.7 #5 按设计意图工作**。

---

## 1. 协议合规度矩阵

| CLAUDE.md § | 协议要求 | pong-03 实际 | 合规 |
|---|---|---|---|
| §0 L0 | graph 节点声明 | 6 attrs + 4 objs | ✓ |
| §0 L1 | impl + confirmed | 4/4 confirmed，impl=index.html | ✓ |
| §0 L2 | 单元测试通过 | 2/4 ok=true（InitGame, UpdatePhysics）；2/4 obstacle+waiver | ◐ |
| §0 L3 | integration + bundle | 单文件无 bundle | ✗ N/A |
| §0 L4 | gameplayProof + PASS + 聚合 | **0/8 gameplayProof**；PASS；aggregated | ◐ |
| §3.0 | graph-edit 优先 | 全部通过 graph_* 工具 | ✓ |
| §4.1 | 命名规则 | PascalCase 对象 + snake_case 属性 | ✓ |
| §4.3 | 单文件 ≤1500 行 | index.html 248 行 | ✓ |
| §5.1.1 #1-4 | 对象/impl/test/子 session | 全齐 | ✓ |
| §5.1.1 #5 | .impl.ts ≥ must × 0.5 | 1 index.html / 8 must = 0.13 | ✗ |
| §5.1.1 #6 | checkpoint 两证齐全 | **8/8 codeProof + 0/8 gameplayProof** | ✗ |
| §5.1.1 #7 | 实际运行 gameplayProof | 未填 | ✗ |
| §5.1.1 #8 | output 聚合 | YES | ✓ |
| **§5.4** | **≥ 3 对象走 path B** | **4 对象走 path A**（root + 0 children）| **✗** |
| §5.5 R1 | 产物聚合 | YES | ✓ |
| §5.5 R3 | npm run snap → K/proofs/ | 完全跳过 | ✗ |
| §5.5 R4 | 两证齐全 | 仅 codeProof | ◐ |
| §5.5 R5 | 最终闸门 | **8 轮 gate_check 后** PASS | ◐ |

合规度汇总：**10 ✓ / 4 ◐ / 5 ✗ / 1 N/A**

---

## 2. **v8.7 #5 [waiver-flood] 实战验证**（本报告核心）

### 2.1 触发条件复盘

`(waivered * 4) >= (total * 3)` 且 `total >= 4`：

| 时刻 | total confirmed | waiver | 比例 | 触发? |
|---|---|---|---|---|
| 第一阶段 | 4 | 3 (ProcessInput, RenderFrame, UpdatePhysics 都有 obstacle+waiver) | 75% (12 >= 12) | **是** |
| 修复后 | 4 | 2 (ProcessInput, RenderFrame) | 50% (8 >= 12 false) | 否 ✓ |

### 2.2 Agent 的响应（transcript msg 2525-2548 摘录）

```
[14:37:21] Agent: "Still waiver-flood: 3/4. The gate wants at least one more object with
                   clean review (no obstacle). InitGame is the only one [already clean]."

           "I need to get either UpdatePhysics or RenderFrame to pass review
            without obstacle. The only way is to fix the runtime issues."

[14:37:29] » bash({"command": "rm -f .kcpos/typecalc-runtime/*.json"})

[14:37:42] » typecalc_test({"object_id": "RenderFrame"})
           » typecalc_test({"object_id": "UpdatePhysics"})
[14:37:45]   ↳ typecalc_test UpdatePhysics: TestedPass
             ↳ typecalc_test RenderFrame: TestedPass

[14:37:57] » bash({"command": "rm -f .kcpos/typecalc-evidence/UpdatePhysics.obstacle.json
                                       .kcpos/typecalc-evidence/RenderFrame.obstacle.json"})
```

**v8.7 #5 完美生效**：
1. Gate 输出明确（"waivered/total ≥ 75% — convert at least N of these to real typecalc_test"）
2. Agent 读懂并执行——把 2 个 waivered 对象转回真测试
3. 测试这次通过（TestedPass × 2），删掉 obstacle.json
4. 再 gate_check 通过

**这是 v8.7 修复链路设计意图的完整闭环**：waiver 是 escape hatch 但有上限，达到上限时 agent 必须**真的解决问题**而不是堆 waiver。

### 2.3 副作用揭示的 v8.8 候选 bug：runtime trace 累积污染

Agent 在 transcript msg 2510 写道：
> "I think the issue is that the runtime trace accumulates entries over multiple test runs. Old entries with `__side_effect__` persist alongside new entries."

这是真实 bug：`typecalc-runtime/<id>.json` 在每次 `typecalc_test` 时**追加**而非**覆盖**。Agent 改了 portObservation 从 `'side_effect'` 改成 `'global'` 后，trace 文件里仍残留旧的 `'__side_effect__'` 条目，触发 246 条 `runtime-enum-violation`。

Agent 解决方法：**手动 `rm` 整个 trace 文件**再重跑。

v8.8 候选修复：
- `harness.go` 在写入 trace 时按 implHash 过滤，丢弃旧 hash 的条目
- 或 `typecalc_test` 启动时清空 trace 文件（implHash 自动重新生成）

---

## 3. v8.7 修复在本实例的命中

| # | 修复 | 命中 | 实际效果 |
|---|---|---|---|
| 1 | OutputPorts ∪ Mutates | **是（核心）** | UpdatePhysics 的 mutates=[ball,score,game_status,paddle] 全部在 outputs 出现（13 次 calls），RenderFrame mutates=[canvas_config] 也在 outputs |
| 2 | reviewer trace 注入 | **是** | UpdatePhysics + InitGame verdict=pass；ProcessInput 虽然 ok=False（trace missing）但 verdict=pass with factual reasoning |
| 3 | type:"enum" 友好降级 | **是（部分）** | game_status 用了 canonical enum，runtime-enum-violation 仍出现但只 1 条（"paused" 不在 [playing, game_over]），无 80 条假阳性 |
| 4 | typecalc-use hook 认 obstacle+waiver | **是** | 4 个对象 status=confirmed 顺畅 |
| 5 | **[waiver-flood] ≥75%** | **是（首次实战触发）** | 阈值精确命中（3/4=75%），agent 正确响应转回真测试 |
| 6 | GameplayProof 字段 + tool param | **否** | 0/8 must 项 gameplayProof 空。Agent 与 pong-01 一样没发现新参数 |

**5/6 修复在本实例命中**——是 v8.7 修复触发率最高的实例。

---

## 4. obstacle/waiver 质量分析

### ProcessInput.waiver (event listeners)

> "ProcessInput attaches keyboard event listeners to window; the CANNOT_SYNTHESIZE verdict is expected because the test harness cannot dispatch synthetic keyboard events. ArrowLeft/ArrowRight set input_direction correctly, Space on game_over triggers InitGame, and keyup resets direction."

**Factual, specific** —— 与 pong-05 ProcessInput 同模式（DOM 事件异步）。Synthesizer 实际返回了 CANNOT_SYNTHESIZE，所以 .tests.json 不存在，trace-missing 是结构性后果。

### RenderFrame.waiver (side_effect / trace pollution)

> "All 196 runtime port-signal issues report '__side_effect__' values for ports declared with portObservation='side_effect'. This is BY DESIGN: RenderFrame produces its output on the HTML canvas element, not through graph-observable ports."

**Factual** —— 引用了真实数字（196 issues），承认 canvas 渲染本质 side_effect。Reviewer 信任 reasonableness verdict=pass。

第二轮 obstacle 引用 reviewer 输出："Review reasonableness is pass (confidence 0.95), static check clean" —— **v8.7 #2 的设计目标达成**：reviewer 给出的可信判断可被 agent 直接引用作 obstacle 依据。

两条 normalized key 完全不同。

---

## 5. 工具调用与时间投入

```
31 typecalc_test         24 typecalc_review        20 graph_merge_attribute
19 typecalc_describe     18 bash                   17 edit
12 write_file            11 graph_link_consume     9 read_file
 9 graph_merge_object     9 typecalc_synthesize    8 checkpoint_add_item
 8 checkpoint_fill        8 session_gate_check     6 graph_create_attribute
 6 graph_link_produce     6 graph_link_mutate      6 typecalc_obstacle
 4 graph_create_object    3 typecalc_waive         2 glob
 2 list_dir               1 grep                   1 session_aggregate
```

**8 次 session_gate_check** —— 最多。Agent 不断试图 pass，每次 gate 反馈具体问题，agent 修复后再试。

**对比 v8.6 pong-03**：

| | v8.6 pong-03 | v8.7 pong-03 |
|---|---|---|
| msgs | 288 | **367** |
| tool calls | 196 | 247 |
| 转录 | 412 KB | **1275 KB** |
| typecalc_test | 14 | **31** |
| typecalc_review | 14 | 24 |
| gate_check | 1 | **8** |
| confirmed | 4/4 | 4/4 |
| waivered | 2/4 (50%) | 2/4 (50%, 但中途 3/4 被 [waiver-flood] 拦住) |
| gameplayProof | 0/8 | 0/8（未发现 #6）|

v8.7 pong-03 **工作量翻倍**（转录 3 倍大）。原因：v8.7 #5 在第一轮把它拦住强制重测，agent 跑了第二轮完整循环。**这是健康行为**——之前 v8.6 没有此限制，pong-05 走 4/4 waiver 直接逃生。

---

## 6. 主要偏离点

### 6.1 §5.4 path B 又一次违规

4 个对象走 path A（root.children=[]）。这是 v8.7 批 4 个完成实例里**第 3 次**违规（pong-01 / pong-05 / pong-03）。pong-02 是唯一形式上走 path B 的，但子 session 是 shell。**path B 在 v8.7 批中实际零次正确执行**。

### 6.2 gameplayProof 未填（同 pong-01）

8 个 must 项全部 codeProof only。再次说明 v8.7 #6 是"机会型"——pong-02/05 用了，pong-01/03 没用。同 binary 同 system.md，命中率不稳定。

### 6.3 8 轮 gate_check（最多）

迭代非常多。每次 gate fail 提示具体问题，agent 修复后再试。**这正是 gate 设计期望**——给出可操作的失败原因。但 8 轮也说明 agent 在循环内没把所有依赖一次理清。

---

## 7. 5 个实例 v8.7 完成结果

**pong-04 仍在跑**。已完成 4 个的对照：

| 实例 | 状态 | 评级 | 触发 v8.7 修复 | gameplayProof | 主要问题 |
|---|---|---|---|---|---|
| v8.7 pong-01 | finished | ★★★ | #1, #2, #4 | 0/11 | path A，gameplayProof 漏过 |
| v8.7 pong-02 | finished | ★★★★ | #1, #2, #4, #6 | 7/7 描述性 | path B shell-only |
| **v8.7 pong-03** | **finished** | **★★★★** | **#1, #2, #3, #4, #5, #6**(部分) | **0/8** | path A，#5 触发后修复成功，trace 累积 bug |
| v8.7 pong-05 | finished | ★★★ | #1, #2, #3, #4, #6 | 10/10 描述性 | path A，camelCase 函数名，reviewer 部分仍幻觉 |
| v8.7 pong-04 | 跑中 | — | — | — | — |

**pong-03 是 5/6 修复触发率最高的实例**，且唯一触发 #5（最复杂的协议层修复）。评级 ★★★★：完成度高，关键 v8.7 修复全部验证，唯一减分项是 path B 未走 + gameplayProof 漏过。

---

## 8. 结论 & v8.8 候选清单

### v8.7 在 pong-03 的关键证明

**#5 [waiver-flood] 工作完美**：阈值精确（75%），消息可操作，agent 响应正确（从堆 waiver 转为修底层）。最终结果从 v8.6 pong-05 模式（mass waiver 通过）回到健康模式（实际证据通过）。

### 新发现的 v8.8 候选 bug

1. **runtime trace 累积污染**（[harness.go appendTrace](kcpos/internal/typecalc/harness/harness.go)）—— trace 文件多次 test 后旧 implHash / 旧 portObservation 的条目残留。修复：按 currentImplHash 过滤，或启动 test 时清空。
2. **gameplayProof "机会型" 改善** —— 2/4 完成实例（pong-01/03）漏过。可能 system.md 关于 `checkpoint_fill` 的描述需要更显著地点出 `gameplay_proof` 参数。
3. **path B 实际执行率 0%** —— 4/4 完成实例 path B 都未真正用。可能需要 system.md / CLAUDE.md 把"≥3 对象 → spawn_subagent"写得更强（而不仅是 session_create）。
4. **gate 失败信息更早** —— 8 轮 gate_check 表明 agent 缺少早期反馈通道。或许 graph_merge_object 后即时跑部分 gate 子检查。
