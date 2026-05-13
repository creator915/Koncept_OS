# Terraria batch v9.2 — 2026-05-12

Run: 2026-05-12 17:46 启动（mid-flight 因 gate HTML 漏洞重启于 18:58），
5 并行实例，DeepSeek deepseek-v4-pro，运行 ~110min 后 4 个被 user kill，
1 个（v92-01）自然 EXIT 但 root 仍 active。

## 5 实例最终状态

| 实例 | spawn | build | smoke | confirmed | deliverable | 终止方式 | 报告 |
|---|---|---|---|---|---|---|---|
| **v92-01** | 3 | 6 | 16 | **14/14** | 72KB / 干净 / 0 ESM | 自然 EXIT, root=active（gate 卡 accepted-evidence-required）| [v92-01.md](terraria-v92-01.md) |
| v92-02 | 2 | 8 | 7 | 0/9 | 68KB / 干净 / 0 ESM | user kill（session_delete s_root 灾难后 rebuild）| [v92-02.md](terraria-v92-02.md) |
| v92-03 | 4 | 8 | 1 | 2/14 | 9.9KB / 干净 / 0 ESM | user kill（链式 spawn + typecalc_test 命名陷阱）| [v92-03.md](terraria-v92-03.md) |
| v92-04 | **7** | **0** | **0** | 1/8 | **无** | user kill（typecalc_test 死磕，从未 build）| [v92-04.md](terraria-v92-04.md) |
| v92-05 | 0 | 4 | 3 | 1/17 | 109KB / 干净 / 0 ESM / **deferred-start** | user kill（SaveLoad 56min 死磕）| [v92-05.md](terraria-v92-05.md) |

## 与 v9.0.6 同 prompt 同 SPEC 对比

| 维度 | v9.0.6（5/12 上午）| **v9.2**（5/12 晚）|
|---|---|---|
| 5/5 实例死于 LLM stream | 03 | **0** |
| `typecalc_waive` / `typecalc_obstacle` 调用 | 5/5 全员（蒙混过关）| **0**（工具已删，不可达）|
| structural waiver "Canvas/DOM/no-runner" 蒙混 | 5/5 通过 finish | **0**（无 waiver 退路）|
| 假阳 shipped 黑屏 deliverable | 4/5（实际 user 验证）| **0**（v92-01 干净；v92-02/03/05 也 0 ESM）|
| runtime_smoke 调用 | 不存在 | 1+3+7+16 共 27 次（v92-04 例外，0 次）|
| chromium 200MB 下载 | 不存在 | **0**（5/5 全 preflight discovery）|
| ESM 污染（`export default` 在非 module）| 01/04/05 全有 | **0/5** 实例 |
| session_delete 误用 | 03 child（rollback 4 def + edges）| **2/5 触发**（v92-01 child + v92-02 ROOT！）|
| Path B 使用率 | 1/5 | **4/5**（v92-05 是有意的 Path A racer）|
| 真触发 `[typecalc-test-required]` | n/a | **1 次**（v92-04，graph 建模错）|
| confirmed deliverable | 1/5（terraria-02，实际可玩）| **1/5** 看上去最干净（v92-01，未实测）|

## 跨实例发现汇总

### v9.2 设计的 5 个**确认有效**之处

1. **典型 v9.0.6 失败模式全部消失**：
   - 0 个 `typecalc_waive` / `typecalc_obstacle` 调用（工具不可达）
   - 0 个 "structural waiver Canvas/DOM" 蒙混 finish
   - 0 个 deliverable 有 ESM 污染（vs v9.0.6 的 4/5）
   - 0 个实例死于 LLM stream（v9.0.3 retry 持续生效）

2. **preflight discovery 完美**：5/5 都通过 cheap probe 或 runtime_link 发现现有
   playwright，**0 个 200MB chromium 下载**。

3. **Path B 普及**：4/5 采用 spawn_subagent（vs v9.0.6 的 1/5）

4. **agent 元认知改善**：多个实例 thinking 里**主动引用** v9.2 协议
   （"HTML deliverables skip [typecalc-test-required] in favor of
   [runtime-smoke-required]"），证明 system.md 教学起效

5. **deferred-start 模式自然出现**：v92-05 自己采用，与 v9.0.6 terraria-02
   唯一 playable 同款架构

### v9.2 新暴露的设计 gap（按严重度排序）

#### **Gap A**：`accepted-evidence-required` 卡死 HTML finish（最严重）
v92-01 14/14 confirmed + runtime_smoke 全 ok=true，但 review tool 只能读 8K
token 的 index.html（72KB），review verdict 必然失败。pre-v9.2 这会被 structural
waiver 蒙混 ship 黑屏；v9.2 让 agent **诚实卡死**，但 finish 路径同时被堵。

修复方向：review 默认读 `obj.ImplFragment` 而非 `obj.Impl`；或 chunked
review 切片处理大 HTML。

#### **Gap B**：HTML agent 仍主动跑 typecalc_test（v92-03/04/05 三连击）
v9.2 说"HTML 跳过 typecalc_test"，但 tool 仍可调，**3/5 实例** agent 主动调，
全部陷入 synth-test 命名/binding 不匹配陷阱。SaveLoad（v92-05）死磕 56 分钟、
CombatSystem（v92-04）死磕 100 分钟、entity_lists vs entityLists（v92-03）
死磕 30+ 分钟。

修复方向（**最高优**）：system.md 加 "for HTML deliverables, **do NOT
call typecalc_test on individual fragments** — go straight to runtime_smoke
per object_id"；或在 HTML 检测时**禁用** typecalc_test 调用。

