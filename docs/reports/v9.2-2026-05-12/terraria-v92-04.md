# terraria-v92-04 — 取证报告

- **状态**：所有 8 个 session 全 `active`，**被 user kill**
- **deliverable**：**不存在**（agent 100min 内未调过 session_build）
- **graph state**：8 个对象，**1 个 confirmed**（CombatSystem），但 impl 路径**错误**
- **日志**：`tests/.batch-logs/terraria-v92-04.log` 256KB / 323 tool 调用
- **运行时长**：19:00:40 - kill（≈100min）
- **gate 真触发**：3 次

## v92-04 最特殊的现象：**100min 0 build / 0 smoke**

| 关键 tool | 次数 |
|---|---|
| `spawn_subagent` | **7**（最多）|
| `session_build` | **0** |
| `runtime_smoke` | **0** |
| `typecalc_test` | 10 TestError + 1 Tested<Pass> |
| `confirm_object` | 1 (CombatSystem) |

→ agent 把 100 分钟全花在让 CombatSystem 单 fragment 跑过 typecalc_test，
**完全没尝试** v9.2 设计的核心路径（build → runtime_smoke）。

## 工具使用分布

| 工具 | 次数 | 备注 |
|---|---|---|
| `read_file` | 58 | **过多** —— 大量回查 def / frag 内容 |
| `markdown_section` | 44 | SPEC 切片 |
| `edit` | 29 | 反复改 CombatSystem.js 试图满足 synth-test |
| `glob` | 24 | 大量 glob 搜文件 |
| `graph_link_consume` | 21 | edges |
| `write_file` | 19 | def + 部分 frag |
| `bash` | 14 | inspection commands |
| `graph_show` | 13 | 反复看 graph state |

## 时间轴

### 阶段 1（19:00-19:13，13min）：graph 搭建 + 链式 spawn 起点
- `session_start s_root parent=""` 19:00:40
- 8 个 object 创建
- 19:13:27 spawn s_impl_gamedata（depth=1 from root）

### 阶段 2（19:13-20:53，1h40min）：**7 次链式 spawn**

| 时间 | session_id | parent（session JSON 显示）|
|---|---|---|
| 19:13:27 | s_impl_gamedata | s_root |
| 19:26:27 | s_impl_worldgen | **s_impl_gamedata** |
| 19:37:26 | s_impl_gameengine | **s_impl_worldgen** |
| 19:52:09 | s_impl_entitysystem | **s_impl_gameengine** |
| 20:02:52 | s_impl_combatsystem | **s_impl_entitysystem** |
| 20:49:31 | s_impl_craftingsystem | **s_impl_combatsystem** |
| 20:52:38 | s_impl_rendergame | **s_impl_craftingsystem** |

→ 同 v92-03 / v9.0.6 terraria-05 **同款 chain spawn bug**。spawn 之间 focus
没回到 root，新 session parent 接给上一个 subagent。

### 阶段 3（20:21+）：CombatSystem 陷阱
agent 在 CombatSystem subagent 上下文不断重试 typecalc_test，遭遇 4 个错误模式：

| 错误 | 原因 | agent 尝试的修复 |
|---|---|---|
| `CANNOT_SYNTHESIZE` | 合约太隐式（mutation in place / 无返回）| 加返回值 |
| `window is not defined` | Node 没浏览器 globals | 加 `typeof window !== "undefined"` guard |
| `SyntaxError: Unexpected token 'export'` | ESM 在非 module script | 改 module.exports |
| `IMPL.CombatSystem is not a function` | 绑定名不对 | 加 `globalThis.CombatSystem = CombatSystem` |

→ 这是 v9.0.6 batch 里的 **"ghost-write 制造机"** —— agent 为了让 typecalc_test
找到函数，往 fragment 里塞 `globalThis.X = X` + `module.exports` + ESM
defensive boilerplate。每次都改一点。

但 v92-04 **没**把这些注入到 deliverable 因为**根本没 build**。

### 阶段 4（20:53）：CombatSystem confirmed，但 impl 路径错

```json
"CombatSystem": {
  "status": "confirmed",
  "impl": "K/frags/CombatSystem.js",  ← 应该是 "index.html"
  "implFragment": null                ← 应该是 "K/frags/CombatSystem.js"
}
```

