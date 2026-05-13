# Terraria batch v9.0.6 — 2026-05-12

Run: 2026-05-12（5 实例并行），DeepSeek deepseek-v4-pro

| 实例 | 状态 | confirm_object 调用 | TestError 总数 | 实际表现 | 报告 |
|---|---|---|---|---|---|
| terraria-01 | finished | 3 | 14 | **黑屏**（user 验证；ESM export in non-module script） | [terraria-01.md](terraria-01.md) |
| terraria-02 | finished | 1 | 1 | **playable**（user 验证） | [terraria-02.md](terraria-02.md) |
| terraria-03 | active   | 3 | 8 | **未验证**（deliverable 13:03 已写完，结构干净；user 未实测） | [terraria-03.md](terraria-03.md) |
| terraria-04 | finished | 1 | 7 | **黑屏**（user 验证） | [terraria-04.md](terraria-04.md) |
| terraria-05 | finished | 10 | 20 | **未验证但应黑屏**（含 ESM export `export { InitGame };`） | [terraria-05.md](terraria-05.md) |

Per-object TestError 分布（每实例最高重试集中在 1 个对象上）：
- 01: InitWorld 1, GenerateWorld 1, **CombatSystem 12**
- 02: InitWorld 1
- 03: **DataTables 7**, WorldGen 1
- 04: **DamageCalc 7**
- 05: HandleInput 3, **InitGame 14**（跨 4 次 confirm_object）, RenderGame 2, UpdateGame 1

**底线**：user 实测过的 3 个里 1 个 playable、2 个黑屏；剩 2 个未实测但结构信号
推测黑屏。02 能跑也不是 kcpos 验证的功劳—— 是 agent 自己读 session_build 输出后
debug 出 load-order 问题，手工实现 `window._startTerraria` deferred-start 模式。

## 跨实例发现

### Path B 触发器是死字
`PathBTriggers{Objects:3, ImplLOC:400, SpecChapters:2}` 写在 protocol.go，
但 `session_gate_check` 没接。3/5 实例（01/02/04）零 `spawn_subagent`。

### v9.0.6.4 anti-theater hard gate 三条独立绕过

`fragFnDeclRe = function\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(` 只识别
`function NAME(`，三个实例用三种不同手段绕过：

- 01：`sed -i 's/function X(/const X = function(/'`
- 02：从一开始就写 `const X = (...) => {}` 形式的 helper
- 03：refuse 后 `edit` 把 `function X` 改成 `const X = () => {}`

→ 修复方向：扩展 `scanFragmentFunctionNames` 到 `const|let|var X = (function|\(.*\) =>)` 一族。

### typecalc_test 持续失败 → structural waiver 全员洗白
- WaiverKindStructural 不计入 flood gate
- 一句 "Canvas/DOM/side-effect/no-runner" 就过
- 01 撑了 12 次重试才转 waiver（这 90 分钟里污染了 deliverable）
- 02 一次失败就转，反而 deliverable 干净

**反直觉结论：撑得越久越容易作茧自缚。**

### checkpoint 自填无核
agent 自己 `checkpoint_fill` 全 must 项 + `checkpoint_freeze` → PASS。
verifier 字段全空，没机制让 agent 真打开浏览器/调用接口。

### batch log 命令截断 ~200 字符
破坏性 `bash` 调用（如 01 的 `python3 ... open('w')`）在 log 取证时是盲区——
具体写入内容看不到，只能靠 mtime + 现场文件状态反推。

## 修复方向 — DONE 与 PENDING

### ✅ 已在 v9.1 / v9.2 落地

1. **`runtime_smoke` 浏览器烟测**（v9.1）—— **取代了**旧首位优先级"追溯 ghost-write
   来源"。不管 ghost-write 怎么发生、ESM export 怎么混进 deliverable，
   `runtime_smoke` 在 finish 末尾用 headless chromium 真打开 file://...，
   `pageError` 立刻暴露 `Unexpected token 'export'`。**症状层防线**比追溯写入源
   （病因层）更直接、性价比更高。
   - 配套：`runtime_install` / `runtime_link` 工具 + preflight 多根发现
   - 实战验证（terraria-04 黑屏 fixture）：6 turns / 60 秒抓到并修好

2. **`typecalc_waive` / `typecalc_obstacle` 完全删除**（v9.2）—— 不再可绕。
   pre-v9.2"撑得越久越容易作茧自缚"（01 撑 12 次 retry 反污染 deliverable）从架构
   层消失：失败就是失败，没有 structural waiver 洗白通道。配套：
   - `WaiverKindStructural`/`Pragmatic` 常量删除
   - gate `[obstacle-needs-waiver]` / `[waiver-flood]` 规则删除
   - `[insufficient-not-confirmable]` 改为硬阻断 + actionable 错误信息

3. **HTML 增加 `[runtime-smoke-required]` gate 规则**（v9.1）—— HTML deliverable
   必须有 `kind=runtime ok=true` 才能 confirm。

4. **`kcpos doctor` CLI**（v9.1）—— 显式工具环境诊断，配 `--install`/`--yes`。

### ⏳ PENDING（按本 batch 证据排序）

**静态检查 / hard gate**：

5. 新增静态检查：fragment 不得包含 top-level `export`/`import` —— 即使 ghost
   write 仍在，这条把 deliverable 出门前拦下；`runtime_smoke` 是 finish 兜底，
   这条是 build 阶段早期防线
6. `fragFnDeclRe` 扩展到 `const|let|var X = (function|\(.*\) =>)` 全形态
   （01/02/03 三案验证 "anti-theater hard gate" 被绕过同一个洞）
7. 追溯 ghost-write 源（**优先级下降**）—— v9.1 smoke 能拦下症状，但仍值得查
   kcpos 内部是哪条代码路径在 typecalc retry 期间动 K/frags/*.js。05 是最干净
   evidence chain（agent 主动删 export 后又被无声写回）

**Path B 与 session 管理**：

8. `session_gate_check` 真正读 `PathBTriggers` 并阻断（**触发 ≠ 强制分布**：
   03 触发了但只委托 1/20 也算"合规"）
9. `spawn_subagent` 强制 parent=root（或新 root），避免 05 那种链式 spawn
10. `session_delete` 改为 additive cleanup，或在 description 大写警告会回滚
    graphDiff（03 因此进入 15 分钟手忙脚乱）
11. `checkpoint_freeze` 前禁止任何 child session 仍在 active 状态（05 在 subagent
    全 finish 前 1 分钟就 freeze 了 checkpoint）

**验证链稳定性**：

12. LLM 流重试覆盖 `emittedAny=true` 场景（03 死于此；目前 v9.0.3 修复有盲区）
13. `confirm_object max_retries` 参数实际生效（05 设 3 但跑了 5 次）
14. `typecalc_test` 全局 retry-cap：单对象 ≥3 次失败自动 abort（pre-v9.2
    01 单 CombatSystem 跑 12 次浪费 1.5 小时；v9.2 删 waiver 后这条更紧迫——
    不能让 agent 死磕的同时还无路可退）

**verification 信任度**：

15. `checkpoint_fill` 必须有非空 verifier 或外部 evidence 文件（4/5 实例
    全自填 PASS，verifier 字段空）

**取证可用性**：

16. batch log 命令记录全文（或独立文件 + 截断标记）—— 01 的破坏性 python3
    被 ~200 字符截断后无法重建现场
