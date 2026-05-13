# terraria-v92-03 — 取证报告

- **状态**：所有 5 个 session 全 `active`，**被 user kill**
- **deliverable**：9885B / 356 行 / 3 `<script>` + kcpos:session_build block / **0 ESM**
- **graph state**：14 个对象，**2 个 confirmed**（ParticleSystem、PlayerInput），12 个 declared 且 impl=null
- **日志**：`tests/.batch-logs/terraria-v92-03.log` 212KB / 289 tool 调用
- **运行时长**：19:00:10 - kill（≈100min）
- **gate 真触发**：3 次

## 工具使用分布

| 工具 | 次数 | 备注 |
|---|---|---|
| `bash` | 36 | **过多** —— 大量 bash 干本可用工具做的事 |
| `write_file` | 33 | def + frag 写入 |
| `markdown_section` | 26 | SPEC 切片 |
| `read_file` | 19 | 验证 |
| `graph_link_consume` | 19 | edges |
| `glob` | 18 | 多次搜索 |
| `graph_link_mutate` | 16 | 大量 mutate edges |
| `confirm_object` | 1 | 仅 ParticleSystem |
| `typecalc_test` | ~（直接调）| 0 Tested<Pass> / 9 TestError |
| `session_build` | 8 | 多次迭代 |
| `runtime_smoke` | ~ | 仅 PlayerInput 1 次（FAIL: canvas all black）|
| `spawn_subagent` | 4 | GameData / WorldGen / PlayerInput / ParticleSystem |

## v92-03 最特殊的现象：4 个 spawn 形成**链式 session 树**

session 文件显示的 parent 关系：
```
s_root
  └ s_impl_gamedata        (spawn 1, 19:11:05)
     └ s_impl_worldgen     (spawn 2, 19:22:11) — parent: s_impl_gamedata!
        └ s_impl_playerinput     (spawn 3, 19:33:14) — parent: s_impl_worldgen
           └ s_impl_particlesystem (spawn 4, 19:54:28) — parent: s_impl_playerinput
```

每次 `spawn_subagent` 的 monitor 输出**都是** `depth=1 · focus=...`——意味着
spawn 调用是从 root 上下文发起的，但新 session 的 `parent` 字段却链给了上一个
subagent，不是 root。

→ **v9.0.6 batch terraria-05 同款 bug** 第二次出现：`spawn_subagent` 的 auto-create
逻辑使用**当前 focus 的 session** 作为新 session 的 parent，而不是 root（或调用方）。
v9.0.6 PENDING 列表第 9 项"`spawn_subagent` 强制 parent=root"未修。

后果：所有 5 个 session 都 `active`，agent 无从知道哪些"已经完成可以收尾"，
也没法用 `session_aggregate` 干净汇总。session_status finished 都无人调过。

## 时间轴

### 阶段 1（19:00-19:11，11min）：graph 搭建 + 第一次 spawn
- `session_start root parent=""` 19:00:10
- 14 个 object 创建
- 19:11:05 spawn s_impl_gamedata —— **从 root 上下文**发起

### 阶段 2（19:11-19:55，44min）：4 次 spawn，但**链式**

每次 spawn 时 focus 还在上一个 subagent 上 → 新 session 接到上一个为 parent：
- 19:11:05 spawn gamedata（depth=1 from root）
- 19:22:11 spawn worldgen（**depth=1 monitor 报，但 session.parent=s_impl_gamedata**）
- 19:33:14 spawn playerinput
- 19:54:28 spawn particlesystem

→ 链式 spawn 让 session 树退化，agent 难以追踪状态。

### 阶段 3（20:00 onwards）：tag chasing in typecalc_test

agent 没在 root 用 confirm_object，反而在 subagent 上下文跑了一次：
```
[20:28:54]   » confirm_object({"object_id": "ParticleSystem", "max_retries": 3})
```

之后大量 typecalc_test 调用尝试 ——**9 次 TestError，0 次 Tested<Pass>**。

