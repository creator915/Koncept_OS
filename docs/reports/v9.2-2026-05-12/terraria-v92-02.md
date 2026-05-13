# terraria-v92-02 — 取证报告

- **状态**：`session=active`，**被 user kill**（明天可继续，但 K/ 已被 session_delete 重置）
- **deliverable 结构**：68610B / 676 行 / 多 `<script>` 标签（含 kcpos:session_build block）/ **0 ESM export-import**
- **graph state**：9 个 object 全部 `status=declared`（**重大回滚后的状态**）—— 实际 1 个 LoadGameData 走过 confirm_object，但 session_delete s_root 后被全部冲掉
- **日志**：`tests/.batch-logs/terraria-v92-02.log` 202KB / 326 tool 调用
- **运行时长**：18:59:28 - kill（≈100min）
- **gate 真触发**：1 次（typecalc-evidence-passing on LoadGameData）

## 工具使用分布

| 工具 | 次数 | 备注 |
|---|---|---|
| `write_file` | 49 | 多次重写（rollback 后重建）|
| `graph_link_consume` | 43 | edges |
| `bash` | 41 | 异常高 —— 用 bash 干其他工具能干的活 |
| `graph_merge_object` | 39 | 重复写 status/impl |
| `markdown_section` | 29 | SPEC 切片 |
| `session_build` | 8 | 多次迭代 |
| `runtime_smoke` | 7 | 但**仅针对 LoadGameData 一个对象**（其他 8 个对象从未 smoke 过）|
| `confirm_object` | 2 | 都是 LoadGameData |
| `spawn_subagent` | 2 | 都是为 LoadGameData |
| `session_delete` | **1** | **删 s_root，毁灭性触发点** |

## 时间轴