→ agent **没用 v9.0.3 fragment 模式**（`impl=shared deliverable` +
`implFragment=per-object code`），而是直接把 impl 设成 fragment 文件。
这违反 v9.0.6 dual-source enforcement 的本意，但当前 kcpos
**没禁止**（只在两个 object 设同一 impl 时阻断，单 fragment-as-impl
被允许）。

由此 gate `[typecalc-test-required]` 正确触发——因为 impl 不是 .html，
HTML 豁免不适用，要求 kind=test ok=true 通过。**gate 行为正确，是 agent
建模错**。

### 阶段 5（20:49-20:53）：最后两个 spawn

agent 在 confirm CombatSystem 后又 spawn 两个新对象（CraftingSystem、RenderGame），
试图复制同样的"per-object typecalc_test 满足"路径。第 7 个 spawn
（RenderGame）在 20:52:38，agent 正在写 RenderGame 函数（log 末尾），被 user kill。

## v92-04 gate 真触发分析

| 规则 | 次数 | 触发对象 | 触发原因 |
|---|---|---|---|
| `[root-deliver]` | 2 | CombatSystem | status=declared 时（？）这不太说得通——CombatSystem 现在是 confirmed。可能某次 graph state 不一致 |
| `[typecalc-test-required]` | 1 | CombatSystem | impl=K/frags/CombatSystem.js（非 HTML），所以触发 |

→ `[typecalc-test-required]` 的触发**正确**——HTML 豁免只对 impl=.html 生效。
agent 把 impl 设错了，自然挨这条规则。这反过来说明 v9.2 mid-flight 修复的
HTML 豁免**针对的是 impl=.html 情形**，**不是**所有"项目里有 HTML"情形。

## v92-04 暴露的设计 gap

### Gap B 复现（v92-03 + v92-04 共同）：agent 主动跑 typecalc_test on HTML
- v9.2 协议说 HTML 走 runtime_smoke，但**没说"don't call typecalc_test"**
- agent 默认调，遇到 4 种错误，每次都改 fragment 试图满足 synth 测试
- 没 build → 没 runtime_smoke → 永远过不了 v9.2 gate

修复方向（**最高优**）：system.md 加 "for HTML deliverables, **do NOT call
typecalc_test on individual fragments** — go straight to session_build +
runtime_smoke per object_id"。或者更激进：HTML 检测后**禁止** typecalc_test。

### Gap I（新发现）：fragment-as-impl 无阻断
v92-04 把 `impl=K/frags/CombatSystem.js`（**fragment file 本身**）。当前
graph 不阻止这个错误建模。dual-source enforcement 只检查"两个 object 共享同一
impl"，**不检查** "impl 指向 fragment file 而不是 deliverable"。

修复方向：graph_merge_object 在 set `impl` 时检查：如果 impl 路径在 K/frags/
目录下且 implFragment 未设，**应该警告或阻断**——这是 v9.0.3 模式的常见
误用。

### Gap E 复现（v92-03 + v92-04 + v9.0.6 terraria-05）：链式 spawn
**未修。三次咬人。**

### Gap J（新发现）：链式 spawn 导致 0 build
v92-04 7 spawn 没有 session_build —— 因为 spawn 后 focus 一直跟着新 subagent，
root agent 几乎没拿回控制权。链式 spawn 让 root 无法在中间做 session_build /
runtime_smoke / 整体协调。

修复方向：与 Gap E 同源，spawn_subagent 应该 spawn 完**立即返回 root**，让
root 决定是否继续 spawn 还是先 build。

## v92-04 几乎没有 **v9.2 设计成功** 的部分

v92-04 是 5 个实例里**最不符合 v9.2 设计意图**的：
- ✗ 没用 runtime_smoke
- ✗ 没用 session_build  
- ✗ 没产出 deliverable
- ✗ 100min 困在 CombatSystem 单对象的 typecalc_test 上
- ✗ 7 个 spawn 全部 chain 而非 fan

**唯一**没踩的坑：没 `session_delete`（不像 v92-01/02 触发回滚灾难）。

## 结论

v92-04 是 **agent 完全没读懂 v9.2 设计** 的典型反例。所有时间都花在让
synthesized test pass，而 v9.2 的核心承诺（HTML 走 runtime_smoke）没被
agent 采纳。

如果不停下来强制改进 system.md（明确说"HTML **don't** call typecalc_test"
+ 阻断 fragment-as-impl 建模），DeepSeek-v4-pro 级别的模型很容易陷入这个
误区。这是 v9.2 教学层最大的失败案例。
