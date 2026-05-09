# Pong Batch v8.5 (5 parallel) — 2026-05-09

v8.5 修了 v8 batch 报告里的 P0/P1（obstacle+waiver 替代 / typecalc-use 阻断 / checkpoint_fill 拒空头支票 / dual-source 阻断 / synthesizer 严格命名）后的回归并行测试。

5 实例同时启动 18:21，2 个 finished、3 个仍 active 但 4 个对象都已 confirmed。报告写于 19:30，pong-03/04 还在工作。

---

## 1. 全 5 实例终态总览

| 实例 | 根 session | confirmed | kind=test | TP | TE | Insuf | obstacle | waive |
|---|---|---|---|---|---|---|---|---|
| pong-01 | **active** ⚠️ | 5/5 | 3/5 | 6 | 5 | 6 | 7 | 7 |
| pong-02 | **finished ✓** | 3/3 | **3/3** | 0 | 0 | 1 | 0 | 0 |
| pong-03 | active | 4/4 | 2/4 | 0 | 0 | 13 | 12 | 11 |
| pong-04 | active | 4/4 | 2/4 | 12 | 10 | 6 | 11 | 9 |
| pong-05 | **finished ✓** | 4/4 | **3/4** | 2 | 0 | 0 | 3 | 4 |

**完成率 2/5（40%）**，4/5 全部对象 confirmed，但 3 实例卡在 root session active。

vs 历史：
- v6: 1/1 finished but **绕 gate**
- v7: 1/1 finished gate-PASS（单跑）
- v8.4: 4/5 finished，但 2 个走 dual-source、1 个 active 卡 kind=test
- **v8.5: 2/5 finished（保守路径），3/5 仍 active（卡 accepted-evidence 漏洞）**

数字看 v8.5 退步了，但**根因是 v8.5 暴露了 v8.4 没看到的更深漏洞**（accepted-evidence-required 不接受 obstacle+waiver）。

---

## 2. v8.5 修复实测情况

| 修复 | 5 实例累计触发 | 实际效果 |
|---|---|---|
| **P0 obstacle+waiver = kind=test 替代** | 0 直接触发，但**间接生效** | pong-05 UpdateGame 走此路径成功 finish |
| P1 typecalc-use 阻断 | **0 次** | agent 自发顺序对，不需要拒绝 |
| P1 checkpoint_fill 拒空头支票 | **0 次** | agent 已学会先 confirm 后 fill |
| P1 dual-source 阻断 | **0 次** | **5/5 全部 impl=index.html**（v8.4 是 3/5）|
| P1 synthesizer 严格命名 | **0 次** | call 表达式 casing 不再错乱 |

**最大胜利：5/5 single-source**。v8.4 时 2 个实例创建了 `.impl.js` 影子文件，v8.5 完全消除。但其他 4 个修复"硬" 0 触发，说明都是兜底防线，agent 凭借更新的 system prompt 自发走对路径。

---

## 3. 单实例分析

### pong-01（卡死 — 暴露新洞）

```
5/5 confirmed · 32 typecalc_test · 31 typecalc_review · 277 tool calls
最终 root active，agent 知道绕 gate 会被拒 → 自发停下汇报
```

**测试 PASS 分布**：
- SetupCanvas / ReadInput / InitGame：4-7 次尝试都 PASS → 全机械验证
- UpdatePaddle: 11 次尝试 → **1 次 PASS**（msg 265 重写后）
- UpdateBall: 9 次尝试 → **从未 PASS**

**走 obstacle+waiver 的对象**：UpdatePaddle / UpdateBall（v8.5 P0 想接的场景）

**最终 gate FAIL 原因**：
```
✗ [accepted-evidence-required] UpdateBall review verdict failed:
    static or runtime check produced issues
✗ [accepted-evidence-required] UpdatePaddle review verdict failed
```

agent 在 obstacle 描述了：
- UpdatePaddle 失败是 **synthesizer expectation bug**（期望 paddle_x=-4 但 spec 要求 clamp ≥ 0）
- UpdateBall 失败是 **synthesizer 浮点精度 bug + 物理碰撞反向假设**

**v8.5 P0 漏洞**：我把 obstacle+waiver 接入了 `typecalc-test-required` 和 `typecalc-evidence-passing`，但**没接入 `accepted-evidence-required`**。typecalc_review 内部当静态/运行时层有 issues 时不跑 reasonableness，输出 `verdict.OK=false`，gate 这层不接受 obstacle+waiver 兜底。

### pong-02（最干净的一个 — 完全机械验证）

```
3/3 confirmed · 19 test calls · 11 reviews · 0 obstacle · 0 waive
session_status finished ✓ · checkpoint PASS 7/7
```

**测试 PASS 分布**：
- InitGame: 7 attempts → 4 PASS
- UpdatePaddle: 6 attempts → 2 PASS
- UpdateBall: 6 attempts → 2 PASS

