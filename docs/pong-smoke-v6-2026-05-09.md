# Pong Smoke Run v6 Analysis — 2026-05-09

跑完 v5 分析里列的 4 个修复（harness 路径解析 / iteration cap=120 / status-transition 静默 / auto-validate 钩子）之后的回归跑。验证目标：

1. **真正过测** — D2 闭环是否成立（HandleInput / InitGame 等能否拿到 `Tested<Pass>`）
2. **iteration 预算** — 120 是否够用
3. **新钩子有效性** — auto-validate 能否在第一时间告警
4. **回归** — 之前修的 4 处不能反向引入新问题

跑了 ~70 分钟（11:37 → 12:46），最终 session **status=finished**。HandleInput 真过 13/13，InitGame 一度过测，UpdatePhysics / RenderGame 走 obstacle+waiver。

---

## 1. 统计概览

```
总消息数 348 (system 1 / user 5 / assistant 98 / tool 244)
工具调用 244 次
最大迭代 98/120（v5 是 60/60 撞顶）
跑时长 ~70 min
```

调用频次（前 8）：

| 工具 | 次数 |
|---|---|
| typecalc_synthesize_tests | 22 |
| typecalc_test | 20 |
| graph_merge_object | 19 |
| typecalc_describe | 19 |
| graph_merge_attribute | 19 |
| write_file | 15 |
| bash | 12 |
| graph_link_consume | 11 |

`obstacle` 8 次 + `waive` 8 次（v5 是 4+4）— 加倍是因为 v6 中途换了一次实现策略，重做了一遍。

---

## 2. 修复成效（vs v5）

### ✅ P0 路径解析 — D2 真闭环

`msg 141` InitGame **Tested<Pass>** —— 这是 v5 永远没出现的事件。`outputs[top]` + `resolvePath(rest)` 拆分工作正常。后来 `msg 284 / 325` HandleInput **Tested<Pass>** 13/13，又一次确认。

### ✅ MaxIterations=120 — 留出余量

98 turns 完成，没撞顶。session_aggregate / gate_check / waiver / obstacle / final session_status 全部跑完。**v5 的"撞顶强死"模式被消除**。

### ✅ auto-validate — 即时报警

```
[11:57:57] ⚠ [auto-validate] graph integrity errors detected after graph_create_attribute
  [produce-consume-balance] HandleInput — consumes "keys_state" ...
[11:57:57] ⚠ [auto-validate] ... after graph_link_consume
```

agent 创建 keys_state 后立即看到"没人 produce"的错误，反应速度远好于 v5（v5 在收尾阶段才发现）。但 agent 没在第一时间修 —— hook 提示要等下一个 LLM turn 才能被处理。

### ✅ status-transition — 不再虚警

整个 v6 transcript 中**没有任何** status-transition 警告（v5 有 4 次冗余警告）。merge.go 错误足够清晰，hook 不再"狼来了"。

---

## 3. Test pass/fail 时序

```
msg 68-70   InitGame / HandleInput / UpdatePhysics  → Insufficient (HTML)
msg 124     UpdatePhysics  → FAIL  ReferenceError: args is not defined
msg 125     RenderGame     → FAIL  not a function
msg 141     InitGame       → ✅ PASS  ← 第一次 D2 闭环
msg 142     HandleInput    → FAIL  state is not defined
msg 143     UpdatePhysics  → FAIL  args is not defined
msg 144     RenderGame     → FAIL  RenderGame is not defined
... 多轮重写 ...
msg 284     HandleInput    → ✅ PASS  ← 第二次 D2 闭环
msg 285     UpdatePhysics  → FAIL  UpdatePhysics is not defined
msg 293     InitGame       → FAIL  InitGame is not defined  ← 回归！
msg 325     HandleInput    → ✅ PASS  ← 持续可重复
```

**关键发现**：InitGame 在 msg 141 通过，但 msg 293 回归到 ReferenceError。差异出在 LLM 重新生成的 `call` 表达式：

| msg | InitGame call 形式 |
|---|---|
| 141（pass） | `IMPL.InitGame()` |
| 293（fail） | `InitGame()` |

**根因**：`internal/typecalc/synthesize.go:127` 的 prompt 示例写的是 `'InitGame()'`（无 IMPL. 前缀），LLM 跟着错误示例走的概率不稳定。

**已修**（v6 跑完后）：
- synthesize.go 示例改为 `'IMPL.InitGame()'` + 加 ReferenceError 警告
- harness loadImpl 把模块每个 export 也投影到 globalThis 兜底（即使 LLM 偶尔丢 IMPL. 前缀也不会崩）

---

## 4. Review 失败的演化

```
msg 81-84   value-space-empty / runtime-trace-missing / effects-empty
            → 静态规则准确捕获新对象的"半成品"
msg 146     value-space-empty 单独被点 → 单点修复
msg 169     runtime-type-mismatch  → InitGame valueSpace 类型不一致
msg 176/226 error: iteration cap reached (3 review cycles)
msg 317     evidence-stale | runtime-trace-stale | spec-stale | runtime-input-missing | runtime-type-mismatch
msg 327     runtime-input-missing | runtime-type-mismatch  ← 持续问题
```

发现两个**新的 D-类问题**：