agent 卡在**命名转换不一致**：
- 属性名是 `entity_lists`（snake_case）
- agent 写的 frag 参数名是 `entityLists`（camelCase）
- typecalc_synthesize_tests 生成的 setup 用 `entity_lists`，生成的 call 用 `entityLists`
- 测试运行时 `entityLists is not defined` 报错

agent thinking 在 20:50+ ：
> "The test harness generates code that references `entityLists` but the
> variable was set up as `entity_lists`. This is a test harness naming
> mismatch — the harness converts the attribute name `entity_lists` to
> camelCase JS variable `entityLists` for the call expression, but the
> setup code uses the snake_case name."
>
> "**This is not something I can fix in the fragment.**"

→ **agent 卡在 typecalc_test harness 命名 bug 上 30+ 分钟**。但 v9.2 协议明明
说 HTML 不需要 typecalc_test，agent 仍坚持调它。**Gap B 复现**：v92-03 + v92-04
都在同一陷阱。

## v92-03 gate 真触发

| 规则 | 次数 | 对象 |
|---|---|---|
| `[runtime-smoke-required]` | 2 | PlayerInput、ParticleSystem —— HTML 对象要求 runtime evidence |
| `[root-deliver]` | 1 | PlayerInput impl "index.html" missing —— 某一刻 index.html 不存在 |

→ runtime_smoke 真要求**正确触发**（这是 v9.1 新加 gate）。但 PlayerInput 调过
1 次 runtime_smoke 且 FAIL（canvas all black —— input 模块本身不渲染）。
**`[runtime-smoke-required]` 对纯逻辑/纯 IO 对象的合理性存疑**——这些不渲染
canvas 的对象 smoke 自然 ok=false，反而被卡。

→ 新 gap：`runtime_smoke` 对非渲染对象判断 ok=false 太严。canvas 判定不应该
是**唯一**的 ok 信号——load_fired + 无 pageError 也算 ok 才对。当前判定逻辑：
```go
ok := raw.LoadFired && len(raw.PageErrors) == 0
if canvas.Found && !canvas.OK { ok = false }
```
canvas found 但全黑就 ok=false——这是真问题。**Gap H**。

## v92-03 暴露的设计 gap

### Gap E 复现：spawn_subagent 链式 spawn
v9.0.6 terraria-05 已知，v92-03 重演。**未修**。

### Gap B 复现：HTML 对象 agent 主动跑 typecalc_test
v92-04 已发现，v92-03 同样。agent 不知道"HTML skip typecalc_test"等于
"**don't even call it**"，主动调了 9 次都失败。

### Gap H（新）：runtime_smoke canvas 全黑判定过严
PlayerInput 是输入模块，**本身就不渲染 canvas**——agent 单独 smoke 它必然失败。
应该允许"load_fired + 无 pageError"作为 ok=true 的充分条件，canvas 全黑只在
**确认有 canvas 应该渲染时**才扣分。

修复方向：`runtime_smoke` 加 `expectCanvas` 参数（默认 false，HTML 整体
deliverable 时 true）。或者 ok 判定分级：load_ok / no_errors_ok / canvas_ok 三档，
agent 据此决定。

## v9.2 在 v92-03 上**仍然有效**的部分

1. **`session_delete` 没误用** —— v92-03 是 5 个里唯一**没**踩这坑的实例
2. **runtime_smoke 真触发了 gate**（PlayerInput 缺 smoke evidence → `[runtime-smoke-required]`）
3. **0 ESM 污染**
4. **runtime_install 0 次** —— playwright 自动发现

## 结论

v92-03 是 **"Path B 链式 + typecalc_test 命名陷阱"** 双重故障的案例：
- 4 spawn 形成链而非扇形 → session 收尾混乱
- typecalc_test 不该跑但跑了，且 synth 工具命名转换有 bug
- runtime_smoke 对非渲染对象判定过严，反向劝退使用

deliverable 9.8KB 极小（v92-01 是 73KB），只有 2 个 fragment 集成进去——agent
还远未进入"全部对象 confirm"阶段。如果不被 kill，按当前速度可能再要 1-2 小时
才能产出完整 deliverable，且会继续被 typecalc_test 套住。
