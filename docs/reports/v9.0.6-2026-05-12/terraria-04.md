# terraria-04 — 取证报告

- **状态**：`session=finished` / `checkpoint=PASS (6/6)` / 12 个对象 confirmed
- **实际表现**：浏览器打开**黑屏**（user 验证）
- **根因**：deliverable `index.html` 行 568 在非 module `<script>` 中含
  `export default DamageCalc;` → SyntaxError
- **特殊点**：5 个实例里唯一**没用 session_build**的（monitor build=0）。
  index.html 完全靠 `write_file` 一次性手写
- **日志**：`tests/.batch-logs/terraria-04.log`（1137 行）
- **transcript**：`tests/terraria-04/.kcpos/transcripts/20260512-112641.json`（297 messages）
- **运行时长**：11:28:41 - 13:15:41（≈1h47min）

## 文件结构现状

- 571 行 / 34KB（5 个实例里最小）
- **1 个 `<script>` 标签**（行 15-569，非 module）
- **无 kcpos:session_build 标记** —— 因为根本没调过 session_build
- 16 个 fragment（CheckCollision, DamageCalc, EnemyAI, GameCore, GameLoop,
  GamePhysics, GameRender, GenerateTerrain, InitCamera, InitEntities,
  InitInput, InitPlayer, InitUI, InitWorld, MatchRecipes, PlayerPhysics）
  —— **几个 fragment 跟 index.html 根本无关**（GameCore, GameLoop,
  GamePhysics, GameRender），写完没用上
- `K/frags/DamageCalc.js`（mtime 11:36）是干净的标准函数定义，**0 个 export**

## 执行流程（按时间戳）

### 阶段 1 — Path A 单 session，自己写 16 个 frag
- 11:28:41 mkdir K
- 11:30:31 - 11:40:09 写 16 个 frag + 12 个 def
- 11:32:33 `session_start s_terraria parent=""`，**零 spawn_subagent**
- **没有 session_build 调用**（监控数据 build=[2/2/3/0/2] 第 4 位是 0）

### 阶段 2 — 一次性 write_file 把 index.html 整个写出来

- transcript msg 117 `write_file path=index.html content_len=32077 字符 /
  34165 字节`
- 内容是完整自包含 HTML：DOCTYPE → style → canvas → `<script>` IIFE
  封装的整个游戏逻辑 → `</script></body></html>`
- 内容尾部是 `init(Date.now()%100000); loop(); })(); </script>` ——
  **不含任何 `export` 或 IMPL 注册**

→ 至此 deliverable 应该是干净可玩的（没有 ESM 污染）。

### 阶段 3 — `confirm_object DamageCalc` 触发的 7 次 typecalc_test 重试

- 12:00:33 `confirm_object DamageCalc`（**唯一一次 confirm_object 调用**，
  monitor cc=1 确认）
- **典型 7 次 TestError**：12:04:57 / 12:10:31 / 12:17:27 / 12:23:24 /
  12:32:44 / 12:42:29 / 12:51:57（≈47 分钟）

每次失败的 obstacle 解释都是相同的（msg 134）：
> "the imported default is `undefined`, and any call to `calculateDamage`
> returns `undefined` for `damage`, causing every test case to fail. Fixing
> this failure would require extracting the damage calculation into a
> standalone module independent of the game environment"

注：obstacle 把测试期望写成 `.calculateDamage(...)`，但实际 typecalc.json
里的 cases 是 `IMPL.DamageCalc(baseDamage, critChance, targetDefense)` ——
描述自相矛盾，进一步引导 agent 朝错误方向修复。

### 阶段 4 — agent 尝试改 impl 路径，被 dual-source hook 拒绝

- 12:59:48 agent 把 DamageCalc.impl 从 `index.html` 改成 `K/frags/DamageCalc.js`
  企图绕过 HTML 测试问题
- v9.0 的 dual-source 检查在 [graph.go] 拒绝：
  > "error: refusing to set impl='K/frags/DamageCalc.js' for object 'DamageCalc':
  > project root contains i..."
- agent 放弃，转 13:00:27 + 13:00:51 `typecalc_waive × 12` 全 structural

### 阶段 5 — 神秘的 ESM 污染（mtime 12:47，**无 visible tool call 责任人**）

