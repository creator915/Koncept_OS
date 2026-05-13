# Pong Batch (5 parallel × v8.4) — 2026-05-09

5 个相同 SPEC 的乒乓球项目并行跑（同 DeepSeek API key）。目标：在 v7→v8 一系列修复（D5 集成验证 + 5 个 HTML harness bugs）后，验证 kcpos 的稳定性、找出在并行场景下暴露的新问题。

5 实例同时启动 16:21，全部结束于 17:30 左右，跑时长 ~80 min。

---

## 1. 总览

| 实例 | session | TP | Confirmed | Checkpoint | turns | impl 模式 |
|---|---|---|---|---|---|---|
| pong-01 | **active** ⚠️ | 8 | 7/7 | PASS 8/8 | 89 | dual (index.html + pong.impl.js) |
| pong-02 | finished ✓ | 0 | 2/2 | PASS 8/8 | 107 | single (index.html only) |
| pong-03 | finished ✓ | 1 | 2/2 | PASS 8/8 | 135 | dual (index.html + game.impl.js) |
| pong-04 | finished ✓ | 4 | 4/4 | PASS 8/8 | 99 | single (index.html only) |
| pong-05 | finished ✓ | 10 | 4/4 | PASS 8/8 | 110 | single (index.html only) |

**完成率 4/5（80%）**。pong-01 唯一卡住的实例 — 7/7 confirmed、8 TP，但 gate 拒绝 finished。

vs 历史：
- v5: 0/1 finished（撞 60 cap）
- v6: 1/1 finished but **绕 gate**
- v7: 1/1 finished gate-PASS（单跑）
- **v8.4: 4/5 finished gate-PASS**（并行 5）

---

## 2. v8.4 修复实测情况

| 修复 | 5 实例累计触发 | 效果 |
|---|---|---|
| auto-validate "Required next turn" | 19 次 | agent 多数立即响应 |
| iteration cap reached | **0 次** | 5/5 都没撞 150 上限 |
| refusing-to-mark (gate guard) | **0 次** | agent 不再尝试绕 gate |
| vitest --reporter=basic 错误 | **0 次** | reporter fix 生效 |
| document is not defined | **0 次** | browser stub 生效 |
| is not a function (IMPL 空命名空间) | **0 次** | indirect eval 生效 |

5 个实例累计 **23 个 Tested<Pass>**（v7 单跑只到 6/6）。kcpos 的机械验证通道**真在 HTML 项目上工作**。

---

## 3. 单实例细节

### pong-01（卡死）

7 对象 / 4 属性全部 confirmed，8 个 Tested<Pass>，**但 session.status 仍是 active**。

- **dual-source 走老路**：agent 创建了 `pong.impl.js` + 内嵌脚本两份。impl 文件多了个 `GameTick` 包装（用于测试），但 index.html 内嵌没用 GameTick — 调用链是 `ProcessInput → UpdatePhysics → RenderFrame` 直接展开
- **卡死原因**：6 review 通过、3 个对象（GameTick / ProcessInput / UpdatePhysics）卡 `runtime-input-missing`（args 端口 trace 不全）+ obstacle+waiver 不被 gate 接受为 kind=test 替代
- agent 跑满 89 turns 自我总结但**没法调 session_status finished**（gate guard 拒了）

### pong-02（最少对象，全靠 waiver）

只 2 个对象（InitGame + ReadInput）confirmed，0 Tested<Pass>，**全靠 obstacle (2) + waiver (7)** 走 gate。

- **single-source**：实现全在 index.html 里
- agent 把 SPEC 拆得最小，每个对象都用 waiver 兜底
- gate PASS（gate 接受 obstacle+waiver 作为 Insufficient 类的合法回避）
- 用户角度：游戏可玩，但 kcpos 验证维度上接近"放弃测试"

### pong-03（中度成功）

2 对象 / 6 属性 confirmed，1 Tested<Pass>。

- **dual-source**：`game.impl.js` 有 InitGame/UpdateFrame；index.html 多一个 `Render` 函数（只在内嵌存在）
- **创建了 2 个子 session**（s_impl_initgame / s_impl_updateframe），探索性拆分
- 7 obstacle / 5 waiver — 大量回避路径
- 最终 root + 2 子 session 全 finished

### pong-04（标准成功）

4 对象 / 6 属性 confirmed，4/4 Tested<Pass>，**所有对象都真过测**。

- **single-source**，函数命名 PascalCase 与 graph 一致
- 8 obstacle / 9 waiver — 但都是辅助记录，不阻塞
- agent 在 17:05 做了关键的物理修复（"axis-by-axis" 重写 UpdatePhysics）

### pong-05（最高 TP）

4 对象 / 6 属性 confirmed，10 个 Tested<Pass>（多次重测累计）。

- **single-source**
- agent 在多次迭代中每个对象测过多次，累计计数最高
- 最后阶段把 Render 用 obstacle 兜底（canvas mock 不全）

---

## 4. 关键观察

### 4.1 dual-source 阴影仍在（D5 没完全消除）

