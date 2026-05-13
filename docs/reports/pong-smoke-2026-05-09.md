# Pong Smoke Run Analysis — 2026-05-09

跑 D1–D4 改造后的第一次 pong 端到端，目标是验证：
1. HTML 不会再 fail-open
2. agent 是否会主动让代码可观测（D2 的预期行为反转）
3. evidence 哈希绑定是否能拒掉 stale 证据
4. CycleCap 是否能阻断死循环并产生结构化 obstacle

跑完时间约 48 分钟（10:26 → 11:14），最后撞 `max iterations (60)` 退出。session_aggregate 没跑完，但 5 对象 + 3 属性全部 confirmed。

---

## 1. 统计概览

```
总消息数 228 (system 1 / user 7 / assistant 60 / tool 160)
工具调用 161 次
```

调用频次（前 8）：

| 工具 | 次数 |
|---|---|
| typecalc_test | 20 |
| graph_merge_object | 19 |
| typecalc_describe | 17 |
| typecalc_synthesize_tests | 17 |
| write_file | 16 |
| typecalc_review | 12 |
| graph_merge_attribute | 12 |
| read_file | 6 |

`typecalc_review` 12 次 = 3 批 × 4 对象，正是 D4 CycleCap 计数到顶的轨迹。

---

## 2. Review 失败原因的演化

### Round 1（msg 62–66，10:30）— 静态规则发现真实漏洞

```
InitGame  → [value-space-empty] game_state 没填 valueSpace
UpdateBall  → 同上
UpdatePaddle → 同上
RenderGame → [effects-empty] produces=[] AND mutates=[]
```

**结论**：D2 配套的静态规则真的捕到了 graph 半成品状态。没有 valueSpace、没声明 effects 的对象被拒绝 confirm。

### Round 2（msg 86–89，10:40）— D3 evidence-stale 触发

```
[evidence-stale] InitGame — compile/test evidence is for impl hash b008dbe9
                            but the current impl is 85687540 — re-run
                            typecalc_compile or typecalc_test
[spec-stale]    InitGame — describe was for old impl hash
```

**结论**：agent 在 Round 1 之间 edit 了 index.html，impl hash 变了，D3 哈希绑定立即把 stale 旧证据拒掉。如果没有 D3，这些 evidence 会继续被当成有效证据。

### Round 3（msg 110–114，10:48）— harness 实际执行后的 runtime-trace-empty

```
[runtime-trace-empty] InitGame — runtime trace exists but recorded zero
                                  calls — tests did not exercise the function
reasonableness: fail (confidence 1.0)
```

**结论**：到这里 agent 已经把 HTML 拆出 pong.js（msg 110 之前），harness 真的能加载 JS 了，但 tests 全 fail（assertion error），所以 `appendTrace` 写了空 calls。

---

## 3. 根本 bug：harness port 路径解析不完整（**新发现**）

最关键的问题，从 msg 137 的 TestError 抠出：

```
✖ initial game is not over and score is zero
  AssertionError: [game_state.gameOver] expected false, got undefined
```

### 三方对照

**synthesizer 生成的 case**（`InitGame.tests.json`）：

```json
"call": "IMPL.InitGame(canvasWidth, canvasHeight)",
"expect": [
  { "port": "game_state.score", "equals": 0 },
  { "port": "game_state.gameOver", "equals": false }
]
```

**graph 中的 portObservation**：

```json
"portObservation": { "game_state": "return" }
```

**harness 实际逻辑**（`internal/typecalc/harness/harness.go:180-211`）：

```js
function snapshotPorts(ports, lastReturn, callArgs) {
  for (const p of ports) {
    const ex = PORT_OBSERVATION[p] || 'global';
    if (ex === 'return') { out[p] = lastReturn; continue; }
    // ...
  }
}
```

assertion 阶段（line 320）：

```js
const v = outputs[exp.port];   // exp.port = "game_state.score"
                                // outputs has key "game_state", not "game_state.score"
                                // → v = undefined
```

### Bug 描述

- synthesizer 生成 `port: "<port>.<sub_path>"` 形式
- harness 只用 PORT_OBSERVATION 解析顶层 port name
- 没把 port 后面的 sub-path 再 resolve 一次

agent 在 obstacle.json 中准确诊断了这个 bug：