**评审演化**：
```
msg 161-163 全 FAIL [value-space-empty + evidence-stale + runtime-trace-stale]
msg 196      InitGame PASS（修 valueSpace 后）
msg 217      UpdateBall PASS（最后一个）
msg 268-272  aggregate → gate → finish ✓
```

**关键间接成果**：早期遇到 `synthesizer malformed JSON envelope` 错误（v8 的 P1 fix），agent 重试合成第二次成功。如果没那个 fix，会 silent 接受 broken raw test code 进入下游 crash。

**为什么不需要 obstacle+waiver**：pong-02 选择对象更小（3 个），每个 review 反复推到 PASS，没用兜底。

### pong-03（compile-only，靠 obstacle+waiver 兜底）

```
4/4 confirmed but 2/4 kind=test only · 13 Insufficient · 12 obstacle · 11 waive
仍 active，预计将卡 accepted-evidence-required
```

**evidence 现状**：
- InitGame: kind=compile + accepted ok=True
- ReadInput: kind=compile + waiver（无 accepted）
- RenderFrame: kind=compile + waiver（无 accepted）
- StepPhysics: kind=compile + accepted ok=True

**预期问题**：4/4 都 kind=compile 不是 kind=test。HTML 项目 v8 把 LangHTML 加进 testable list，gate 会要求 kind=test。除非 obstacle+waiver 对 compile-only 也算合法。需要查 gate 是否接受。

### pong-04（最高 TP，但 review 卡住）

```
4/4 confirmed · 12 TP · 10 TE · 11 obstacle · 9 waive
仍 active，evidence 状态分裂：
  InitGame:    test ok=True + accepted ok=True ✓
  UpdatePaddle: test ok=True + accepted ok=True ✓
  UpdateBall:   test ok=False + accepted ok=False + obstacle + waiver
  RenderGame:   test ok=False + accepted ok=False + obstacle + waiver
```

**和 pong-01 一样的 P0 漏洞**：agent 已经做了完整 obstacle+waiver 路径，但 review verdict.OK=false → gate 拒。pong-04 比 pong-01 离完成更近（已有 12 TP，更多机械验证），但卡同样的洞。

### pong-05（最高效完成）

```
4/4 confirmed · 32 min（最快）· 9 reviews · 3 obstacle + 4 waive
session_status finished ✓ · checkpoint PASS 7/7
```

**走的混合路径**：
- InitGame / InputHandler / RenderFrame：Tested<Pass> + Review PASS
- UpdateGame：15/16 测试 PASS，1 个失败是 synthesizer 内部不一致（bias 偏移角度算错）→ obstacle + waiver

**为什么能过**：UpdateGame review 在第 3 次（msg 151）PASS — 静态/运行时 issues 全清后 reasonableness 跑通，verdict.OK=true。所以 gate 走 acc.OK 这条路通过，**不依赖 v8.5 P0 carve-out**。

---

## 4. 最大发现：accepted-evidence 是 v8.5 P0 没覆盖的盲点

```
v8.4 → v8.5 改进路径：

v8.4 卡点：kind=test ok=false / kind=compile only → gate FAIL
   ↓ v8.5 P0 修：obstacle+waiver 视为 kind=test 替代
v8.5 新卡点：accepted ok=false → gate FAIL
   ↓ 应修：obstacle+waiver 视为 accepted 替代
```

**根因机制**：
1. typecalc_review 内部当 static/runtime issues 非空时**直接拒绝跑 reasonableness LLM**
2. 输出 `verdict.OK=false, reasons=["static or runtime check produced issues"]`
3. gate 看到 `acc.OK=false` 就 FAIL

agent 走 obstacle+waiver 是承认"static/runtime issues 是 synthesizer bug、不是代码 bug"。reasonableness 没跑过 ≠ 代码错。但 gate 把这等同处理了。

