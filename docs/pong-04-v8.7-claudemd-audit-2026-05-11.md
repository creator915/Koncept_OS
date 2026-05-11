# pong-04 v8.7 执行过程 vs CLAUDE.md 合规审计

**Run**: pong-04, 2026-05-11, kcpos v8.7 (commit `19d32f8`)
**结果**: s_pong_root.status = `finished`, 4/4 confirmed, gate PASS（**仅 2 轮** gate_check）
**用时**: ~85 min（启动 13:18 → finished ~14:43 量级）
**资源**: 319 messages, 222 tool calls, 556 KB transcript

**最重要发现**：**pong-04 是 v8.7 批次唯一真正执行 CLAUDE.md §5.4 path B 的实例**——`session_start × 4` + `spawn_subagent × 4 parallel` + `graph_merge_object session_id=...` 全套都用上了。**5 个实例里第一次见到完整 path B 闭环**。

---

## 1. 协议合规度矩阵

| CLAUDE.md § | 协议要求 | pong-04 实际 | 合规 |
|---|---|---|---|
| §0 L0 | graph 节点声明 | 5 attrs + 4 objs | ✓ |
| §0 L1 | impl + confirmed | 4/4 confirmed，impl=index.html | ✓ |
| §0 L2 | 单元测试通过 | **3/4 ok=true（InitGame, UpdatePaddle, UpdateBall）**；1/4 obstacle+waiver (Render) | ✓ |
| §0 L3 | integration + bundle | 单文件无 bundle | ✗ N/A |
| §0 L4 | gameplayProof + PASS + 聚合 | 0/7 gameplayProof；PASS；aggregated | ◐ |
| §3.0 | graph-edit 优先 | 全部通过 graph_* 工具 | ✓ |
| §4.1 | 命名规则 | PascalCase + snake_case，**impl 函数名与对象 ID 一致**（无 pong-05 camelCase 问题）| ✓ |
| §4.3 | 单文件 ≤1500 行 | index.html ~200 行 | ✓ |
| §5.1.1 #1-4 | 对象/impl/test/子 session | 全齐 | ✓ |
| §5.1.1 #5 | .impl.ts ≥ must × 0.5 | 1 index.html / 7 must = 0.14 | ✗ |
| §5.1.1 #6 | checkpoint 两证齐全 | **7/7 codeProof + 0/7 gameplayProof** | ✗ |
| §5.1.1 #7 | 实际运行 gameplayProof | 未填 | ✗ |
| §5.1.1 #8 | output 聚合 | session_aggregate × 1 | ✓ |
| **§5.4** | **≥ 3 对象走 path B（真正委派）** | **session_start × 4 + spawn_subagent × 4 + session_id 属归** | **✓ (唯一)** |
| §5.5 R1 | 产物聚合 | YES | ✓ |
| §5.5 R3 | npm run snap → K/proofs/ | 完全跳过 | ✗ |
| §5.5 R4 | 两证齐全 | 仅 codeProof | ◐ |
| §5.5 R5 | 最终闸门 | **2 轮 gate_check** PASS | ✓ |

合规度汇总：**12 ✓ / 2 ◐ / 4 ✗ / 1 N/A** —— **5 个实例中合规度最高**。

---

## 2. 关键差异：真正的 path B（v8.7 批唯一）

### 2.1 时间线

```
msg 58:  session_start s_pong_root              ← 创建根
msg 62:  session_start s_impl_initgame          ← 4 个并行
         session_start s_impl_updatepaddle
         session_start s_impl_updateball
         session_start s_impl_render

msg 147: spawn_subagent × 4 parallel batch     ← 真委派！
   "Run the full typecalc chain for object InitGame: typecalc_describe → ..."
   "Run the full typecalc chain for object UpdatePaddle: typecalc_..."
   "Run the full typecalc chain for object UpdateBall: typecalc_..."
   "Run the full typecalc chain for object Render: typecalc_describe → ..."

msg 226: graph_merge_object id=InitGame session_id=s_impl_initgame
         graph_merge_object id=UpdatePaddle session_id=s_impl_updatepaddle
         graph_merge_object id=UpdateBall session_id=s_impl_updateball
         graph_merge_object id=Render session_id=s_impl_render
                                       ↑ diff 准确归到子 session

msg 259: session_focus s_pong_root              ← 收尾切回根
```

### 2.2 子 session 实际承载工作

