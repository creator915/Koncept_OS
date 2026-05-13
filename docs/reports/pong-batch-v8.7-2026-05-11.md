# pong v8.7 5 并发批次总报告（2026-05-11）

**Binary**: kcpos v8.7 (commit `19d32f8`) — 6 个修复在 v8.6 baseline 上累加
**触发条件**: 5 个 pong-01..05 目录同时运行同一 prompt（实现 SPEC.md 单人乒乓球）
**结果**: **5/5 finished** — 对照 v8.6 批 5 完成 1 死锁
**报告**: 单实例审计另存 [pong-0N-v8.7-claudemd-audit-2026-05-11.md](.) × 5

---

## 0. 一句话结论

**v8.7 实现了 "v8.6 验证完全可达，但收尾合规仍有 3 个机会型 gap"**。最关键的根因（typecalc.go:217 OutputPorts 漏 Mutates）修复后，**没有一个实例再走到 v8.6 pong-01 那条死路**；5 个实例对各自的 v8.7 修复触发率从 3 个到 6 个不等，覆盖了所有设计意图路径，包括 #5 [waiver-flood] 节流首次在 pong-03 实战触发并成功收敛。

---

## 1. 五实例终态对比矩阵

| 实例 | 评级 | confirmed | waiver% | 真测试通过 | gameplayProof | gate 轮 | path B 真实 | spawn_subagent |
|---|---|---|---|---|---|---|---|---|
| pong-01 | ★★★ | 3/3 | 67% | 1/3 | 0/11 | 1 | ✗ (path A) | 0 |
| pong-02 | ★★★★ | 6/6 | 33% | 4/6 | **7/7 描述性** | 1 | ◐ (shell-only) | 0 |
| pong-03 | ★★★★ | 4/4 | 50%(中途 75%) | 2/4 | 0/8 | **8** | ✗ (path A) | 0 |
| **pong-04** | **★★★★★** | 4/4 | 25% | **3/4** | 0/7 | 2 | **✓ (完整)** | **4** |
| pong-05 | ★★★ | 6/6 | 67% | 2/6 | **10/10 描述性** | 4 | ✗ (path A) | 0 |

**总计**：
- **20 + 3 = 23 个 confirmed 对象** 分布在 5 个 root session
- **12 个真测试通过**（ok=true verdict=pass）= 52%
- **11 个走 obstacle+waiver pair** = 48%
- **17/43 must items 填了 gameplayProof** = 40%（v8.6 batch 为 0/N，硬提升）
- 0 个实例死锁

---

## 2. v8.6 vs v8.7 head-to-head（按实例）

| 实例 | v8.6 结局 | v8.7 结局 | 关键变化 |
|---|---|---|---|
| pong-01 | **死（6.8 MB grep 自爆）** | finished | **OutputPorts 修复让 mutates 端口 outputs 不再空，agent 不必走调试死循环** |
| pong-02 | 1/2 waiver | 2/6 waiver（33%）| 拆得更细（2→6 对象），gameplayProof 字段被使用 |
| pong-03 | 2/4 narrow waiver | 2/4 + [waiver-flood] 触发中途 | **#5 实战触发并收敛，agent 修复后通过** |
| pong-04 | 0/3 waiver（v8.6 最佳）| 1/4 waiver + **完整 path B** | path B 第一次真正用 spawn_subagent 并行 |
| pong-05 | **4/4 全 confabulation** | 4/6 obstacle，**全部 factual** | **#2 trace 注入让 obstacle 理由从假变真** |

**核心改变**：
- pong-01 的"mutates → outputs 空 → 死锁"路径**根除**
- pong-05 的"mass-waiver + confabulation"路径**改善**（理由变真，但 reviewer 仍部分幻觉）
- 新出现一个 ★★★★★（pong-04 完整 path B），是 v8.6 batch 没有的水平

---

## 3. v8.7 六项修复实战命中