#### **Gap D**：`session_delete` 仍是深度递归 rollback（v9.0.6 + v92-01 + v92-02 三连击）
- v9.0.6 terraria-03：删 child → rollback graphDiff + 4 def files
- v92-01：删 child s_impl_generateworld → rollback 3 sessions + 4 def files
- **v92-02：删 ROOT session → rollback 20 source files including index.html**

每次 agent 都把 session_delete 当 "cleanup" 误用。v9.0.6 PENDING 列表第 10 项
未修。

修复方向：(a) `session_delete` 在 ROOT 强制 `--force` 参数；(b) description
第一句加 "**DESTRUCTIVE — rolls back ALL graphDiff and deletes ALL files**"；
(c) 或拆成两个工具：`session_cleanup`（只删 session JSON）+ `session_rollback`
（毁灭性）。

#### **Gap E**：链式 spawn_subagent（v9.0.6 terraria-05 + v92-03 + v92-04 三连击）
v92-03 4 spawn 接链：`s_root → gamedata → worldgen → playerinput → particlesystem`
v92-04 7 spawn 接链：`s_root → gamedata → worldgen → gameengine → entitysystem → combatsystem → craftingsystem → rendergame`

每次 spawn 后 focus 留在新 subagent 上，下一次 spawn 接到上一个 subagent
为 parent。session 树退化成链，agent 无法整体协调。

修复方向：`spawn_subagent` 在 spawn 后**立即返回 root focus**；或 task
description 第一句加 "Spawn always sets parent=root for fan-out
parallelism; if you want chain, use session_start manually with explicit
parent."

#### **Gap C**：C3 协议文本 "additionally" 歧义
v92-01 花 30 分钟纠结。

修复方向（一行 prose）：把 "additionally need kind=runtime ok=true" 改为
"**replaces kind=test for HTML deliverables — only kind=runtime ok=true
is needed**"。

#### **Gap K**：`confirm_object max_retries` 参数无效
v9.0.6 terraria-05 InitGame `max_retries=3` 跑了 5 次；v92-05 SaveLoad
`max_retries=2` 跑了 7 次。**两次复现**。

修复方向：实现 `max_retries` 实际生效；或文档明确"该参数当前是 advisory，
chain 内部 retry-cap 在 typecalcchain.DefaultMaxRetries"。

#### **Gap L**：runtime_smoke 默认只对 1 个 object 调
v92-02 7 次全 LoadGameData；v92-05 3 次全 InitGame。agent 没意识到每个 object
都要单独 smoke 才能拿到 runtimeSmoke evidence。

修复方向：confirm_object 在 HTML 情形**自动**调一次 runtime_smoke 记到当前
object_id；或 system.md 加 "for each HTML object, after build call
runtime_smoke object_id=<id>"。

#### **Gap H**：runtime_smoke canvas 全黑判定过严
v92-03 PlayerInput（纯输入模块、不渲染 canvas）的 runtime_smoke 自然返回
ok=false（canvas 全黑），被 gate 卡。

修复方向：runtime_smoke 加 `expectCanvas` 参数，或 ok 判定分级（load_ok /
no_errors_ok / canvas_ok），让 agent 决定。

#### **Gap I**：fragment-as-impl 建模错无阻断
v92-04 把 `impl=K/frags/CombatSystem.js`（不是 index.html），dual-source
check 没阻止。

修复方向：graph_merge_object set impl 时若路径在 K/frags/ 下，警告或阻断。

#### **Gap M**：deferred-start 模式没在协议
v92-05 + v9.0.6 terraria-02 都自己采用，但协议没说。其他实例可能产出
load-order 不确定的 deliverable。

修复方向：在 system.md "Single-file deliverable model" 段加 deferred-start
模式描述 + 示例。

### v9.2 实际表现总评

**正面**：架构改革彻底成功——v9.0.6 那批"撑过 12 次 retry 转 waiver 蒙过去
shipped 黑屏"的反模式**完全消失**。Deliverable 结构信号显著优于 v9.0.6
（5/5 实例 0 ESM 污染 vs v9.0.6 4/5 有 ESM 污染）。

**负面**：v9.2 删除 waiver 退路后，**新的失败模式**出现：
- 4/5 实例**没法 finish**（root 仍 active；agent 卡在 accepted-evidence-required
  或 typecalc_test 死磕）
- session_delete 误用频率反而上升（v9.0.6 1 次，v92 2 次）—— 因为没 waiver
  退路了，agent 把 session_delete 当成"重试方式"

**根本判断**：v9.2 把"shipped 黑屏"问题彻底关上了门，但**finish 流程**有 3-4
个细节问题让 agent 卡死。**优先修复 Gap A + B + L**，预计能让 v92-01/05 类型
的项目 finish 率从 0/5 提升到 3-4/5。

## 修复方向（v9.3 提议）

按性价比排序：

1. **Gap B**：system.md 明示 "HTML don't call typecalc_test"（一行 prose 改动）—— 影响 3/5 实例
2. **Gap A**：review 改读 implFragment 而非 impl（小代码改动）—— 影响 1/5 实例（v92-01 这种最有希望的）
3. **Gap C**：协议 C3 "additionally" → "replaces"（一行 prose）
4. **Gap L**：confirm_object 在 HTML 自动调 runtime_smoke（小代码改动）
5. **Gap D**：session_delete docs 大写警告 + 在 root 强制 confirm 参数
6. **Gap E**：spawn_subagent 强制 reset focus to root（小代码改动）
7. **Gap K**：max_retries 参数生效
8. **Gap H**：runtime_smoke canvas 判定分级
9. **Gap I**：fragment-as-impl 警告
10. **Gap M**：deferred-start 模式写入 system.md