```
s_impl_initgame:    added=0, modified=1  (InitGame 状态变更)
s_impl_render:      added=0, modified=4  (render_output attribute + Render 多次)
s_impl_updateball:  added=0, modified=1  (UpdateBall 状态变更)
s_impl_updatepaddle:added=0, modified=1  (UpdatePaddle 状态变更)
s_pong_root:        added=0, modified=1, impls=1
```

**不是 shell**：每个子 session 都有真实的 graphDiff 内容，状态变更精确归属。对比 pong-02 的 children=[] shell-only（diff 都在根 session）—— pong-04 是真正的 distributed work。

### 2.3 CLAUDE.md §5.4 path B 全要点对照

| Path B 步骤 | pong-04 实现 |
|---|---|
| 1. 设计子结构（细对象 + 中间属性）| msg ~30-60 设计 4 对象 + 5 属性 |
| 2. checker validate（拆分平衡）| msg 156 graph_validate |
| 3. 提取依赖、拓扑排序 | 4 个对象无强依赖（除 Render 消费其他）|
| 4. **parallel:preflight** | msg ~160 `graph_preflight` × 1 |
| 5. 为每个对象创建子 session | msg 62 session_start × 4 |
| 6. **按顺序/并行执行子 session** | **msg 147 spawn_subagent × 4 并行**（唯一真委派）|
| 7. 每个子 session 完成后 validate | msg ~200 graph_validate × 1 |
| 8. 所有子 session finished → 父 session | msg 259 session_focus s_pong_root |
| 9. **集成测试 + R1-R5** | session_aggregate + checkpoint + gate ✓ |

**这是 5 个实例里唯一完整执行 path B 流程的运行**。

---

## 3. 证据质量分析

### 3.1 3/4 truly clean

| 对象 | ok | verdict | static | rt | 备注 |
|---|---|---|---|---|---|
| InitGame | true | pass | 0 | 0 | reviewer 引用具体细节 |
| UpdatePaddle | true | pass | 0 | 0 | "moves left/right using keys and dt at fixed speed" |
| UpdateBall | true | pass | 0 | 0 | "wall bounces, paddle collision, score, game-over detection" |
| Render | false | pass | 2 | 0 | obstacle+waiver for canvas ctx |

reviewer reasons 都是事实陈述，无 hallucination。

### 3.2 Render obstacle/waiver（唯一 waiver）

> "Render function requires a browser CanvasRenderingContext2D which is not available in the Node.js test runner environment. The function draws to canvas using fillStyle, fillRect, arc, fillText etc. — all of which require a real DOM canvas context."

**Factual** —— 同 pong-05 DrawFrame 的诊断，且更简洁。

normalized key 单独存在，与其他无重复。

### 3.3 1/4 = 25% waiver rate

- 远低于 [waiver-flood] 75% 阈值，不触发
- 与 pong-04 v8.6（0/3=0%，完全无 waiver）相比略高，但 v8.7 版本多了 Render 这个 side_effect-only 对象
- 整体 waiver 使用克制——只在结构性必要时使用

---

## 4. v8.7 修复在本实例的命中

| # | 修复 | 命中 | 实际效果 |
|---|---|---|---|
| 1 | OutputPorts ∪ Mutates | **是** | UpdateBall 的 mutates=[game_state] 在 outputs 出现（93 calls）|
| 2 | reviewer trace 注入 | **是** | 9 次 review 全 verdict=pass，含 ok=false 的 Render 也 pass（因为 reviewer 看到测试失败的根因是结构性而非代码缺陷）|
| 3 | type:"enum" 友好降级 | **否** | 未用 enum |
| 4 | typecalc-use hook 认 obstacle+waiver | **是** | Render confirm 顺畅 |
| 5 | [waiver-flood] ≥75% | **否（正确不触发）** | 25% 远低于阈值 |
| 6 | GameplayProof 字段 + tool param | **否（漏过）** | 0/7 未填 |

3/6 命中。最少触发数（因为运行最顺畅，少踩坑）。

---

## 5. 工具调用与时间投入

```
26 edit                   19 typecalc_describe     18 typecalc_test
18 graph_merge_attribute  16 typecalc_synthesize_tests 14 graph_merge_object
13 write_file             10 typecalc_compile       9 graph_link_consume
 9 typecalc_review         7 graph_link_produce     7 checkpoint_add_item
 7 checkpoint_fill         6 bash                   5 graph_create_attribute
 5 session_start           5 session_status         4 read_file
 4 graph_create_object     4 spawn_subagent ★      2 list_dir
 2 graph_validate          2 session_gate_check     1 graph_link_mutate
 1 graph_preflight         1 session_set_architecture 1 typecalc_waive
 1 session_focus           1 session_aggregate     1 checkpoint_freeze
 1 typecalc_obstacle       1 checkpoint_show       1 graph_show
```

