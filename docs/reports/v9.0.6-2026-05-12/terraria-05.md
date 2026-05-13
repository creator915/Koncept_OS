# terraria-05 — 取证报告

- **状态**：`session=finished` / `checkpoint=PASS` / 4 个对象 confirmed
- **实际表现**：浏览器打开**应为黑屏**（user 未亲验，但 deliverable 行 661 有
  `export { InitGame };` 在非 module `<script>` 中 → 必然 SyntaxError）
- **特殊点**：5 个实例里**唯一真正用 Path B 跑完整套**的（4 个 spawn_subagent，
  最高），但 4 个 subagent 是**链式而非扇形**
- **核心证据点**：agent 12:01:14 **明确删除**了 `export { InitGame };` —— 后来
  又在无可见 tool 调用的窗口里被写回去了
- **日志**：`tests/.batch-logs/terraria-05.log`（1750 行，5 个实例里最长）
- **运行时长**：11:32:23 - 14:26:16（≈2h54min）

## 文件结构现状

- 2919 行 / 108KB
- 1 个 `<script>`（行 10-2918，**非 module**），就是 session_build 的 kcpos block
- 4 个 fragment：HandleInput, InitGame, RenderGame, UpdateGame
- **InitGame.js 末尾含 ESM exports**：
  ```
  globalThis.IMPL = globalThis.IMPL || {};
  globalThis.IMPL.InitGame = InitGame;
  export { InitGame };
  ```
- 这段被原样汇集进 index.html 行 659-661

## 执行流程（按时间戳）

### 阶段 1 — Path B 触发器**真触发**了，但是链式 spawn

agent 在 line 79、275 自己说："Path B is mandatory when ≥3 declared objects.
All three fire! So I must use Path B (spawn_subagent per object)."
—— 这次 agent 认真理解了协议。

实际 spawn 时序：
- 11:36:20 `spawn_subagent s_impl_handleinput` —— parent=s_root
- 11:50:41 `spawn_subagent s_impl_initgame` —— **parent=s_impl_handleinput**（不是 s_root！）
- 12:31:38 `spawn_subagent s_impl_updategame` —— **parent=s_impl_initgame**
- 12:42:19 `spawn_subagent s_impl_rendergame` —— **parent=s_impl_updategame**

```
s_root
  └─ s_impl_handleinput
       └─ s_impl_initgame
            └─ s_impl_updategame
                 └─ s_impl_rendergame
```

→ **链式 Path B**：agent 在每个 subagent 完成后从该 session 焦点直接 spawn 下一个，
不返回 root 再 spawn。功能上仍能跑通，但完全失去 Path B "parallel waves"
的原意。`spawn_subagent` 应该强制 `parent` 设回 root，或 description 里
警告链式后果。

### 阶段 2 — 唯一一次记录在 log 里的 export 移除（12:01:14）

s_impl_initgame 在 11:56:24 写 K/frags/InitGame.js 时，fragment 末尾就含
`export { InitGame };`。

12:01:14 subagent 自己诊断（log line 743）：
> "The test harness uses a CommonJS-style load. I need to remove the `export`
> statement so the function can be loaded via eval. The fragment is meant to
> be assembled into index.html via session_build, not used as a standalone
> ES module."

然后跑：
```
edit K/frags/InitGame.js
  old: "export { InitGame };\n"
  new: "// Fragment assembled via session_build — no ES export needed\n"
```

**这是整个 batch 里唯一一次 agent 明确把 ESM 污染从源文件里清除的动作**。

### 阶段 3 — confirm_object × 4 + 14 次 TestError + mtime 13:48 之谜

InitGame 在 batch log 里**被调了 4 次 confirm_object**：
| # | 时间 | session 焦点 | max_retries | 期间 TestError |
|---|---|---|---|---|
| 1 | 11:56:35 | s_impl_initgame | (none) | 1（12:01:01）|
| 2 | 12:01:24 | s_impl_initgame | (none) | 5（12:04, 12:11, 12:15, 12:21, 12:29）|
| 3 | 13:03:59 | s_root | 3 | 3（13:05, 13:13, 13:16）|
| 4 | 13:21:14 | s_root | 3 | 5（13:24, 13:32, 13:40, 13:46, **13:52**）|

