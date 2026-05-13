# terraria-v92-05 — 取证报告

- **状态**：`s_terraria_root` 仍 `active`，**被 user kill**
- **deliverable**：108946B / 3197 行 / 3 `<script>` + kcpos block + **deferred-start bootstrap** / **0 ESM**
- **graph state**：17 个对象（最多）、Path A 单 session、**1 个 confirmed**（InitGame）
- **日志**：`tests/.batch-logs/terraria-v92-05.log` 109KB / 246 tool 调用
- **运行时长**：18:58:47 - kill（≈110min）
- **gate 真触发**：1 次

## v92-05 最特殊的现象：**Path A racer + 17 高分解 + deferred-start 模式**

| 特征 | v92-05 | 其他实例 |
|---|---|---|
| `spawn_subagent` | **0** | 4 (v92-03) / 7 (v92-04) / 3 (v92-01) / 2 (v92-02) |
| object 数 | **17**（最多）| 8-14 |
| session 树 | **单 session**（无 chain bug）| 3-8 个嵌套 sessions |
| `<script>` tag 数 | 3（含 bootstrap call）| 1-3 |
| deferred-start 模式 | **是** —— `<script>InitGame({canvasId:'game'});</script>` 在 kcpos block 之后 | 仅 v92-05 用 |

→ Path A 但**架构最干净**。同 v9.0.6 batch 唯一 playable 的 terraria-02 一样的
deferred-start 模式：把 bootstrap 调用放在 kcpos block 之后单独 `<script>`，
保证 fragment 全 load 后才启动游戏。**agent 自己学会了 v9.0.6 batch 教训**。

## 工具使用分布

| 工具 | 次数 | 备注 |
|---|---|---|
| `write_file` | 45 | 17 个 def + 17 个 frag 写入 |
| `graph_merge_object` | 36 | 大量 impl/portObservation set |
| `graph_merge_attribute` | 34 | 17 个 attribute 配套 |
| `graph_link_consume` | 29 | edges |
| `graph_link_mutate` | 27 | mutates edges |
| `graph_create_object` | 17 | 17 个 objects 一次性 declare |
| `graph_create_attribute` | 8 | 部分 attributes |
| `runtime_link` | 1 | 用了 user 自己的 koncept_agents 项目 node_modules 路径 |
| `runtime_smoke` | 3 | **全部针对 InitGame** |
| `confirm_object` | 2 | DayNight、SaveLoad |
| `session_build` | 4 | 多次迭代 |
| `typecalc_test` | 8 TestError + 0 Tested<Pass> | 全部失败 |

## 时间轴

### 阶段 1（18:58-19:33，35min）：graph 大规模搭建
- 17 个 object + 8 个 attribute（部分 attribute 没 def 文件，可能漏建）
- 17 个 def 文件 + 17 个 frag 文件 写入
- **所有 17 个 object 的 impl 都正确设成 `index.html` + implFragment 指向 K/frags/<id>.js**
  —— v9.0.3 fragment 模式应用正确，比 v92-04 强

### 阶段 2（19:33-19:39，6min）：build + smoke 紧密耦合
- `session_build` ×4：19:33:17 / 19:34:54 / 19:38:06 / 19:39:33
- `runtime_link path=/Users/kangxin/Documents/workspace/koncept_agents/node_modules`
  发现已有 playwright（用了 koncept_agents 项目的 node_modules，不同于其他实例）
- 3 次 `runtime_smoke InitGame` —— 验证整体 deliverable 能 boot

### 阶段 3（19:40-19:54）：第 1 次 confirm_object → 1 次 TestError 后放弃
```
[19:40:11] » confirm_object({"object_id": "DayNight"})
[19:45:41]     ↳ typecalc_test DayNight: TestError
[19:49:03 thinking] "The test synthesis created tests that try to use `state`
as an argument, but the DayNight function accesses `G` directly (a global).
The tests are synthesized from the def's signature which says the function
takes `state`. But the actual implementation uses the global `G`."
```

→ agent **正确识别**了 typecalc_synthesize_tests 和 fragment 实现的不匹配。
但没意识到 HTML 不该跑 typecalc_test，**还是去试 SaveLoad**。

### 阶段 4（19:54-20:50，56min）：**SaveLoad 死磕 7 次**

```
[19:54:50] confirm_object({"object_id": "SaveLoad", "max_retries": 2})
[20:02:25] typecalc_test SaveLoad: TestError #1
[20:10:32] TestError #2
[20:17:05] TestError #3
[20:26:24] TestError #4 ← max_retries=2 早已超
[20:32:08] TestError #5
[20:42:47] TestError #6
[20:50:21] TestError #7
```