| # | 修复 | 命中实例 | 实际效果 |
|---|---|---|---|
| **1** | typecalc.go:217 OutputPorts ∪ Mutates | **5/5（全员）** | mutates-pattern outputs 不再空；pong-01 直接受益脱离死锁 |
| **2** | review.go renderIssueAwareLog 注入 trace | **5/5（全员）** | obstacle 理由从 confabulation 转向 factual；pong-05 改善尤其大；但 LLM 在 evidence-stale 同时存在时仍可能部分幻觉 |
| 3 | runtime_check `type:"enum"` 友好降级 | 2/5（pong-03, pong-05）| game_state 用 enum 时未触发 80 条假阳性 |
| 4 | typecalc-use hook 认 obstacle+waiver | **5/5（全员）** | status=confirmed 无 v8.6 那种"先跑假 test 生壳"弯路 |
| **5** | **gate.go [waiver-flood] ≥75%** | **1/5（pong-03 中途）** | **首次实战触发，agent 正确响应——从堆 waiver 转回真测试，最终通过** |
| 6 | Item.GameplayProof 字段 + tool param | 2/5（pong-02, pong-05）| 命中率 40%，是"机会型"修复 |

**触发率说明**：
- #1, #2, #4 在所有 mutates-pattern impl 上必然命中
- #3 取决于 agent 是否用非 canonical enum 形式
- #5 取决于 waiver 是否密集——pong-03 中途确实达到 75% 触发，pong-01/05 接近但未达（67%）
- #6 取决于 agent 读 `checkpoint_fill` 工具描述的深度——明显不稳定

---

## 4. CLAUDE.md 合规度汇总

| 协议要求 | 合规实例数 |
|---|---|
| §0 L0 graph 声明 | 5/5 |
| §0 L1 impl + confirmed | 5/5 |
| §0 L2 单元测试通过（含 waiver）| 5/5 |
| §0 L4 gameplayProof + PASS + 聚合 | 2/5（pong-02/05 填了描述性 gameplayProof）|
| §3.0 graph-edit 优先 | 5/5 |
| §4.1 命名规则（PascalCase 对象 + snake_case 属性）| 4/5（pong-05 函数名 camelCase 与对象 ID 不一致）|
| §4.3 单文件 ≤1500 行 | 5/5 |
| §5.1.1 8 项达成 | 0/5（全部 #7 实际运行截图缺）|
| **§5.4 path B 真正委派** | **1/5（仅 pong-04）** |
| §5.5 R1 聚合 | 5/5 |
| §5.5 R3 npm run snap + K/proofs/ | **0/5（全部跳过）** |
| §5.5 R4 codeProof + gameplayProof 填齐 | 2/5（描述性）|
| §5.5 R5 gate 通过 | 5/5 |

最薄弱处：**§5.4 path B 实际执行率 20%（1/5）+ §5.5 R3 snap 实际执行率 0%（0/5）**。

---

## 5. 关键 v8.7 现象学发现

### 5.1 [waiver-flood] 阈值实战验证（pong-03，唯一）

pong-03 中途 3/4=75% waiver，命中 v8.7 #5 阈值。transcript 摘录：

```
Agent: "Still waiver-flood: 3/4. The gate wants at least one more object
        with clean review (no obstacle). InitGame is the only one [already clean]."
       "I need to get either UpdatePhysics or RenderFrame to pass review
        without obstacle. The only way is to fix the runtime issues."
Agent: rm -f .kcpos/typecalc-runtime/*.json
Agent: typecalc_test UpdatePhysics → TestedPass
       typecalc_test RenderFrame → TestedPass
Agent: rm -f .kcpos/typecalc-evidence/UpdatePhysics.obstacle.json
       rm -f .kcpos/typecalc-evidence/RenderFrame.obstacle.json
```

**完整闭环**：gate 给出可操作反馈 → agent 从堆 waiver 转为真解决底层 → 通过。这是 v8.7 #5 的设计意图，**实战验证成功**。

