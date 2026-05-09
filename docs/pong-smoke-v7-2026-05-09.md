# Pong Smoke Run v7 Analysis — 2026-05-09

跑完 v6 分析里列的 6 个修复（session_status gate guard / cycle 进展检测 / cycle graph-diff reset / runtime-input-missing 兜底 / auto-validate 强指令 / synthesizer call 前缀）之后的回归跑。

**核心结论**：第一次完整闭环。6/6 对象真测过，session 走 gate PASS finished，没绕路。

---

## 1. 统计概览

```
总消息数 418  (system 1 / user 5 / assistant 112 / tool 300)
工具调用 300  次
跑时长 ~70 min  (13:25 → 14:35)
迭代次数 112/120 (用了 93%)  ← 离顶 7 步
```

调用频次（前 8）：

| 工具 | v5 | v6 | v7 |
|---|---|---|---|
| typecalc_review | 12 | 10 | **35** |
| typecalc_synthesize_tests | 17 | 22 | 26 |
| typecalc_test | 20 | 20 | 23 |
| graph_merge_object | 19 | 19 | 31 |
| typecalc_describe | 17 | 19 | 27 |
| write_file | 16 | 15 | **35** |
| typecalc_waive | — | 8 | 12 |
| typecalc_obstacle | — | 8 | 6 |

`review` 翻 3 倍：cycle 进展检测让 agent 敢多 review；review 不再花钱买 cycle。
`write_file` 翻 2 倍：agent 多次重写实现以满足 axis-by-axis 测试要求。

---

## 2. 修复实测情况

| 修复 | 触发次数 | 效果 |
|---|---|---|
| **auto-validate "Required next turn"** | 9 次 | agent 几乎都立即响应 |
| **gate guard refusing-to-mark** | **0 次** | 没触发 — agent 没尝试绕路 |
| **iteration cap reached** | 1 次 | 仅 1 次撞顶（v5/v6 是 4 次） |
| **cycle progress reset** | 大量背后跑 | 终态 cycle count 全部为 0 |

**最戏剧化的成果**：0 次 gate guard 拒绝。要么 agent 没再尝试绕路，要么 cycle reset + progress detection 让 agent 总能找到正路。

---

## 3. Test 通过的演化

```
msg 93-96    InitGame/UpdatePaddle/UpdateBall/CheckCollision → Insufficient (HTML)
msg 160-162  第一次 JS：1/3, 1/9, 1/8 全部 TestError
msg 171-174  InitGame ✅, UpdatePaddle ✅, 其他 1/8 / 1/9
msg 194-195  UpdateBall ✅ ← runtime-input-missing 修复显效
msg 218      UpdatePaddle ✅ (runtime-input-missing 又过)
msg 292      Render ✅ (改 produces 后真过)
msg 317      CheckCollision ✅
msg 368      UpdateBall ✅ (axis-by-axis 改写后 13/13)
msg 385      ReadInput ✅
```

**最后真测过的对象**：6/6（v5: 0/6，v6: 1/6 + 1 偶过）。

---

## 4. Review 的演化

```
msg 98       value-space-empty + runtime-trace-missing  ← 静态规则
msg 122-127  全部 runtime-trace-missing                  ← 没有 trace（HTML 跑不动）
msg 202-204  spec-stale + runtime-input-missing         ← D3 evidence-stale 显效
msg 205      CheckCollision PASS  ← 第一次 review PASS
msg 211      InitGame PASS
msg 212-213  UpdatePaddle/UpdateBall runtime-input-missing  ← v6 老问题
msg 230-231  又来一遍
msg 294      Render runtime-input-missing                ← Render 也撞这个
msg 326      Render runtime-input-missing
msg 338      UpdatePaddle PASS  ← runtime-input-missing 终于过了
msg 345-346  UpdateBall + Render PASS
msg 391-399  ReadInput PASS（最后一个）
```

---

## 5. obstacle / waive 使用

**obstacle 6 次**（v6 是 8 次）：

| msg | 对象 | 原因 |
|---|---|---|
| 196 | ReadInput | 读全局 keys 对象，未被 graph 端口建模 |
| 196 | Render | 纯 canvas 副作用，无输出端口 |
| 223 | UpdatePaddle | harness 不录 consume 端口的 trace |
| 223 | UpdateBall | 同上 |
| 319 | UpdateBall | 测试 y 期望与 axis-by-axis 实现冲突 |
| 371 | ReadInput | synthesizer 生成 legacy 格式 + JSON 语法错 |

**waive 12 次**（v6 是 8 次）：

- 每对象 HTML 阶段一个 waiver（6 个，msg 114）
- 后期 JS 阶段补充（msg 199, 226, 319, 371）

---

## 6. Session Lifecycle