### 阶段 1（18:59-19:07，8min）：graph 搭建
- `session_start s_root parent=""` 18:59:28
- 9 个 object + 10 个 attribute 创建
- 14 个 def 文件写入（K/defs/*.js）
- 这阶段顺利

### 阶段 2（19:07-19:23，16min）：**Path B 失败 + 重 spawn**

- 19:07:41 `spawn_subagent s_impl_loadgamedata` —— 委托 LoadGameData 实现
- 子 agent 跑了 16 分钟没产出**可用** fragment
- 19:23:10 `spawn_subagent s_impl_gamedata` 重新委托（同对象，新 session id，加 `max_iterations: 30`）

→ **Path B 早期信号**：第一次 spawn 子 agent 处理大数据初始化（LoadGameData
要包含 60 tile + 35 enemy + ... 的所有静态表）。LLM 写大数据表的能力**有上限**，
agent 自己也意识到，给第二次 spawn 加了重试上限。但仍只对 1/9 委托。

### 阶段 3（19:23-20:00，37min）：root 自己写其他 8 个 fragment

agent 在 root 上下文直接写了剩下 8 个 fragment：GameLoop / GenerateWorld /
InitPlayer / RenderFrame / SaveLoad / UpdateEntities / UpdatePlayer / UpdateUI。

→ 后续就再没有 spawn_subagent ——9 个 object 只 spawn 1 个（同一个对象算 2 次）。

### 阶段 4（20:00-20:04，4min）：runtime 路径开始
- 20:00:39 第一次 `session_build`
- 20:01:36 `runtime_link path=~/.npm/_npx/.../node_modules` —— preflight 提示
  生效，agent 把已存在的 playwright 链入 kcpos cache
- 20:02:00 / 20:04:06 `runtime_smoke LoadGameData` ×2（first failed, second...）
- 20:04:42 `confirm_object LoadGameData`

### 阶段 5（20:04-20:25，21min）：尝试 confirm 全员 + 撞墙

**LoadGameData 是 v92-02 唯一 confirm_object 成功的对象**。其他 8 个对象 agent
都没跑 confirm_object——但**也没跑 runtime_smoke**。

agent thinking 在 20:24 附近：
> "Actually, let me try a completely different approach: delete the LoadGameData
> object from the graph and recreate it with the proper fragment."
>
> "But that's destructive. Let me instead try to **roll back the session**."

### 阶段 6（20:25 - 20:47，22min）：💥 **session_delete s_root 灾难**

```
[20:25:05] » session_delete({"id": "s_root"})
[rollback s_root] deleted 20 source file(s):
  K/defs/player_state.js K/defs/ui_state.js K/defs/game_config.js
  K/defs/game_state.js K/defs/light_map.js K/defs/particle_pool.js
  K/defs/world_data.js K/defs/camera_pos.js K/defs/entity_list.js
  K/defs/input_state.js K/defs/GenerateWorld.js index.html ← 含 deliverable 本身！
  K/defs/InitPlayer.js K/defs/LoadGameData.js K/defs/SaveLoad.js
  K/defs/UpdateEntities.js K/defs/UpdatePlayer.js K/defs/GameLoop.js
  K/defs/RenderFrame.js K/defs/UpdateUI.js
```

**全部 K/defs/\*.js + 已建好的 index.html 全被删**。session_delete docs 明明
写着 "Also deletes def/impl files this session created"，但 agent 把 "roll
back" 当成"撤销最近 op"。

agent thinking 立刻意识到：
> "OK, the session was rolled back. Now let me check what remains. **The
> graph should be empty now.** But I still have the monolithic index.html.
> Let me start fresh with a better approach..."

但 index.html **也**被删了——agent 没意识到。20:25:19 立刻 `session_start s_root`
（同 id 重建），再用 22 分钟重写所有 def + 9 个 fragment + 6 次 `session_build`
迭代 + 5 次 runtime_smoke 重试 LoadGameData 单对象。

20:47:14 第二次 `confirm_object LoadGameData` —— 然后被 user kill。

→ **最终 K/graph.json 状态**：9 个 object 全 declared，0 个 confirmed，
deliverable 是**第二次重建**的版本（676 行 / 68KB / 干净结构）。

## v92-02 暴露的设计 gap

### Gap D 复现（v9.0.6 已知，v92-01 + v92-02 两连击）：`session_delete` 是深度毁灭

| 实例 | session_delete 触发位置 | 破坏范围 |
|---|---|---|
| v9.0.6 terraria-03 | s_impl_datatables (child) | graphDiff 回滚 status/edges/portObservation/valueSpace |
| v92-01 | s_impl_generateworld (child) | 同上 + 4 个 def 文件 |
| **v92-02** | **s_root**（ROOT session！）| **20 个文件被删除，包括 index.html 本身** |

session_delete s_root 是最坏情况。**强烈建议**:
1. `session_delete` 在 ROOT 上强制 confirmation 或额外参数（如 `--force-root`）
2. 改名 `session_rollback_destructive`，新增非毁灭性 `session_cleanup`（仅删 session JSON）
3. 在 description 第一句加 "**WARNING: DESTRUCTIVE — rolls back ALL graphDiff and deletes ALL files this session created. For root sessions, this empties the entire project.**"

### Gap F（新发现）：Path B 单点失败导致全员退回 Path A

- v92-02 第一次 spawn_subagent 失败（16 min 无产出）
- 第二次 spawn 同对象、加重试上限
- 此后 **agent 不再 spawn**，9 个 object 中 8 个 root 自己写

agent 没考虑"spawn 失败时换个对象重 spawn"或"找几个独立对象并发 spawn"。
单点失败就放弃整套 Path B。

修复方向：协议加例 / system.md 加 "spawn 失败时的回退策略"——例如 fan-out
其他对象、或限定 child 工具集（不让 child 自己再 spawn 复杂任务）。

### Gap G（新发现）：runtime_smoke 只对一个对象跑

v92-02 7 次 runtime_smoke 全部是 `object_id=LoadGameData`。其他 8 个对象
完全没 smoke。这违背设计意图——**每个对象都应该有 runtimeSmoke evidence**
才能 confirm（HTML 的 [runtime-smoke-required] 规则）。

agent 没意识到 runtime_smoke 是按 object 记 bundle 的，可能误以为
"smoke 一次就够全 deliverable"。

修复方向：system.md 加 "对每个 HTML object 都要单独 runtime_smoke" 强调。

## v9.2 哪些设计在 v92-02 上**仍然有效**

1. **无 waiver 退路**——agent 没法 waive 9 个未 confirm 的对象
2. **runtime_smoke 实战可用**——LoadGameData 7 次重试最终 ok=true
3. **runtime_link 自动发现**——preflight 提示 → agent 调 runtime_link 一次绑定
4. **0 个 200MB 下载**——chromium 全部走已有 cache
5. **deliverable 结构干净**——0 ESM 污染（无论是初版还是 rebuilt 版）

## 结论

v92-02 是**Path B 选择性失败 + session_delete 误用**的双重案例。**架构没问题**
（deliverable 干净），但**agent 决策失误**两次：

1. spawn_subagent 失败一次就放弃 Path B（应该换对象再 spawn）
2. **session_delete s_root** 把整个项目 graph + def + deliverable 全删

第二次决策是致命的，让前 90 分钟工作几乎全废。**v9.2 的"无 waiver"反而把这次
失误暴露得更彻底**——pre-v9.2 agent 可能就 structural waiver 蒙过去 ship 不完整
deliverable；v9.2 让 agent 没退路，被迫面对 session_delete 自己造成的烂摊子，
但也来不及修完整。