### 5.2 pong-04 是 v8.7 批的 path B 范本

| Path B 步骤 | pong-04 实际 |
|---|---|
| 子 session 创建 | `session_start × 4` (msg 62) |
| **真委派** | **`spawn_subagent × 4` 并行 (msg 147)** |
| diff 归属 | `graph_merge_object id=X session_id=s_impl_X × 4` (msg 226) |
| 收尾切回根 | `session_focus s_pong_root` (msg 259) |
| 子 session 真实工作 | 4 子 session 各有 modified=1-4 graphDiff |

**5 个实例里唯一一个走过完整 path B 链路**。其他 4 个：3 个 path A，1 个 shell-only path B（pong-02 创建子 session 但无 spawn_subagent，diff 都归在根）。

### 5.3 pong-05 暴露的新 bug：camelCase 函数名

```
graph 对象: UpdatePhysics (PascalCase per §4.1)
impl 函数:  updatePhysics (camelCase, agent 偏好)
harness 调: IMPL.UpdatePhysics(...) → undefined → "is not a function"
```

CLAUDE.md §4.1 没明确规定 impl 函数名 = 对象 ID。harness 默认按对象 ID 在 `IMPL` 命名空间查找。v8.4 indirect-eval scan 只匹配 `function NAME` 字面量，没做 PascalCase↔camelCase 兜底。

### 5.4 pong-03 暴露的新 bug：runtime trace 累积污染

`.kcpos/typecalc-runtime/<id>.json` 每次 `typecalc_test` **追加**而非**覆盖**条目。Agent 改 portObservation 后旧 `'__side_effect__'` 条目残留，触发 246 条 `runtime-enum-violation` 假阳性。Agent 不得不**手动 rm trace 文件**才能继续。

---

## 6. v8.8 候选修复清单（按优先级）

| # | 问题 | 暴露实例 | 修复方向 |
|---|---|---|---|
| A1 | runtime trace 累积污染 | pong-03 | harness.go appendTrace 按 currentImplHash 过滤或覆写 |
| A2 | harness camelCase 兜底 | pong-05 | `IMPL.PascalCase` 找不到时尝试 `IMPL.camelCase` |
| A3 | reviewer module-load 幻觉残留 | pong-05 | trace 摘录前置 + 显式禁令 "trace.calls > 0 时不得提及 module-load" |
| B1 | gameplayProof 命中率不稳定 | pong-01/03/04 | `checkpoint_fill` 在 code 填了/gameplay 空时输出 hint（非 FAIL）|
| B2 | path B 实际执行率 20% | pong-01/03/05 | system.md 强化"≥3 对象 → spawn_subagent"，区分 session_create vs 真委派 |
| B3 | §5.5 R3 snap 跳过率 100% | 全部 5 | batch 目录预置 package.json + tools/snap.ts 软链，或 kcpos 内置 headless snap |
| C1 | path B 阈值边界 = 3 | pong-01（3 对象）| CLAUDE.md §5.4 改写"> 3 必须 path B；= 3 可选" |
| C2 | impl 函数名 = 对象 ID | pong-05 | CLAUDE.md §4.1 加约束，或 §1.1 system.md 加强 |
| D1 | gate_check 反馈过晚 | pong-03（8 轮）| graph_merge_object 后即时跑部分 gate 子检查 |

**P0 必须**：A1（runtime trace 污染会让任何复杂场景反复假阳性，agent 解决靠 bash rm 不可持续）

**P1 推荐**：A2 + A3 + B1（剩余 confabulation 风险 + 工具发现率不稳定）

**P2 可观察**：B2 / B3 / C1 / C2 / D1（结构性 / 约束类，影响合规但不影响完成）

---

## 7. 资源消耗对比

