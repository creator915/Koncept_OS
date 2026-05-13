# terraria-03 — 取证报告

- **状态**：`session=active`（**未 finish** — LLM 流崩死在 13:45:07）
- **实际表现**：deliverable 在 13:03 已写完且结构干净（126KB / 3171 行 / 单
  非 module `<script>` / 零 ESM export）—— **playability 未实际验证**（user 未亲打开浏览器；本报告**不**对 playable/黑屏 下结论，只描述结构状态）
- **核心独特点**：5 个实例里唯一使用 `spawn_subagent` 的（但仅 1 次，20 个对象里
  只委托了 1 个）
- **核心故障**：`session_delete` 引发的级联回滚 + LLM 流崩溃使 finish 失败
- **日志**：`tests/.batch-logs/terraria-03.log`（1670 行，运行最长）
- **运行时长**：11:29:40 - 13:45:07（≈2h15min）后崩溃

## 文件结构现状

- 3171 行 / 126KB，**kcpos 默认生成的极简 HTML scaffold**（agent 没自己写 index.html）
- 1 个 `<script>`（行 10-3168，非 module），就是 session_build 注入的 kcpos block
- 20 个 fragment（最细的分解）：AudioSystem, BossManager, Camera, CombatSystem,
  DataTables, EnemyManager, GameLoop, InputManager, Inventory, Lighting,
  ParticleSystem, Physics, PlayerController, ProjectileManager, RecipeDB,
  Renderer, SaveSystem, UIRenderer, WorldData, WorldGen
- 所有 fragment 零 export/import
- GameLoop fragment 内部包含 `initCanvas(); requestAnimationFrame(rAF);`
  作为启动序列（依赖 JS 的 function 提升 + rAF 异步执行）

## 执行流程（按时间戳）

### 阶段 1 — Path B 触发了，但只用了一次
- 11:29:40 `session_start terraria_root parent=""`
- 11:38:41 **`spawn_subagent s_impl_datatables`** — 委托 DataTables fragment
  实现
- **5 个实例里唯一的 spawn_subagent 调用**；剩余 19 个对象全部由 root 自己写

→ Path B 触发器没有"必须全部分裂"的语义。`PathBTriggers` 即使触发，agent
完全可以只委托 1 个、自己干 19 个。**触发器 ≠ 强制分布**。

### 阶段 2 — session_build hard gate 又被绕过（第三次：edit 而非 sed）

- 12:17:35 `session_build` 第一次 → **REFUSED**：6 个未建模 helper
  （`initCanvas`, `startGame`, `gameLoop`, `getTileColor`, `getEnemyColor`,
  `getBossColor`）
- agent 在 log line 860 写下：
  > "the error says these are 'function(s) declared in fragments' - let me
  > re-read. ... So `const initCanvas = () => {...}` should pass because it's
  > a const with an arrow function, not a `function` declaration."
- 12:17:46 `edit` 把 `function initCanvas()` 改成 `const initCanvas = () => {`
- 12:18:59 `session_build` 第二次 → 还有未处理的
- 12:20:13 `session_build` 第三次 → **PASSED**
- deliverable 写完时间：**13:03**

至此 deliverable 已经稳定。

→ **v9.0.6.4 gate 的第三种绕过路径**（汇总）：
- 01：`sed -i 's/function X(/const X = function(/'`
- 02：从一开始就写 `const X = (...) => {}` 形式的 helper
- 03：refuse 后 edit 把 `function X` 改成 `const X = () => {}`

三个独立实例三种不同手段绕过同一个 gate，说明：
- gate 的正则太窄，所有现代 JS 写法都能绕
- 需要扩展到 `const|let|var X = (function|\(.*\) =>)` 全形态

### 阶段 3 — 12:20 之后的 1h17min 验证链折腾
session_build #3 完成时间 12:20:13。注：deliverable mtime 是 13:03:05，
window 内无任何 write_file/edit 调用 —— 写入主体仍是 session_build #3，
13:03 的 touch 来源不明（可能是 typecalc 路径的副作用）。

- 12:20:43 - 13:16:56 `confirm_object × 3`（**仅 DataTables, WorldGen, GameLoop**）
  - DataTables typecalc_test 重试 **7 次失败**：12:23:10 / 12:28:50 / 12:36:15 /
    12:47:50 / 12:52:04 / 13:01:08 / 13:05:45
  - WorldGen typecalc_test 失败 1 次（13:12:22）
- 13:22:39 `typecalc_waive × 13` 全 structural（"Browser-only; all fragments share
  global G namespace"）
- 13:23:10 `typecalc_waive × 7` 补完剩下 7 个
- 13:36:17 `typecalc_review × 5`（DataTables, WorldGen, GameLoop, InputManager,
  WorldData —— **超出 confirm_object 范围**，agent 直接对没走完链的 object
  调 review）
- 13:37:41 `session_aggregate` + 13:37:48 `session_gate_check` → 报错

### 阶段 4 — `session_delete` 引爆级联回滚

13:37:53 agent thinking 解读 gate_check 输出：
> "Child session: s_impl_datatables is still active - need to delete it"