**修复方向**：[internal/session/gate.go:223-231](kcpos/internal/session/gate.go#L223-L231)：

```go
if !acc.OK && !passViaWaiver {  // ← 加 passViaWaiver
    r.Issues = append(r.Issues, fmt.Sprintf(
        "[accepted-evidence-required] object %s review verdict failed: %s",
        objID, reasons))
}
```

---

## 5. 关键观察

### 5.1 路径选择两极分化

5 个实例按"用 obstacle+waiver 程度"分：

| 策略 | 实例 | obstacle+waiver | 结果 |
|---|---|---|---|
| **零兜底** | pong-02 | 0+0 | finished ✓ |
| **轻兜底** | pong-05 | 3+4 | finished ✓ |
| **重兜底** | pong-01 / 04 | 7+7, 11+9 | active 卡死 |
| **极重兜底** | pong-03 | 12+11 | 仍 active（kind=compile only） |

**核心规律**：用 obstacle+waiver 越多 → 越容易撞上 accepted-evidence 漏洞。pong-02/05 之所以能完成是因为它们**最终都让 review PASS**，没在 acc.OK=false 上卡。

### 5.2 5 实例对象数差异巨大

| 实例 | 对象数 | 对象拆分 |
|---|---|---|
| pong-02 | 3 | InitGame / UpdateBall / UpdatePaddle |
| pong-05 | 4 | + InputHandler / RenderFrame |
| pong-03 / 04 | 4 | 4 类常见拆法 |
| pong-01 | 5 | 加了 SetupCanvas / ReadInput |

对象数越少越容易完成 —— 因为每多一个对象，多一次撞 review 漏洞的机会。

### 5.3 0 enforcement triggers

5/5 都没触发 v8.5 任何阻断（ev/dual/fill/finish）。意味着：
- 修复在防御层无作用 = agent 自发顺序对
- 修复的真正价值在改 system 行为可达性 = obstacle+waiver 真路径打通

但**真正生效的不是 P0 阻断，是 system prompt 引导**。agent 在第一回合就 impl=index.html，第一次就先跑 typecalc 再 confirm。这暴露了 kcpos 修 bug 的策略：**system prompt 引导 ≥ 工具阻断**。

---

## 6. 数字汇总

| 维度 | pong-01 | pong-02 | pong-03 | pong-04 | pong-05 |
|---|---|---|---|---|---|
| Total tool calls | 277 | 190 | (跑中) | (跑中) | 114 |
| Assistant turns | 104 | 79 | (跑中) | (跑中) | 57 |
| Objects | 5 | 3 | 4 | 4 | 4 |
| Confirmed | 5/5 | 3/3 | 4/4 | 4/4 | 4/4 |
| kind=test ok=True | 3/5 | 3/3 | 2/4 | 2/4 | 3/4 |
| Tested<Pass> 计数 | 6 | 0(*) | 0 | 12 | 2 |
| obstacle | 7 | 0 | 12 | 11 | 3 |
| waive | 7 | 0 | 11 | 9 | 4 |
| Root finished | ❌ | ✓ | ❌ | ❌ | ✓ |
| Index.html bytes | (待查) | (待查) | (待查) | (待查) | (待查) |

(*) pong-02 transcript 显示 InitGame/UpdatePaddle/UpdateBall 都最终 Tested<Pass>，但 grep 字面 "Tested<Pass>" 在日志找不到 —— 可能是 agent 用中文表述结果，原始 tool 返回值嵌入到 transcript 不进 stderr。

---

## 7. P0/P1 残留清单（v8.5 暴露 + 未修）

### **P0: accepted-evidence-required 不接受 obstacle+waiver**（pong-01 / 04 卡死的真因）

- 症状：obstacle+waiver 路径完整 → gate 仍 FAIL
- 修复：上面 §4 给的 `!passViaWaiver` 条件加在 acc.OK 检查上
- 影响：pong-01/04 完成

### **P1: typecalc_review 在 static/runtime issues 非空时拒跑 reasonableness**

- 症状：单一 issue 让 review 不进 LLM 阶段，verdict.OK=false 导致 gate 看不到代码本身的合理性判断
- 修复方向：让 reasonableness 在 static/runtime issues 存在时仍跑（标 confidence 较低），而非短路 fail
- 影响：减少误报"基础证据失败"

### **P2: kind=compile only 应该是不被接受的（除非 obstacle+waiver）**

- 现状：pong-03 4/4 都是 kind=compile only，预计撞 typecalc-test-required
- 这是 v8 设计意图：HTML 应被当作可测语言。pong-03 没把 typecalc_test 推到底，写了 obstacle+waiver。需要看 gate 是否将 obstacle+waiver 与 kind=compile only 配合视为合法。

### **P2: `Tested<Pass>` 字面在 log 中难追踪**

- pong-02 evidence 显示 3/3 test ok=True，但 log grep 0 次
- 字符串 "Tested<Pass>" 可能只在 tool result 里，不输出到 stderr 日志
- 不影响功能，但影响调试可观测性

---

## 8. 整体评价

**v8.5 是迭代深度而非完成率提升**：

- 完成率 4/5 → 2/5 看似退步
- 但发现的 bug 更深层：**accepted-evidence-required vs obstacle+waiver 不对称**
- 5/5 single-source 是质的进步（dual-source 阴影完全消除）
- v8.5 P0 的 carve-out 设计正确，但**作用层级错了**（应在 acc.OK 而非仅 ev.OK）

**修 P0（accepted-evidence carve-out）后预期**：
- pong-01 / 03 / 04 都能 finished（其 obstacle+waiver 路径已完整）
- 完成率应到 5/5

**v8.5 路径选择启示**：
- 对象数少（≤3）+ 不依赖 obstacle/waiver = 最稳完成（pong-02 / pong-05 路径）
- 对象数多 + 重 obstacle = 撞 accepted-evidence 漏洞（pong-01 / 03 / 04）
- system prompt 引导比工具阻断更有效（v8.5 5 个 P0/P1 修复都没真触发）

剩余动作：补 P0（accepted-evidence carve-out），再跑一次 5 实例验证。