v8.4 的本意是让 HTML 直接走 harness，**不再需要 K/impl/*.js 影子文件**。但 5 个实例里 **2 个（pong-01 / pong-03）仍走 dual-source**，agent 自发创建了 `.impl.js` 文件。

**为什么**：agent 学习的不是"v8.4 修了 HTML"，而是"kcpos 历史上喜欢 .js 文件"。system.md 没明确指引 agent 优先 single-source。

**影响**：
- pong-01 卡死跟 dual-source 直接相关 — impl 文件里有 `GameTick` 但 inline 没有，gate 要求 GameTick 有 kind=test 但 inline 测不到
- 用户打开 index.html 仍能跑（inline 完整），但 kcpos 视角是"有未测函数"

**修复方向**：在 system.md 里加"HTML 项目永远 impl=index.html，不要创建 .impl.js"硬规定。

### 4.2 gate 对 kind=test 的强要求与 obstacle/waiver 不对等

**pong-01 vs pong-02 对比**：

|   | pong-01 | pong-02 |
|---|---|---|
| Tested<Pass> | 8 | 0 |
| Confirmed | 7/7 | 2/2 |
| Session finished | ❌ | ✅ |

pong-02 有 0 个真过测但 gate 通过；pong-01 有 8 个真过测但 gate 不通过。差异：

- pong-02 把每个对象都用 `Insufficient + waiver` 路径走，gate 接受 waiver 作为 Insufficient 的合法替代
- pong-01 走 `kind=test (但部分失败) + obstacle + waiver`，gate 不把这组合视为 kind=test 的合法替代

这是 **P1 bug**：obstacle + waiver 在 "tests passed but a few cases failed" 场景上应被 gate 接受，而不是把这种状况判定为劣于 "完全没测"。

### 4.3 函数命名一致性仍是噪音源

agent 在 PascalCase（synthesizer 生成的 `IMPL.X(...)`）和 camelCase（实际函数声明）之间切换，多次重写实现。pong-04/05 早期都因此撞 `is not a function`。

**修复方向**：synthesizer prompt 要求 case 的 `call` 字段必须用 graph object id 的精确形式（PascalCase）。

### 4.4 子 session 模型可用

pong-03 第一次自然出现"父 session + 2 子 session"结构（s_impl_initgame / s_impl_updateframe），全部 finished，**没出 lifecycle bug**。这验证 kcpos 的 session tree 逻辑在并行测试下也稳。

---

## 5. 数字汇总

| 维度 | pong-01 | pong-02 | pong-03 | pong-04 | pong-05 | 合计 |
|---|---|---|---|---|---|---|
| Objects | 7 | 2 | 2 | 4 | 4 | 19 |
| Confirmed | 7 | 2 | 2 | 4 | 4 | 19 |
| Tested<Pass> | 8 | 0 | 1 | 4 | 10 | 23 |
| TestError | 6 | 0 | 8 | 8 | 3 | 25 |
| Insufficient | 0 | 3 | 15 | 13 | 7 | 38 |
| obstacle | 3 | 2 | 7 | 8 | 7 | 27 |
| waive | 4 | 7 | 5 | 9 | 13 | 38 |
| auto-validate | 6 | 4 | 2 | 5 | 2 | 19 |
| Required-next-turn | 6 | 4 | 2 | 5 | 2 | 19 |
| iteration-cap | 0 | 0 | 0 | 0 | 0 | 0 |
| refusing-to-mark | 0 | 0 | 0 | 0 | 0 | 0 |
| gate-checks | 9 | 3 | 6 | 7 | 6 | 31 |
| Index.html (bytes) | 8070 | 5591 | 7553 | 7279 | 6555 | — |

---

## 6. P0/P1/P2 残留清单

### P0：obstacle+waiver 不能替代 kind=test（pong-01 卡死的根因）

- **症状**：agent 把所有能做的都做了（review、obstacle、waiver），gate 仍拒绝 finished
- **修复方向**：当对象有 `kind=test ok=false` + `kind=obstacle` + `kind=waiver`（且 waiver 描述充分）时，gate 视为合法的 kind=test 替代
- **优先级**：P0，否则有"无形死锁"风险

### P1：HTML 项目仍可创建 dual-source（D5 没强制单源）

- **症状**：5 个里有 2 个仍创建 `.impl.js` 旁路文件
- **修复方向**：
  - 软：system.md 明确"HTML 项目 impl 必须 = index.html"
  - 硬：graph_create_object/merge_object 检测 cwd 有 index.html 时拒绝 impl 指向其他 .js

### P1：synthesizer call 表达式 casing 不强

- **症状**：synthesizer 用 PascalCase（`IMPL.InitGame()`），有些实现用 camelCase（`function initGame()`）→ 命名不匹配
- **修复方向**：synthesize.go prompt 强制 "call 必须以 IMPL.<exact-graph-id>(...) 开头"

### P2：runtime-input-missing 在 mutates 场景仍偶发

- **症状**：pong-01 部分对象（args.0.* 端口）仍有 runtime-input-missing
- **修复方向**：v8 中已对"既 produces 又 consumes"做了 carve-out，但 mutates pattern 没覆盖

---

## 7. 整体评价

**v8.4 是 kcpos 第一次在并行场景下达成多数完成**：

- 4/5 走完整 lifecycle（含 gate PASS）
- 累计 23 个对象真过测
- 0 次撞 iteration cap
- 0 次绕 gate

**剩余卡点集中在"应该过但 gate 不让"**（pong-01）—— 不是 agent 不努力，是 gate 规则在 "kind=test ok=true 必须" 这条上太硬，不接受合理的 obstacle + waiver 替代。

完成 P0 修复后，下一轮跑 5 实例完成率应能到 5/5。

---

## 8. 交付物状态

- 5 个 index.html 都打开能跑（pong-04 / pong-05 验证最充分）
- pong-01 / pong-03 dual-source 模式下 inline 部分自洽，但若按 v7 同样路径走可能藏未发现的 inline-vs-impl 偏差
- 4/5 通过 checkpoint 8/8 PASS
- 累计消耗 DeepSeek 余额约 5-8 CNY