| 实例 | msgs | tool calls | 转录 KB |
|---|---|---|---|
| v8.6 pong-01 (dead) | 128 | 91 | **7400** ⚠ grep 自爆 |
| v8.7 pong-01 | 270 | 185 | 501 |
| v8.6 pong-02 | 113 | ~113 | 150 |
| v8.7 pong-02 | 281 | 216 | 458 |
| v8.6 pong-03 | 288 | 196 | 412 |
| v8.7 pong-03 | **367** | 247 | **1275** |
| v8.6 pong-04 | 289 | 203 | 498 |
| v8.7 pong-04 | 319 | 222 | 556 |
| v8.6 pong-05 | 194 | 132 | 369 |
| v8.7 pong-05 | 362 | 263 | 516 |

**普遍**：v8.7 比 v8.6 多 30-80% 资源。两个原因：
- **#1 让真测试不再被 OutputPorts bug 卡死**：agent 拿到真实失败信号后会迭代尝试修复（v8.6 是直接放弃走 waiver）
- **#5 触发后 pong-03 必须重测部分对象**：单独贡献了 ~860 KB（v8.7 pong-03 的 1275-412 KB 增量大部分来自这里）

**这是健康的资源增长**——之前是结构性失败导致放弃，现在是真在解决问题。

---

## 8. DeepSeek API 实际消耗

约 ¥5 / 5 实例（按 batch 启动前 ¥30 余额 - 当前 ¥24.71 = ¥5.29）。其中 pong-03 + pong-04 占大头（最长运行）。

**未来 5 并发**经验：
- 简单游戏 → 单批 ¥5-10
- 中等复杂度 → 单批 ¥10-20
- 想做大规模 (10+ 实例) → 提前充值 ¥50+

---

## 9. 总体判断

**v8.7 是 v8.6 的彻底修复 + 部分 polish**：

✓ **彻底修复**：v8.6 的 mutates outputs 空 + reviewer mass-confabulation 两条死循环都断了
✓ **新设计闭环验证**：#5 [waiver-flood] 节流在 pong-03 上完整跑通——这是 v8.7 最重要的方法论胜利
✓ **路径示范**：pong-04 给出了完整 path B 的范本，5 实例里有 1 个"按教科书走"的案例可以学习

◐ **机会型 gap**：#6 GameplayProof 字段 + #2 reviewer trace 注入都属于"能力提供，命中率不稳"，下一轮需要更强引导

✗ **结构性 gap**：§5.5 R3 snap 全部跳过（工程化兜底缺失），§5.4 path B 仅 1/5 真实执行（system.md 还需强化）

**建议**：v8.7 落盘（已 commit `19d32f8`）。是否继续做 v8.8 取决于：
- 如果**优先级是验证 kcpos 设计**：v8.7 已证明所有关键路径可达，可以暂停修复转向其他设计验证
- 如果**优先级是单批合规度刷到 5 ★★★★★**：A1 (trace 污染) + B1 (gameplayProof hint) + B2 (path B 引导) 三个改动应该能让下批 5/5 都接近 pong-04 v8.7 水平

---

**附**：所有单实例详细审计：
- [pong-01-v8.7-claudemd-audit-2026-05-11.md](pong-01-v8.7-claudemd-audit-2026-05-11.md) ★★★
- [pong-02-v8.7-claudemd-audit-2026-05-11.md](pong-02-v8.7-claudemd-audit-2026-05-11.md) ★★★★
- [pong-03-v8.7-claudemd-audit-2026-05-11.md](pong-03-v8.7-claudemd-audit-2026-05-11.md) ★★★★（首次触发 [waiver-flood]）
- [pong-04-v8.7-claudemd-audit-2026-05-11.md](pong-04-v8.7-claudemd-audit-2026-05-11.md) **★★★★★（完整 path B）**
- [pong-05-v8.7-claudemd-audit-2026-05-11.md](pong-05-v8.7-claudemd-audit-2026-05-11.md) ★★★