deliverable 现状（行 565-569）：
```
init(Date.now()%100000);
loop();
})();
var DamageCalc={calculateDamage:function(...)}; ← 第二个 DamageCalc 定义（与 frag 不同！）
export default DamageCalc;                       ← ESM export 在非 module script
</script>
```

**取证盲区**：
- index.html mtime = 12:47:xx，介于第 6 次（12:42:29）和第 7 次（12:51:57）
  TestError 之间
- transcript 里 msg 117 之后**没有任何** write_file / edit / bash 命令触碰
  index.html（已逐一检查 msg 117 到 297 全部 297 条消息）
- 注入的内容 `var DamageCalc={calculateDamage:function(...)}` **不在
  fragment 里**（frag 是 `function DamageCalc(...)` 签名），是凭空冒出的
  对象字面量
- 同样的取证盲区在 [terraria-01.md] 也出现 —— 01 的 `globalThis.IMPL.CombatSystem`
  + `export {}` 在 transcript 0 命中
- 两个独立实例都有"看不见的 export 注入" + 都恰好在 typecalc_test 重试期间
  发生 → **强烈怀疑 typecalc/harness 链路里有路径在 retry 期间向 impl
  写入 defensive boilerplate**，但本次没追溯到 kcpos 源码哪一段会做这事

→ 如果是这条假设，**v9.0 修复的优先级最高**。需要在 typecalc 全链路打开
write tracing，定位 ghost write 来源。

### 阶段 6 — 后续的清扫与 finish

- 13:00 ~ 13:15 一系列 typecalc_describe / typecalc_review × 11
- 所有 review FAIL（"static check: N issues"），agent 补上 `typecalc_obstacle × 12`
  让 obstacle+waiver 配对满足 gate
- 13:15:41 `session_status finished` 通过

最终：12 对象 confirmed，6/6 checkpoint PASS，finalVerdict=PASS，
deliverable 黑屏。

## 与 01/02/03 的对照

| 维度 | 01 | 02 | 03 | 04 |
|---|---|---|---|---|
| spawn_subagent | 0 | 0 | 1 | 0 |
| session_build | 2 | 2 | 3 | **0** |
| session 状态 | finished | finished | active | finished |
| 实际可玩 | 黑屏（user 验） | playable（user 验） | 未验证 | 黑屏（user 验） |
| ESM 污染 | export×2 + IMPL | 无 | 无 | export×1 |
| 神秘 ghost 写入 | 是（lines 3713-3761）| 否 | 否 | 是（lines 567-568）|
| confirm_object 调用 | 3 | 1 | 3 | 1 |
| TestError 总数 | 14 | 1 | 8 | 7 |
| 集中重试对象 | CombatSystem × 12 | InitWorld × 1 | DataTables × 7 | DamageCalc × 7 |

→ **强相关**：ghost 写入只出现在某个对象 TestError ≥7 次的实例（01 CombatSystem
12 次、04 DamageCalc 7 次）。02 一次就转 waiver、03 虽然 DataTables 7 次但
deliverable 没污染。**v9.0 的 typecalc retry 路径有 side-effect 写入隐患**。

## 04 的发现汇总

1. **agent 完全没用 session_build** — 把 fragment 模型当摆设，自己一次性 write_file
   写完整个 deliverable。kcpos 不强制要求走 session_build 路径
2. **typecalc 链路在 retry 期间产生 ghost write** —— 4/5 实例里有 2 个被打到
   黑屏，相关性强，需要源码追溯
3. **obstacle 文本误导 agent** —— 把测试期望描述成 `.calculateDamage`，与
   实际 cases `IMPL.DamageCalc(...)` 不一致，agent 顺着错误方向去"造一个
   default export"。obstacle 应该由 typecalc 引擎直接 quote case 的 call
   表达式，不能让 LLM-generated 描述自由发挥
4. **dual-source hook 起到了作用** —— 阻止了 impl 切换到 K/frags/<id>.js
   的旁路（msg 136），但 agent 接着选 structural waiver 也能过
5. **fragment 与 deliverable 解耦**：16 个 frag 里有 4 个（GameCore, GameLoop,
   GamePhysics, GameRender）根本没被 deliverable 用到，但都过了 confirm。
   说明 frag 模型对单文件 HTML 项目语义不清 —— frag 是"图对象的实现"还是
   "deliverable 的组件"？混淆导致 4 个对象被验证了却没用上