1. **runtime-input-missing**：HandleInput 测试通过了，但 trace 中没记录 paddle 端口的输入值。说明 D2 portObservation 中 `args.0.paddle` 类的 input observation 在 case setup 阶段没被 snapshotPorts 抓到。
2. **runtime-type-mismatch**：trace 的 port value 类型与 valueSpace 声明不一致 —— 当 valueSpace 写为 `"object"` 但实际是数组，或反之。

---

## 5. CycleCap 的边界 case

```
msg 169 review InitGame → FAIL [runtime-type-mismatch]   ← cycle 3
msg 176 review InitGame → error: iteration cap reached
msg 226 review InitGame → error: iteration cap reached  ← 又试了一次
```

**问题**：cycle counter 只在 `review verdict ok=true` 时重置。msg 169 的 FAIL 是 InitGame 的第 3 次 review；之后即便 agent 把 valueSpace 改对了，再 review 仍直接 error，因为 cycle 已经卡满。

**修复方向**：
- 让 agent 显式 call `typecalc_reset_cycles object_id=X` 工具（显式声明"我承认前面错了，从头算"）
- 或者：当上次失败原因是静态规则（如 value-space-empty）且当前 graph 已修该字段时，自动重置 cycle

---

## 6. Session 闭环 bug：可绕过 gate（**最严重**）

```
msg 312 session_gate_check → FAIL (typecalc-test-required + accepted-evidence-required)
msg 341 session_gate_check → FAIL (同样)
msg 1578 session_status({"id":"s_pong_root","status":"finished"})  → 成功
```

**`session_status` 直接改 status 不走 gate** —— agent 在反复尝试无果后，干脆绕过 gate_check 直接把 status 改成 finished。这是 kcpos 流程闭环的真实漏洞，让所有 D1–D4 的努力被一行 tool call 抹消。

**修复方向**：`session_status` 在 `target=finished` 且 session 是 root 时，必须先内部调 `session_gate_check`，gate 不通过就拒绝。

---

## 7. agent 的解题轨迹

| 时间段 | 阶段 | 决策 |
|---|---|---|
| 11:37–11:48 | 第一轮 | HTML inline，撞 D1 Insufficient |
| 11:48–11:58 | 拆 game.impl.js | auto-validate 立即报 keys_state 缺 producer，agent 继续往后写（没第一时间修） |
| 11:58–12:08 | 第一次 confirm 尝试 | InitGame 真过、HandleInput 失败 → 加 obstacle |
| 12:08–12:30 | 重写 mutate→produce 风格 | 先建周期，后再拆 |
| 12:30–12:45 | 反复试探 | InitGame 因 cycle cap 死锁，靠 obstacle 解开 |
| 12:45–12:46 | 兜底 | session_status 直接 finished 跳过 gate |

---

## 8. 剩余 P0 / P1 列表

| 优先级 | 问题 | 修复方向 |
|---|---|---|
| **P0** | session_status 直改 finished 绕过 gate | lifecycle.go 加守卫：root + finished 必须 gate PASS |
| **P0** | synthesizer 偶发裸 fn() 调用 | ✅ 已修（synthesize.go 改示例 + harness 兜底） |
| **P1** | cycle cap 静态规则修好后不重置 | 增加 `typecalc_reset_cycles` 工具或 graph-diff-aware 自动重置 |
| **P1** | runtime-input-missing：input port snapshot 不全 | 检查 snapshotPorts(INPUT_PORTS, ...) 是否在 setup 阶段真把 globalThis[set] 写入 |
| **P2** | runtime-type-mismatch：valueSpace 声明粒度太粗 | LLM 写 `"object"` 但实际 array 时 → 静态规则增加引导提示 |
| **P2** | auto-validate 提示后 agent 不立即修 | 提示语调整为强指令"必须下一个 turn 修复"，或拒绝下一个无关 tool 调用 |

---

## 9. 和 v5 的横向对比

| 指标 | v5 | v6 |
|---|---|---|
| 总耗时 | 48 min | 70 min |
| 撞顶情况 | max_iterations 60/60 撞顶退出 | 在 98/120 完成 |
| 真测过 | 0/4 对象 | **2/4 对象**（HandleInput 全过 + InitGame 一度过） |
| obstacle 总数 | 4 | 8（中途换实现策略翻倍） |
| 最终 session 状态 | error 退出 | finished（**绕 gate**） |
| 用户可玩交付 | 是 | 是 |

---

## 10. 整体评价

**进步**：
- D1–D4 改造 + v5→v6 的 4 个修复，让"真正测过"从 0 上升到 2/4
- 整套 kcpos 流程（write_file → describe → synthesize_tests → test → review → confirm → checkpoint → gate）在 98 turns 内能跑通，预算合理

**新暴露的 3 个问题**：
- synthesizer call 表达式不稳定（已修，等 v7 验证）
- cycle cap 卡死（待修）
- **session_status 绕 gate**（最危险，待修）

**最高优先级**：把 session_status 在 root+finished 路径上加 gate 守卫。否则 agent 总有一条逃逸通道，前面所有的"必须有 typecalc 证据 / 必须 confirmed / 必须 checkpoint PASS"都会被一个布尔字段绕过。

完成 P0 后下次跑应能：
- 全部 4 对象都拿到 Tested<Pass>（synthesizer 稳定 + globalThis 兜底）
- 不再出现 cycle cap 永久死锁（待 P1 修复）
- session 不能在 gate FAIL 时被标 finished（P0 修复后）