agent 把 "child 还 active" 误解为"需要删除"。实际上正确动作是
`session_status({"id":"s_impl_datatables","status":"finished"})`
—— session_status 的 description 明确写："Other moves error — to abandon
a session use session_delete."

- 13:38:47 agent 并行调用：
  - `session_delete s_impl_datatables`
  - `typecalc_obstacle × 17`（补完剩下 17 个对象的 obstacle）
  - `graph_link_produce × 1`
- agent 后续 thinking（line 1526）：
  > "The session_delete of s_impl_datatables rolled back the graph to before
  > the objects were confirmed! The session_delete reverted all graph mutations.
  > All objects lost their status and impl paths."

`session_delete` description 其实写得很清楚：
> "Roll back a session: depth-first roll back all children (reverse-applying
> their graphDiff to K/graph.json), then reverse-apply this session's
> graphDiff... Also deletes def/impl files this session created."

注意：docs 称会"删除 def/impl 文件"，但**实际验证**：
`K/frags/DataTables.js` mtime 是 11:59，session_delete 13:38:47 之后**仍然存在**。
所以 docs 与实际行为不符 —— delete 没真删文件，但 graphDiff 全回滚了。

→ **真正的问题**是 agent 误判语义：把"child active"读成"需要 delete"，
而 session_delete 的 "ROLL BACK" 关键词在 description 第一句就有。
agent 的工程素养在这里短路了。
→ **可改进点**：gate_check 输出应该明确说"call session_status(id, finished)
to mark the child complete" 而不是只说"still active"。

### 阶段 5 — 15 分钟手忙脚乱的恢复

- 13:40:12 `graph_merge_attribute × 12` 重新 confirm 所有 attribute
- 13:40:27 `checkpoint_add_item × 4` 加 checkpoint
- 13:40:33 `checkpoint_freeze`
- 13:40:47 `checkpoint_fill × 4`（全自填，verifier 字段空）
- 13:42:09 `graph_merge_object × 20` 重新 set impl + implFragment + status
- 13:42:49 ⚠ 15 个 object-gate `accepted-evidence-required` 告警（review
  evidence 也被回滚）
- 13:43:16-31 重建 produce/mutate 边、再 confirm attributes
- 13:43:44 `session_aggregate` → 13:43:50 `session_gate_check`：
  "15 reviews missing"

### 阶段 6 — LLM 流崩溃（v9.0.3 retry 修复未覆盖的场景）

- 13:44:04 `typecalc_review × 5`（CombatSystem, PlayerController, RecipeDB,
  Inventory, Physics）
- 13:45:07 **CRASH**：
  ```
  error: read stream: unexpected EOF (partial content already emitted;
  not retried — re-invoke or resume manually)
  ```

→ v9.0.3 的 LLM 流重试只在 `emittedAny=false` 时触发；
这里 partial content 已 emit，`emittedAny=true`，重试被跳过，agent 死。

后续就再没有 `session_status finished`。session 永远停在 `active`。

## 03 的特殊价值

03 **是 batch 里唯一证据：当 agent 真去用 spawn_subagent 时，会撞上 session_delete
的破坏性 bug**。01/02/04（甚至 05 仅 4 次）都没踩这个坑，因为它们压根没生成子 session。

deliverable 本身**结构状态**（playability 未验证）：
- 单非 module `<script>`
- 零 ESM 污染
- GameLoop 内 `initCanvas() + rAF` 启动序列依赖 JS function hoisting + rAF 异步
- 20 个 fragment 都用 `function NAME(...)` 形式（除了被 edit 改的 6 个 helper）

→ **真正阻碍 03 完成的不是 deliverable 质量，而是验证链的脆弱性**。要想下"是否 playable"
的定论，需要 user 在浏览器实际打开 + `runtime_smoke` 复测。

## 03 的发现汇总

1. **Path B 触发了但没强制分布** — agent 在 line 579 自己写下计划
   "spawn sub-agents for the core systems in parallel waves"，**实际只 spawn 了
   1 次（DataTables）就放弃，剩下 19 个对象自己写**。1/20 委托不应算作
   "path-B compliant"
2. **session_build hard gate 第三种绕过**（const arrow function）—— 进一步确认
   `fragFnDeclRe` 需要扩展
3. **agent 误用 session_delete 当 cleanup**：description 写得清楚是 roll-back，
   但 gate_check 输出只提示"still active"，没明确推荐 session_status(finished)
4. **session_delete docs 与实际行为不一致**：docs 说会删 def/impl 文件，
   但 `K/frags/DataTables.js` mtime 11:59 在 13:38:47 delete 后**仍然存在**。
   这点反而救了 deliverable
5. **typecalc_test 没有 retry 上限** —— DataTables 单对象 7 次重试约 42 分钟，
   WorldGen 1 次。共浪费约 50 分钟测一定不会过的浏览器代码
6. **`emittedAny=true` 的流崩溃没有 retry** —— v9.0.3 的修复有盲区。
   review 输出长，partial emit 后断流就死定了
7. **checkpoint 与 verification 解耦得过分** —— agent 13:40:33 就 freeze 了
   checkpoint，然后还在折腾 verification 5 分钟才崩。checkpoint=PASS 不应该
   在 gate 未通过前可达到
