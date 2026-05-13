# pong-05 v8.7 执行过程 vs CLAUDE.md 合规审计

**Run**: pong-05, 2026-05-11, kcpos v8.7 (commit `19d32f8`)
**结果**: s_pong_root.status = `finished`, 6/6 confirmed, gate PASS（经 4 轮 gate_check）
**用时**: ~75 min（启动 13:18 → finished ~14:30 量级）
**资源**: 362 messages, 263 tool calls, 516 KB transcript

**最重要的对照**：v8.6 pong-05 用 4/4 confabulation 通过 gate（"HTML cannot be loaded as module"）—— **是 v8.7 修复 #2 的主要靶子**。

---

## 1. 协议合规度矩阵

| CLAUDE.md § | 协议要求 | pong-05 实际 | 合规 |
|---|---|---|---|
| §0 L0 | graph 节点声明 | 6 attrs + 6 objs | ✓ |
| §0 L1 | impl + confirmed | 6/6 confirmed，impl=index.html | ✓ |
| §0 L2 | 单元测试通过 | 2/6 ok=true（SetupCanvas, ProcessInput）；4/6 obstacle+waiver | ◐ |
| §0 L3 | integration.test + bundle | 单文件无 bundle | ✗ N/A |
| §0 L4 | gameplayProof + PASS + 聚合 | 10/10 gameplayProof（描述性）；PASS；aggregated | ◐ |
| §3.0 | graph-edit 优先 | 全部通过 graph_* 工具 | ✓ |
| §4.1 | 命名规则 | graph 用 PascalCase；**impl 用 camelCase（不一致！）** | ✗ |
| §4.3 | 单文件 ≤ 1500 行 | index.html ~200 行 | ✓ |
| §5.1.1 #1-4 | 对象/impl/test 都齐 | 全齐 | ✓ |
| §5.1.1 #5 | .impl.ts ≥ must × 0.5 | 1 index.html / 10 must = 0.1 | ✗ |
| §5.1.1 #6 | checkpoint 两证齐全 | **10/10 codeProof + 10/10 gameplayProof** | ✓ |
| §5.1.1 #7 | 实际运行 gameplayProof | 字段填齐但是描述性 | ◐ |
| §5.1.1 #8 | output 聚合 | session_aggregate | ✓ |
| **§5.4** | **≥ 3 对象走 path B** | **6 对象走了 path A**（root + 0 children）| **✗** |
| §5.5 R1 | 产物聚合 | YES | ✓ |
| §5.5 R3 | npm run snap → K/proofs/ | 完全跳过 | ✗ |
| §5.5 R4 | 两证齐全 | codeProof + gameplayProof 全部填齐 | ✓ |
| §5.5 R5 | 最终闸门 | **4 轮 gate_check 后** PASS | ◐ |

合规度汇总：**10 ✓ / 5 ◐ / 5 ✗ / 1 N/A**

---

## 2. v8.7 #2（reviewer trace 注入）效果验证：**部分有效**

### 2.1 Agent 的 obstacle 理由全部 factually grounded

对比 v8.6 pong-05 与 v8.7 pong-05 的 obstacle reasons：

| 对象 | v8.6 obstacle | v8.7 obstacle |
|---|---|---|
| InitGame | "Cannot import/test the HTML-embedded module" *(confabulation)* | "ball.vx 和 ball.x 使用 Math.random()...synthesizer 返回 CANNOT_SYNTHESIZE" *(factual)* |
| CaptureInput | "Same as InitGame — module import structural failure" *(pasted)* | "安装持久 DOM 事件监听器...单一同步调用无法观察异步事件效果" *(factual)* |
| UpdatePhysics | "Implementation is a full HTML page...not usable in headless environment" *(confabulation)* | "harness 报错 IMPL.UpdatePhysics is not a function — 全部 13 个测试用例均失败" *(factual)* |
| DrawFrame | "HTML embedded JS can't be loaded as ES module" *(confabulation)* | "调用 drawFrame 时报 ReferenceError: ctx is not defined" *(factual)* |

**Agent 的判断质量明显改善**——4 个 obstacle 全部指向**具体技术现象**（synthesizer 限制、异步事件、函数名大小写、ctx 未提供）。Reason-diversity probe 下，4 个 normalized key 完全不同。

### 2.2 但 reviewer LLM 仍部分幻觉

`accepted.json` 的 reasonableness reasons 部分仍重复 v8.6 老话：

```
UpdatePhysics: "Implementation is an HTML script, not a module exporting updatePhysics as required."
DrawFrame:    "Implementation is a full HTML page with game loop, not a standalone module exporting drawFrame."
```