```
Test harness port-observation mismatch: harness wraps return values differently
than the portObservation 'return' contract specifies. The function InitGame
returns a correct GameState object (verified manually via Node), but harness
assertions use port paths like 'game_state.score' that resolve to undefined.
```

### 修复建议

`internal/typecalc/harness/harness.go:319-323`：

```js
for (const exp of (c.expect || [])) {
  const dot = exp.port.indexOf('.');
  const top = dot < 0 ? exp.port : exp.port.slice(0, dot);
  const rest = dot < 0 ? '' : exp.port.slice(dot + 1);
  const v = rest ? resolvePath(outputs[top], rest) : outputs[top];
  const [pass, msg] = checkExpectation(exp, v);
  assert.ok(pass, '[' + exp.port + '] ' + msg);
}
```

这是 4 个对象全部 obstacle 的真因。修这一处后 D2 才算真闭环。

---

## 4. D1–D4 实际触发情况

| 方向 | 触发证据 | 行为 |
|---|---|---|
| **D1 Insufficient** | msg 53–56 — 4 个对象 HTML 全返 Insufficient (`no in-tree test runner for language HTML`) | 没 fail-open，agent 必须主动 waiver 或转 JS |
| **D2 PortObservation** | msg 64 effects-empty / msg 110 runtime-trace-empty | agent 主动从 HTML 拆出 pong.js + globalThis 模式（**真正的行为反转**） |
| **D3 Evidence-stale** | msg 86 — `b008dbe9 vs 85687540` 哈希不一致 | 强制重跑 describe/synthesize/compile，旧证据失效 |
| **D4 CycleCap** | InitGame.cycles.json count=3，4 对象全部产生 obstacle.json | review 撞 cap → typecalc_obstacle → typecalc_waive → confirm |

---

## 5. agent 的解题轨迹

| 时间段 | 阶段 | 决策 |
|---|---|---|
| 10:26–10:35 | Round 1 | 直接全部 HTML inline，撞 D1 Insufficient → 全 waiver |
| 10:36–10:42 | Round 2 | review 报 evidence-stale，重跑链路刷新 hash |
| 10:48–10:58 | Round 3 | review 报 runtime-trace-empty + harness 跑出 ReferenceError → 反思：harness 是用 require 加载，HTML inline 不可达 |
| 11:00–11:09 | Round 4 | **拆 pong.js + globalThis + module.exports**，三次微调让 IMPL 命名空间正确 |
| 11:09–11:11 | Round 5 | 测试 assertion 仍 fail（路径解析 bug）→ obstacle + waiver → confirm |
| 11:11–11:14 | 收尾 | confirm 5 对象 + 3 属性 + 补加 CaptureInput → graph_validate PASS → session_aggregate **撞 60 步上限** |

---

## 6. 可改进的具体问题

### P0：harness port 路径解析不完整
位置：`internal/typecalc/harness/harness.go:319-323`
说明：见第 3 节。这是 D2 闭环的关键，修完后 pong 应该能真测过。

### P0：max_iterations=60 太紧
session_aggregate / gate_check / 多轮迭代加起来 60 步根本不够。pong（4 对象 + 1 补加）已用 ~58 步就撞顶。建议提到 120，或对收尾步骤豁免计数。

### P1：graph_merge_attribute 状态机告警但放行
msg 11:11:50：`status-transition declared → confirmed` 只报 warn 没拦住。隐式信号：状态机宽松，攻击面大（agent 可以跳过 implementing）。

### P1：CaptureInput 后期才补加
msg 168 之后才发现 input_keys 没人 produce。`produce-consume-balance` 应在每次 graph 编辑后强制运行，而不是 graph_validate 收尾时才暴露。

### P2：synthesize_tests 单次太慢
parallel batch 4 个 synthesize_tests 用了 ~6 分钟（11:02–11:08）。LLM-bound，但可以考虑 case 数从 5–7 降到 3。

---

## 7. 整体评价

D1–D4 的改造**完全达到设计目标**：

- 旧版本：4 对象都会被标 confirmed 但 HTML 测试根本没跑（fail-open）
- 新版本：agent 被迫转换实现策略（HTML→JS），并准确报告了 harness 的真实 bug
- obstacle.json 提供结构化的故障声明，比沉默成功有用

剩下的工作集中在：

1. harness 路径解析 bug（最值钱）
2. 提高 iteration 预算
3. 收紧状态机

完成 1–3 后，下次 pong 跑应能在不依赖 obstacle 的情况下直接测过 + confirm。