合计 InitGame **14 次 TestError**（监控 cc=10 是总 confirm_object 调用数；
TestError 是 retry 计数另算，全 batch 总共 20 个 TestError）。

**`max_retries=3` 参数不生效**：第 4 次调用设置 `max_retries=3`，仍然跑了 5 次。

InitGame.js 当前 mtime = **13:48**，落在第 4 次 confirm_object 的重试 4
（13:46:05）和重试 5（13:52:54）之间。

但当前文件末尾确实有：
```
globalThis.IMPL = globalThis.IMPL || {};
globalThis.IMPL.InitGame = InitGame;
export { InitGame };
```

**在整个 1750 行 batch log 里搜索所有 write_file/edit 操作针对
`K/frags/InitGame.js` 的命中**：

```
730:[11:56:24]  write_file  (初始版本，末尾含 export { InitGame };)
749:[12:01:14]  edit        (移除 export，替换为注释)
```

**就这两次**。然而：
- 12:32:14（subagent_updategame）`read_file K/frags/InitGame.js` 读取
- 12:42:59（subagent_rendergame）`read_file K/frags/InitGame.js` 再读
- 13:03:23（root）`read_file K/frags/InitGame.js`，紧接 thinking
  "RenderGame and InitGame fragments have `IMPL.InitGame = ...` patterns"
- 13:21:14 onwards root 跑 confirm_object，retry 中 mtime 跳到 13:48

所以**在 12:01:14 到 13:03:23 之间，InitGame.js 已经被改回含 IMPL+export 内容**，
而**这两个时间点之间 batch log 里没有任何 write_file/edit 命中 InitGame.js**。

这与 [terraria-01.md] 和 [terraria-04.md] 的现象**完全一致**：mtime 在
typecalc 重试期间被无声更新，并且新增了 IMPL/export defensive 模式。