**对比 v8.6 pong-04**（v8.6 的最佳实例）：

| | v8.6 pong-04 | v8.7 pong-04 |
|---|---|---|
| msgs | 289 | 319 |
| tool calls | 203 | 222 |
| 转录 | 498 KB | 556 KB |
| typecalc_test | 14 | 18 |
| **spawn_subagent** | **1** | **4** |
| obstacle | 0 | 1 |
| waive | 0 | 1 |
| gate_check | 2 | 2 |
| confirmed | 3/3 | 4/4 |
| path B 实际用 | 否 | **是（首次完整）** |
| gameplayProof | 0/9 | 0/7 |

资源接近，但**架构合规度跨级提升**。v8.6 pong-04 是"凭直觉走通"，v8.7 pong-04 是"按协议走通"。

---

## 6. 5 个实例 v8.7 完成结果汇总

| 实例 | 状态 | 评级 | 触发 v8.7 修复 | path B 真实? | gameplayProof | waiver |
|---|---|---|---|---|---|---|
| v8.7 pong-01 | finished | ★★★ | 1, 2, 4 | ✗ (path A) | 0/11 | 2/3 (67%) |
| v8.7 pong-02 | finished | ★★★★ | 1, 2, 4, 6 | ◐ (shell-only) | 7/7 描述 | 2/6 (33%) |
| v8.7 pong-03 | finished | ★★★★ | 1, 2, 3, 4, **5**, 6 | ✗ (path A) | 0/8 | 2/4 (50%, 中途触发 #5) |
| **v8.7 pong-04** | **finished** | **★★★★★** | 1, 2, 4 | **✓ (完整)** | 0/7 | 1/4 (25%) |
| v8.7 pong-05 | finished | ★★★ | 1, 2, 3, 4, 6 | ✗ (path A) | 10/10 描述 | 4/6 (67%) |

**pong-04 v8.7 是 5 个实例中的最佳运行**——唯一完整 path B，最少 waiver，2 轮 gate 即过。**评级 ★★★★★**。

---

## 7. 主要剩余偏离

### 7.1 gameplayProof 仍未填（与 pong-01/03 同）

`checkpoint_fill` 调用 7 次只填 code，0 次填 gameplay。Agent 即便走对了 path B，仍没发现 v8.7 #6 的新参数。

**修复方向**：将 `checkpoint_fill` 工具描述中 `gameplay_proof` 参数前置，并在 system.md 中明确"R4 必须双填"。

### 7.2 §5.5 R3 snap 完全跳过（结构性，所有实例）

无 `K/proofs/` 目录，无截图。所有 5 个实例一致。**结构性 gap**：pong-04/ 目录没有 package.json，agent 无法 `npm run snap`。

### 7.3 单文件 vs 多 .impl.ts

`§5.1.1 #5` 的"`.impl.ts` ≥ must × 0.5"对单 index.html 多函数模式不友好。pong-04 把 4 函数集中在 1 index.html，逻辑上每个对象都有"impl"但物理上共享一个文件。**协议描述与单文件 deliverable 实践有落差**。

---

## 8. 结论

pong-04 v8.7 是 v8.7 修复全部工作就绪后的**理想行为示范**：

1. **正确选择 path B** —— ≥3 对象时按 §5.4 规则真委派
2. **真实并行 spawn_subagent** —— 不是空跑形式
3. **session_id 准确归属 diff** —— 每个子 session 承载它该承载的工作
4. **最少 waiver / 最少 gate_check** —— 因为正确流程下少踩 v8.7 漏洞
5. **reviewer 全部 verdict=pass** —— 含 Render（structural obstacle 也通过 reasonableness review）

**遗留问题集中在两点**，都不是 v8.7 已修问题：
- **gameplayProof 仍未填**——v8.7 #6 提供能力但不强制；命中率 2/5
- **R3 snap 工程化兜底缺失**——所有 5 实例一致跳过

下一轮（v8.8）应当：
1. 让 `checkpoint_fill` 在 codeProof 已填 / gameplayProof 空的情况下输出 hint（非 FAIL）
2. 给 batch 测试目录预置 package.json + tools/snap.ts 软链，让 §5.5 R3 真能跑
3. 解决 pong-03 暴露的 runtime trace 累积污染（按 implHash 过滤）
4. 强化 reviewer prompt 抗 module-load 幻觉（pong-05 仍部分中招）