这是 v8.7 #2 trace 注入**未完全防住**的情况。原因分析：

- UpdatePhysics.json 的 trace 实际有 48 calls（旧 implHash=b4753e91）
- 但 evidence-stale 触发（impl 已变成 288663ee）
- runtime-output-missing 也触发（mutates 端口 outputs 空）
- LLM 在"trace 看似存在但 issues 显示出问题"的情况下，**优先相信 issues** 而忽视 trace 计数

**修复建议**：v8.7 #2 注入的 trace 摘录应**显式注明 "calls > 0 → impl WAS loaded, 不要怀疑 module-load 问题"**——我已经写了，但 LLM 仍被旁边的"runtime-output-missing"干扰。需要进一步：把 trace 摘录放到 prompt 最顶部、issue 列表后置。

### 2.3 真正的新 bug 被暴露：camelCase 函数名

```
graph 对象名: UpdatePhysics (PascalCase per §4.1)
impl 函数名:  updatePhysics (camelCase)
harness 调用: IMPL.UpdatePhysics(...) → undefined → "is not a function"
```

[index.html:86](tests/pong-05/index.html#L86) `function updatePhysics(...)`——agent 把 6 个函数全写成 camelCase。但 §4.1 命名规则说对象 ID 用 PascalCase，没强约束 impl 函数名要匹配。Harness 隐含假设 `IMPL[objectID]` 能找到函数。

**这是 v8.7 修复留下的协议落差**：
1. CLAUDE.md §4.1 没规定 impl 函数名 = 对象 ID
2. system.md 的 harness 说明没强调"函数声明名必须 = 对象 ID 才能被 IMPL 命名空间发现"
3. v8.4 的 indirect eval scan 只匹配 `function NAME` 与 PortObservation 默认假设的命名一致

v8.8 候选修复：harness 在找不到 `IMPL[objectID]` 时，**尝试 camelCase 别名**（首字母小写 + 保留剩余）作为兜底。

---

## 3. v8.7 修复在本实例的命中

| # | 修复 | 命中 | 实际效果 |
|---|---|---|---|
| 1 | OutputPorts ∪ Mutates | **是** | UpdatePhysics 的 mutates 端口 trace 看起来正常（48 calls 有 outputs），但因为 camelCase 函数找不到导致测试失败而非 outputs 空 |
| 2 | reviewer trace 注入 | **部分** | Agent obstacle reasons 都是 factual（4/4），但 reviewer LLM 仍部分老话（2/6 还在说"not module exporting"）|
| 3 | type:"enum" 友好降级 | **是** | DrawFrame 的 game_state valueSpace 用了 enum；runtime-enum-violation 仅 1 条（且观测到 "__side_effect__" 这种特殊值），未制造 80 条假阳性 |
| 4 | typecalc-use hook 认 obstacle+waiver | **是** | msg 246 直接 confirm × 5，没绕路 |
| 5 | gate.go [waiver-flood] ≥75% | **否（正确不触发）** | 4/6=67% 低于 75%。但**贴近阈值**——若 5/6 就会触发。系统性确认 v8.7 阈值合理 |
| 6 | GameplayProof 字段 + tool param | **是** | 10/10 must 项 gameplayProof 都填了。pong-05 v8.7 与 pong-02 一样发现并使用新参数（pong-01 没用）|

---

## 4. 主要偏离点

### 4.1 §5.4 path B 又一次违规（与 pong-01 同）

6 个对象（最多 batch 之一），root `s_pong_root` 的 `children=[]`。CLAUDE.md §5.4 硬阈值"≥3 必须 path B"在本批 5 个实例中 3 个走错（pong-01 / pong-05，且 pong-02 走的也是 shell-only path B）。**3/4 完成实例 path B 未真实启用**。

### 4.2 §4.1 命名规则被破——函数名 camelCase vs 对象名 PascalCase

唯一一个被 CLAUDE.md §4.1 命名规则触及的实例（pong-01/02/03/04 都没踩到，因为 portObservation 走 `return.*` 或 `global` 路径不依赖函数名查找）。pong-05 用 args.0/args.1 + camelCase 函数名，触发 harness lookup miss。

### 4.3 evidence-stale + 4 轮 gate_check

```
msg 297: gate_check #1 → FAIL (evidence-stale on InitGame, CaptureInput)
msg 302: gate_check #2 → FAIL (still some issue)
msg 350: gate_check #3 → FAIL
msg 354: gate_check #4 → PASS
```

期间 agent 反复 edit index.html → 重跑部分 typecalc_test → 再 review → 再 merge。这是合理的迭代，但 4 轮 gate 暴露了 agent 没在更早的循环里把所有依赖跑齐。

### 4.4 SetupCanvas 的 outputs={} 但 verdict=pass 蹊跷

```
SetupCanvas: 5 calls, outputs keys=[] —— 端口未观察到
verdict=pass, reasons: "Returns hardcoded width=600, height=400 exactly as specified"
```

portObservation: `{canvas_dimensions: 'return'}` 应取整个 return 值作 canvas_dimensions。但 outputs keys 是空。可能 `IMPL.SetupCanvas` 也命中 camelCase miss（实际是 `setupCanvas`），lastReturn=undefined，于是 outputs[port]=undefined → snapshotPorts 过滤 undefined → outputs={}。

但 reviewer 还是 verdict=pass —— 看起来不在乎 trace 内容，凭 SPEC + impl 直接判断。**这是 v8.7 #2 漏过的另一种形态**：trace 空了，reviewer 不质疑直接 pass。

---

## 5. 工具调用与时间投入

```
33 graph_merge_object       21 graph_merge_attribute    20 checkpoint_fill
19 edit                     19 typecalc_review          17 write_file
17 typecalc_describe        14 typecalc_test            10 graph_link_consume
10 graph_link_produce       10 typecalc_synthesize_tests 10 checkpoint_add_item
 8 read_file                 7 graph_create_attribute    6 graph_create_object
 6 graph_link_mutate         5 typecalc_waive            5 typecalc_obstacle
 4 graph_validate            4 session_gate_check        3 list_dir
 3 bash                      3 graph_unlink_produce
```

**对比 v8.6 pong-05**：

| | v8.6 pong-05 | v8.7 pong-05 |
|---|---|---|
| msgs | 194 | **362** |
| tool calls | 132 | **263** |
| 转录 | 369 KB | 516 KB |
| typecalc_test | 12 | 14 |
| typecalc_review | 4 | 19 |
| typecalc_obstacle | 4 | 5 |
| typecalc_waive | 4 | 5 |
| graph_merge_object | 12 | 33 |
| 评级 | ★ (confabulation 通过) | ★★★ (factual obstacle 通过) |

工作量翻倍但**证据质量从虚假变真实**。Agent 用 19 次 review（v8.6 仅 4 次）说明它在反复纠结找具体原因，而不是早早 mass-waive。

---

## 6. 5 个实例 v8.7 中间结果（截至本报告时间）

| 实例 | 状态 | 评级 | gameplayProof | 主要偏离点 |
|---|---|---|---|---|
| v8.7 pong-01 | finished | ★★★ | 0/11 | path A 用错，gameplayProof 未填 |
| v8.7 pong-02 | finished | ★★★★ | 7/7 描述性 | path B 名实分离 |
| v8.7 pong-03 | 跑中 | — | — | — |
| v8.7 pong-04 | 跑中 | — | — | — |
| **v8.7 pong-05** | **finished** | **★★★** | **10/10 描述性** | path A 错，camelCase 函数名错，4 轮 gate 才过 |

---

## 7. 结论

**v8.7 #2（reviewer trace 注入）效果是"显著但不彻底"**：
- Agent 端：obstacle reasons 从 4/4 confabulation 变为 4/4 factual（满分改善）
- Reviewer 端：仍有 2/6 老话"not module exporting"——trace 注入没完全压住

**v8.7 #6（gameplayProof 字段）继续呈现"机会型"特性**：
- pong-02 + pong-05 填齐
- pong-01 完全没用
- 同 binary 同 system.md 同 prompt，命中率 ~67%

**新发现的 v8.8 候选 bug**：
1. **Harness camelCase 兜底**：`IMPL.UpdatePhysics is not a function` 时 fallback 到 `IMPL.updatePhysics`
2. **Reviewer prompt 进一步强化 anti-hallucination**：把 trace 摘录前置 + 显式禁令"trace.calls > 0 时不得提及 module-load/export 问题"
3. **CLAUDE.md §4.1 增加约束**：impl 函数名 = 对象 ID（或至少 case-insensitive 等价）
4. **gameplayProof 引导**：当 codeProof 已填且 gameplayProof 空且 frozen=true 时，gate 输出非 FAIL 的 hint

**评级 ★★★**：完成度 + 证据真实性（4/4 factual obstacle）合格，但 path B 未走 + 4 轮 gate + camelCase 命名违规拉低评级。v8.7 在 pong-05 上的对照表现比 v8.6 大幅改善——从 ★ 上到 ★★★，**修复 #2 的设计意图达成（破除 confabulation）但 polish 还差一步**。
