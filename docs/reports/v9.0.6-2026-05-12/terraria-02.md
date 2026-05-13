# terraria-02 — 取证报告

- **状态**：`session=finished` / `checkpoint=PASS (10/10)` / `graph_validate=PASS`
- **实际表现**：**playable**（用户验证）
- **关键**：02 能跑不是 kcpos 验证的功劳，是 agent 自己 debug 出 load-order
  问题后手工实现 `window._startTerraria` deferred-start 模式
- **日志**：`tests/.batch-logs/terraria-02.log`（1051 行）
- **transcript**：`tests/terraria-02/.kcpos/transcripts/20260512-112639.json`
- **运行时长**：11:28:07 - 12:13:18（≈45min，相比 01 的 2h52min）

## 文件结构现状

- 977 行 / 49KB（vs 01 的 3766 行 / 149KB）
- 6 个 `<script>` 标签（实际 3 个）
  - 行 24-206：`<script>`（**非 module**，agent 写的 game orchestration）
  - 行 208-973：`<script>`（**非 module**，session_build kcpos block）
  - 行 975：`<script>window._startTerraria();</script>` — 第三个 script，
    bootstrap 调用
- 6 个 fragment（CraftItem, InitWorld, RenderFrame, SpawnEnemies, UpdateGame, UpdateInput）
- **零 ESM export/import**

## 与 01 的共同失守

### Path B trigger 同样失守
- 11:28:07 `session_start s_terraria_root parent=""`
- 整个 transcript **零 `spawn_subagent`**
- 6 objects + 大 SPEC 仍满足 Path B 触发条件，单 session 走完

### 验证链同样退化为 structural waiver
- 11:56:05 `confirm_object InitWorld` → 12:01:24 `typecalc_test: TestError`
- agent 一次失败就放弃（**vs 01 撑了 12 次**）
- 12:04:20 `typecalc_waive × 6` 全 structural
- 12:05:23-24 `typecalc_obstacle × 6`
- 12:12:33 `checkpoint_freeze` → 10/10 PASS, 0 waived, finalVerdict=PASS
- 12:13:18 `session_status finished`

### checkpoint 自填无核
checkpoint 10 个 must 项全 PASS，0 waived，verifier 字段全空。

## 与 01 的关键差异

### 没走 sed 旁路
- 11:44:23 `session_build` 第一次 **REFUSED**（15 个未建模 helper：
  hash/noise/tileSolid/...）
- agent 在 log line 438 写下：
  > "The fix is to NOT use session_build for this project. Instead, I'll embed
  > everything directly into index.html. The graph objects and fragments serve
  > as the specification/verification layer, but the actual deliverable is a
  > self-contained index.html that includes all the code."
- agent 没用 sed，而是把 helpers 改写成 `const tileSolid = (...) => {...}` 形式
- 11:51:30 `session_build` 第二次 **PASSED**

→ **v9.0.6.4 的 `fragFnDeclRe` 同样不识别箭头函数和 `const X = (...) =>` 模式**。
两条绕过路径：
1. `const X = function(...)`（01 sed）
2. `const X = (...) => {}`（02 手写）

graph object 的 `implSymbol` 设的是 camelCase（`craftItem`, `initWorld`...）——
`buildAllowedFunctionNames` 把这些纳入白名单，所以 frag 里的 `function craftItem(...)`
主入口通过；helpers 走箭头函数完全无视 gate。

### 没走 bash 直写旁路
02 整轮停留在 kcpos 工具层。`grep "open('index.html', 'w')"` 在 02 的 transcript
里**零命中**。

### typecalc 一次失败就转 waiver（反而救了 02）
- 01 撑了 12 次 TestError 重试，过程中（很可能是 12:10:13 那次 truncated python3）
  为了让 typecalc_test 工作而插入了 `globalThis.IMPL = ...; export {...}`
  defensive workaround，最终污染了 deliverable
- 02 一次失败就转 structural waiver，agent 没去碰 typecalc 那一套，
  deliverable 也就没有 ESM 污染

**反直觉结论：撑得越久越容易作茧自缚**。01 死磕 typecalc_test 90 分钟反倒
把好端端的 deliverable 写坏了。

## 02 能跑的真正原因（agent 自己的功劳）

log line 487-585 有 75 行 agent reasoning 反复读 session_build 的输出，发现：
- 原始 template 把 `gameLoop()` 直接放在 `</script>` 前
- session_build 在这 `</script>` 之后插入 fragment 块
- 顺序：bootstrap script 调 `gameLoop()` → 还没执行到 fragment 块 →
  `tileSolid` `RECIPES` `ITEMS` 等都 undefined

agent 主动应对（11:52:57 / 11:54:04 / 11:54:31 三次 edit）：
1. 把初始化 + `gameLoop()` 包成 `window._startTerraria = function(){...}`
2. 在 `<!-- kcpos:session_build:end -->` 之后追加
   `<script>window._startTerraria();</script>`

这是 **agent 自己的工程素养**，不是 kcpos 的产物。
**v9.0 协议里没有任何 rule 教 agent 走 deferred-start 模式。**

## 02 的结论

- kcpos 验证链对 02 仍然什么都没验
- 02 能跑纯粹是 agent 自己 debug 出来的 load-order 修复
- `session_build` hard gate 倒是触发了一次有意义的 refuse，但 agent 用箭头函数绕过
- v9.0 还有救：fragment 模型 + session_build refuse 在 02 案例里**第一次发挥了
  有意义的作用**（让 agent 重新审视 helper 边界）。**只是 enforcement 太弱**——
  需要扩展 `fragFnDeclRe` 到 const/let/var = function/arrow 全形态。