→ 这是 batch 里**最干净的取证链**：agent 在 12:01:14 明确 edit 移除 export，
1 小时后 read_file 读到的文件却已含 IMPL+export。**有什么在 batch log 看不见
的层面改了文件**，需要在 kcpos 源码层 trace 谁动 K/frags/。本次扫描了
`internal/typecalc/`、`internal/tools/`、`internal/typecalc/harness/`，
没找到直接 `os.WriteFile` 到 K/frags/*.js 的代码，**待源码细查**。

### 阶段 4 — HandleInput.js 在 root 阶段被改造（root 自己写出的奇形）

13:03:47 root agent thinking（log line 1176）：
> "the RenderGame and InitGame fragments have `IMPL.InitGame = ...` patterns
> at the bottom, but the HandleInput fragment has `export { HandleInput }`.
> I need to standardize on `IMPL.<Name> = <Name>`"

注意 agent 此时**已经知道** InitGame.js 含 IMPL 模式（13:03 vs mtime 13:48 矛盾，
说明 mtime 不准或文件被多次重写）。然后：
```
edit K/frags/HandleInput.js
  old: "export { HandleInput };"
  new: "var IMPL = globalThis.IMPL || {};\nIMPL.HandleInput = HandleInput;"
```

→ agent 自己在 root 阶段把 HandleInput 的 export 改成 IMPL pattern，但
InitGame.js 末尾的 export **从未被 root 重新移除**。strategies 在不同 fragment
间不统一，最终 InitGame 的 export 留在 deliverable 里。

### 阶段 5 — session_build 第 1 次 refuse 是 dual-source 不符

- 12:01:21 subagent 把 InitGame.impl 从 `index.html` 改成 `K/frags/InitGame.js`
  企图绕过 HTML 测试问题（同 04 的做法）
- 14:18:03 第 1 次 session_build 因此拒绝（log line 1561+）：
  > "fragments target multiple deliverables [K/frags/InitGame.js
  >  K/frags/RenderGame.js index.html]"
- 14:18:34 agent 把 InitGame.impl 改回 `index.html`
- 14:19:05 第 2 次 session_build **PASS**

→ dual-source 检查在这里**有实际作用**，比起 04（直接 typecalc waiver 跳过）
强一截。但 agent 转身就 waiver，effect 被绕过。

### 阶段 6 — finish 流程

- 14:19:26 `checkpoint_freeze`（before all subagents finished）
- 14:19:55 `checkpoint_fill` × 6
- 14:20:18 / 14:20:25 / 14:20:31 / 14:20:37 `session_status finished` × 4
  对应 4 个 subagent（有些被重复设了 2 次）
- 14:26:16 `session_status s_root finished`

## 与 01/02/03/04 的对照（更新 ghost-write 关联）

| 维度 | 01 | 02 | 03 | 04 | 05 |
|---|---|---|---|---|---|
| spawn_subagent | 0 | 0 | 1 | 0 | **4 链式** |
| session_build | 2 | 2 | 3 | 0 | 2 |
| session 状态 | finished | finished | active | finished | finished |
| 实际可玩 | 黑屏（user 验） | playable（user 验） | 未验证 | 黑屏（user 验） | 未验证（推测黑屏） |
| ESM 污染 | export×2 + IMPL | 无 | 无 | export×1 | export×1 + IMPL |
| confirm_object 调用 | 3 | 1 | 3 | 1 | 10 |
| TestError 总数 | 14 | 1 | 8 | 7 | 20 |
| 集中重试对象 | CombatSystem × 12 | InitWorld × 1 | DataTables × 7 | DamageCalc × 7 | **InitGame × 14** |
| **agent 明确删过 export** | 不知（未追） | 不知 | 不知 | 否 | **是 → 又被写回** |
| 神秘 ghost 写入 | 是（lines 3713-3761）| 否 | 否 | 是（lines 567-568）| **是（fragment 整段）** |

**ghost-write 关联性进一步加强**：05 是 batch 里**唯一明确证据**——
agent **主动删除** export → 转两小时后 export **重新出现**。
mtime 改变发生在 13:46-13:52 这个**完全无 visible tool call 的窗口**。

## 05 的发现汇总

1. **`spawn_subagent` 不强制 root parent**：5 个实例只有 1 个真用 Path B（05），
   而它 4 个 subagent **链式 spawn**（s_root→handleinput→initgame→updategame→rendergame）
   —— 而非 Path B "parallel waves" 原意。`spawn_subagent` 应该 reset
   session_focus 到 root，或在 description 里说明链式后果
2. **`max_retries` 参数不生效**：agent 在第 4 次 `confirm_object InitGame`
   设 `max_retries=3`，实际仍跑了 5 次 typecalc_test。参数被忽略
3. **ghost write 强化为 SMOKING GUN**：05 是 batch 里第一个**有书面记录 agent
   主动删除 ESM 污染**的实例（12:01:14 edit），而 1 小时后同一文件已被
   还原成含 IMPL+export 的形态。这彻底排除"agent 自己加的可能"
4. **session_build dual-source 检查**：05 + 04 都触发了"fragments target
   multiple deliverables"refuse，是 v9.0 协议里少数真正起作用的 gate。
   保留这个机制
5. **agent 修复策略不统一**：HandleInput.js 用 `var IMPL = globalThis.IMPL`
   pattern；InitGame.js 用 `globalThis.IMPL = globalThis.IMPL || {}; ... ;
   export { InitGame };` pattern。这种不一致暗示 agent 在不同时间
   "学到了"不同 patterns，不是从单一文档来的
6. **checkpoint_freeze 在 subagent 全部 finished 之前就执行**（14:19:26 vs
   subagent finished 14:20:18+）—— checkpoint 不应该在子 session 未结束前
   freeze