```
msg 256  session_aggregate → 6 implementations / 3 tests
msg 278  session_gate_check → FAIL (CheckCollision typecalc-test-required)
msg 350  session_gate_check → FAIL (ReadInput typecalc-test-required)
msg 401  session_gate_check → PASS  ← 第一次过 gate
msg 411  session_gate_check → PASS  ← 复查再过
msg 413  session_status(finished) → s_pingpong status → finished  ← 走正路
```

**关键时刻**：msg 401 第一次 gate PASS。从 msg 278 第一次 FAIL 到 msg 401 PASS 中间经历了 123 个消息 — agent 真的去把所有问题修了，**没绕路**。这是 P0 gate guard 的核心收获。

---

## 7. agent 的决策模式

| 时间段 | 阶段 | 行为 |
|---|---|---|
| 13:25–13:35 | HTML inline 起手 | 撞 D1 Insufficient，全 6 对象 waiver |
| 13:35–13:50 | 拆出 K/impl/*.js | runtime-trace-missing（trace 还没生成） |
| 13:50–14:00 | 第一轮真测试 | 部分 PASS（InitGame/UpdatePaddle）+ 部分 TestError |
| 14:00–14:15 | 修测试 | UpdateBall 改 axis-by-axis；逐个攻关 |
| 14:15–14:25 | runtime-input-missing 修 | harness 兜底 globalThis 在 args.* extractor 里生效 |
| 14:25–14:35 | gate 收尾 | aggregate → gate FAIL → 修缺的 → gate PASS → finished |

---

## 8. 新观察到的问题（剩余 P1/P2）

### P1：synthesizer 偶尔生成 legacy 格式

```
msg 360 ReadInput → FAIL 0/1 with JSON syntax error
msg 371 obstacle: "synthesizer generated legacy-format with broken JSON"
```

LLM 在某些 prompt 上会忽略 schema 要求，输出 raw test code（带 JSON 语法错）。建议 `synthesize.go` 严格 fail-on-invalid-json，强制 LLM 重新生成。

### P1：harness 不记录 consume 端口的 trace

agent 抱怨 4 次（msg 223, 294, 326）：Tested<Pass> 但 review 报 runtime-input-missing。

**根因**：harness `appendTrace(inputs, outputs)` 中 `inputs` 来自 `snapshotPorts(INPUT_PORTS, ...)`，对于 `args.0.<path>` extractor 虽然 v7 加了 globalThis 兜底，但在 input snapshot 阶段如果 `globalThis[port]` 没设过（端口没 setup）就仍然是 undefined。

**修复方向**：synthesizer 应保证每个 INPUT_PORT 都有 setup 条目，或 harness 在所有 input 端口缺值时给一条结构化错误而不是默默 undefined。

### P2：iteration 用了 93%

112/120，离顶 7 步。如果 agent 多走两个失败回合就会撞顶。建议放到 150。

### P2：TestError 不写 kind=test 证据

CheckCollision 6/8 通过 → TestError → 只有 kind=compile，gate 看不到 kind=test → typecalc-test-required FAIL。

**修复方向**：typecalc_test 在 TestError 时也写 kind=test 证据，标 `ok=false`，让 gate 能区分"真没测"和"测了部分通过"。

---

## 9. 和 v5/v6 的横向对比

| 指标 | v5 | v6 | v7 |
|---|---|---|---|
| 总耗时 | 48 min | 70 min | 70 min |
| 迭代用量 | 60/60 撞顶 | 98/120 | 112/120 |
| 真测过 | 0/4 | 1/4（+1偶过） | **6/6** |
| obstacle 数 | 4 | 8 | 6 |
| waive 数 | 4 | 8 | 12 |
| session_status finished | error 退出 | **绕 gate** | **走 gate PASS** ✅ |
| checkpoint | 没填 | 8/8 PASS | 7/7 PASS |
| 用户可玩 | 是 | 是 | 是 |

**v7 是第一次完整闭环** — 不绕 gate、6/6 真过测、checkpoint frozen、走标准 lifecycle。

---

## 10. 整体评价

D1–D4 + v6→v7 的 6 个修复完全到位：

- **工作流正常**：write_file → describe → synthesize → test → review → confirm 的标准链路第一次走通
- **没绕路**：session_status 在 gate FAIL 前被拦住，agent 老老实实修问题
- **没卡死**：cycle reset 和 progress detection 让 UpdateBall 重写两次都没撞 cap
- **没虚警**：auto-validate 9 次都触发了真问题

剩余 P1 主要是 harness/synthesizer 层面的小补丁：

- consume 端口 trace 不全（synthesizer 应保证每个 input 端口有 setup）
- TestError 不写 kind=test（gate 看不到部分通过的事实）
- legacy 格式漏检（synthesize.go 应严格 reject）

完成这些后下次跑应能：
- 不再出现 runtime-input-missing 的反复失败
- TestError 也能让 review 走完（agent 可以基于"部分通过"做 reasonableness 判断）
- 不会因为 synthesizer 偶发 legacy 格式被卡 1 个对象