→ **`max_retries=2` 参数不起作用**（v9.0.6 batch 也观察到过）。agent 设了
重试上限，实际跑了 7 次。**56 分钟死磕一个对象的 typecalc_test，对 16 个
其他对象的 runtime_smoke 完全没做**。

### 阶段 5（20:50 - kill）：被 user kill

## v92-05 gate 真触发

| 规则 | 次数 | 触发对象 |
|---|---|---|
| `[accepted-evidence-required]` | 1 | InitGame — confirmed 但没 review evidence |

→ InitGame 是唯一 confirmed 的对象，但 agent 没跑 typecalc_describe + review
（review 的 LLM 调用代价高，agent 可能跳过了）。gate 因此卡。

## deliverable 实际结构（v92-01 之外最干净的）

```html
<body>
  ...
  <script>
    // 17-77: orchestration / bootstrap helper definitions
  </script>
  <!-- kcpos:session_build:begin -->
  <script>
    // 78-3194: 17 个 fragment 集成内容
  </script>
  <!-- kcpos:session_build:end -->
  <script>InitGame({canvasId:'game'});</script>  ← deferred-start
</body>
```

→ **deferred-start 模式正确**：bootstrap 调用 `InitGame(...)` 放在 kcpos
block 之后单独 `<script>`，确保 17 个 fragment 全 load + 顶层 const 求值完成后
才启动游戏。**这是 v9.0.6 batch terraria-02（唯一 playable）的关键修复手法**。
agent 学到了。

## v92-05 暴露的设计 gap

### Gap B 复现（v92-03 + v92-04 + v92-05）：HTML agent 仍跑 typecalc_test
**三次复现**。v9.2 system.md 写了 "HTML deliverables skip typecalc-test-required"，
但 agent 还是主动调 typecalc_test。SaveLoad 56 分钟死磕是直接证据。

修复方向必要性极高。

### Gap K（新发现）：`confirm_object max_retries` 参数无效
**v9.0.6 batch 已发现**（terraria-05 InitGame max_retries=3 跑了 5 次），
**v92-05 复现**（SaveLoad max_retries=2 跑了 7 次）。两次复现还没修。

### Gap L（新发现）：runtime_smoke 默认只对 InitGame 调
v92-05 3 次 runtime_smoke 全部针对 InitGame。其他 16 个 object 没 smoke 过。
agent 可能误以为 "smoke 一次代表整个 deliverable 没问题"，没意识到每个 object
都需要 runtimeSmoke evidence。

→ 与 v92-02 同款问题（v92-02 7 次 smoke 全 LoadGameData）。**两次复现**。

修复方向：要么 confirm_object 在 HTML 情形**自动**调 runtime_smoke 并记到当前
object_id；要么 system.md 强调每个 object 都要单独 smoke。

### Gap M（新）：deferred-start 模式没在协议里
v92-05 + v9.0.6 terraria-02 都用了 `<script>X();</script>` 在 kcpos block 之后
单独写一个 bootstrap。这是 fragment 模型在浏览器里能跑的**关键**。但当前协议
没提到这个模式——v92-01/03/04 都没用，导致它们的 deliverable bootstrap
顺序不确定。

修复方向：把 deferred-start 写进 system.md 的 "Single-file deliverable model"
段落。

## v9.2 在 v92-05 上**仍然成功**的部分

1. **0 ESM 污染** —— deliverable 干净
2. **正确的 fragment 模式建模** —— 17 个 object 都 `impl=index.html` + `implFragment=...`
3. **runtime_link 用户自定义路径发现** —— preflight 多根探测 work
4. **没踩 session_delete 坑** —— v92-05 没 session_delete，安全
5. **deferred-start 模式自然采用** —— 架构层面正确（即使协议没明说）
6. **没死于 LLM stream** —— v9.0.3 retry 修复持续生效

## 结论

v92-05 是 **"Path A 高分解 + 架构正确 + verification 覆盖差"** 的案例：
- **deliverable 看起来最有戏**：109KB、3197 行、deferred-start 模式、0 ESM、
  17 fragment 集成进去（可能 playable，未实测）
- **但只 1/17 confirmed**：agent 没用 v9.2 的 runtime_smoke per-object 模式
- **56 分钟死磕 SaveLoad 的 typecalc_test**：完全是浪费

如果协议改一行 "for each confirmed HTML object, call runtime_smoke with
that object_id"，v92-05 可能 1 小时内就能 17/17 confirmed。**架构成功但
verification 流程没教好**。
