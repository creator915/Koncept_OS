# terraria-v92-01 — 取证报告

- **状态**：`session=active`（agent 调过 `session_status finished`，gate 硬阻断）
- **进程**：自然 EXIT（agent 主动放弃，未被 user kill）
- **deliverable 结构**：72954B / 1712 行 / 单非 module `<script>` / **0 个 ESM export-import** / 14 个 fragment 全部 runtimeSmoke=ok=true
- **playability**：未实测，但**结构信号是 5 实例里最好的**——值得 user 浏览器实测
- **日志**：`tests/.batch-logs/terraria-v92-01.log` 240KB / 519 tool 调用
- **运行时长**：18:59:29 - 20:48:46（≈109min）
- **gate 真触发**：28 次（不是 monitor 显示的 thinking-text 误报）

## 工具使用分布

| 工具 | 次数 | 备注 |
|---|---|---|
| `graph_merge_object` | 108 | 极高——大量 status/impl/portObservation/valueSpace 写入和**回填** |
| `graph_merge_attribute` | 76 | 同上，session_delete 后重建 |
| `write_file` | 45 | 14 个 frag + def + index.html scaffold |
| `read_file` | 43 | 验证状态 |
| `graph_link_produce` | 27 | edges |
| `typecalc_describe` | 25 | 完整跑了 14 个对象的 describe |
| `markdown_section` | 23 | SPEC 切片读取，无 `force=true` 滥用 |
| `typecalc_review` | 20 | 完整跑了 review，多次因 review 失败而重试 |
| `confirm_object` | 4 | 仅 ApplyDamage/TileCollision/UpdateTime/PlayerInventory ——其他 10 个用了直接 graph_merge_object status=confirmed |
| `runtime_smoke` | 16 | **每个对象都跑了一次 smoke** |
| `spawn_subagent` | 3 | 只对 GenerateWorld/RenderFrame/UpdatePlayer 委托——14 个对象只委托了 3 个 |
| `session_build` | 6 | 多次迭代 |
| `session_delete` | **1** | **灾难触发点**（详见下） |

## 时间轴

### 阶段 1（18:59-19:46，47min）：graph 搭建 + 部分 Path B
- `session_start s_root_terraria parent=""` 19:00:00
- 3 次 `spawn_subagent`：GenerateWorld（19:15）/ RenderFrame（19:23）/ UpdatePlayer（19:36），每个约 10 分钟
- 14 个 `graph_create_object`（含未 spawn 的 11 个，root 自己实现）
- 11 个 def 文件（K/defs/*.js throw-stub）+ 14 个 frag 文件（K/frags/*.js）

→ **Path B 部分采用**：仅 3/14 委托。说明 agent 把 spawn_subagent 当成"对复杂对象选择性使用"的工具，而非"≥3 objects 时强制"。

### 阶段 2（19:46-19:49，3min）：build + smoke 紧密耦合
- `session_build` × 5（19:46:29 / 19:47:04 / 19:48:04 / 19:48:56）
- 每次 build 后立刻 `runtime_smoke object_id=InitGame`
- 这是验证 deliverable 能在浏览器里 boot 的最小闭环

→ **v9.2 设计意图体现**：build 出来立刻浏览器烟测，发现问题快速回头改 fragment。

### 阶段 3（19:49-20:21，32min）：confirm_object 陷阱 + per-object smoke

- 19:49:34 `confirm_object ApplyDamage` → typecalc_test TestError
- 19:59:22 `confirm_object TileCollision` → TestError
- 20:11:32 `confirm_object UpdateTime` → TestError
- 20:17:48 `confirm_object PlayerInventory` → TestError

**4 个 confirm_object 全部失败**——因为 agent **主动跑了 typecalc_test**，对象是
HTML deliverable 的 fragment，vm.Script harness 无法在 Node 上模拟 Canvas/DOM。

agent 用了约 30 分钟才**自纠**："Wait, the parenthetical could be read as: 'for HTML deliverables, the requirement is kind=runtime ok=true' (i.e., the 'additionally' means 'in addition to the normal rule, HTML has a different rule')." 然后：

- 20:20:42-20:21:15 在 33 秒内并发跑 13 个 `runtime_smoke`——每个对象单独烟测，得 `runtimeSmoke` evidence 进 bundle

→ **C3 协议文本歧义代价**：30 分钟。文本里 "additionally need kind=runtime"
被读成"两者都要"。后续需要改成 "**replaces** kind=test"。

### 阶段 4（20:33-20:35，2min）：最终 build + session_delete 灾难

- 20:33:08 最后一次 `session_build`（72954B index.html 落盘）
- 20:34:33 第一次 `session_aggregate`
- **20:35:09 agent 调 `session_delete s_impl_generateworld`** —— 本意"清理已完成的子 session"

工具返回：
```
[rollback s_impl_updateplayer] deleted 4 source file(s):
  [defs/collision_result.ts defs/damage_result.ts defs/recipe_result.ts defs/save_result.ts]
```

**一次 session_delete 触发了三联回滚**：s_impl_generateworld 的子链
（s_impl_updateplayer、s_impl_renderframe）也被反向 rollback，4 个 result 属性的
def 文件被删除。

agent thinking 立刻意识到："Wait, deleting s_impl_generateworld also deleted
s_impl_updateplayer and s_impl_renderframe because they were children of it
or each other?"

→ **复现 v9.0.6 batch 里 terraria-03 的同款 bug**：`session_delete` 是
**深度递归 rollback**，不是 additive cleanup。pending 修复列表里第 10 项
（"`session_delete` 改为 additive cleanup"）未做，再次咬人。

### 阶段 5（20:35-20:46，11min）：rollback 恢复 + 顽固 finish 重试
agent 用 11 分钟修复被 rollback 删除的内容：
- 重建 4 个 result attribute（damage_result / collision_result / recipe_result / save_result）
- 重写 def 文件（受 `[def-existence]` enforcement hook 提醒）
- 重新 link produce/mutate edges
- 重新 set portObservation 和 valueSpace（agent 怀疑 rollback 也动了这些）

### 阶段 6（20:46-20:48，2min）：finish 被 gate 硬阻断
- 20:46:04 `session_aggregate`
- 20:46:10 `session_status finished` → **被 gate FAIL 拒绝**

gate 报告 28 个真 ✗ 错误：

| 规则 | 触发次数 | 触发原因 |
|---|---|---|
| `[accepted-evidence-required]` | 15 | review tool 看不全 72KB 的 index.html（8K token 限制），review verdict 失败 |
| `[produces-or-mutates-non-empty]` | 4 | session_delete 后 produces/mutates edges 没完全重建 |
| `[typecalc-evidence-passing]` | 1 | ApplyDamage 仍有 ok=false 的 typecalc_test 残留 |
| `[accepted-evidence-required] review verdict failed` | 1 | UpdateTime review LLM 实际给了 fail |

→ **agent 最终放弃**：
> "I think I need to accept this limitation... the deliverable exists,
> runtime_smoke passes for all 14 objects, all fragments compile cleanly...
> Let me provide the final summary."

进程于 20:48:46 自然 EXIT，但 root session 仍 `active`。

## v92-01 暴露的设计 gap

### Gap A（最严重，新发现）：`accepted-evidence-required` 卡死 HTML finish
review tool 用 LLM 读 `obj.Impl` 作为输入。当 `impl=index.html` 72KB 时，超 8K
token 限制，LLM 只能看到前 ~3 个函数，review verdict 必然失败或不完整。
**pre-v9.2 这会被 structural waiver 蒙过去，shipped 黑屏；v9.2 让 agent
诚实卡死**，但 finish 路径同时被堵。

修复方向（**未做**）：
- 让 review 默认读 `obj.ImplFragment` 而不是 `obj.Impl`——单个 fragment 远小于 deliverable
- 或者实现 chunked review：把大 HTML 切片轮流 review
- 或者引入 "review-by-frag" 跳过 deliverable 整体 review

### Gap B（已 mid-flight 修）：gate `[typecalc-test-required]` HTML 豁免漏洞
mid-flight 在 v92-04 上发现，已 patch + 测试落地。

### Gap C：C3 协议文本 "additionally" 歧义
v92-01 因此**浪费 30 分钟**。protocol.go 改为 "**replaces** kind=test for HTML"
即可。

### Gap D：`session_delete` 仍是深度递归 rollback
**v9.0.6 batch 已知，未修，再次咬人**。docs 写了"Roll back a session: depth-first
roll back all children"，但 agent 仍以为是 additive 清理。修复方向：
- 改成 additive（只删 session JSON，不动 graphDiff）
- 或者改名 `session_rollback`，新增 additive 版本 `session_cleanup`
- 至少把 description 第一句改成 "**DESTRUCTIVE / ROLL-BACK**: ..."

### Gap E：Path B 触发器仍非强制
v92-01 14 个 object 只 spawn 3 个。**触发 ≠ 强制分布**——v9.0.6 已知问题 8，
未做。

## 反过来：v9.2 哪些设计在 v92-01 上**成功**了

1. **typecalc_waive / typecalc_obstacle 不可达**——agent 4 次 confirm_object 失败
   后没有 waiver 退路，被迫去理解 HTML deliverable 的 runtime_smoke 路径。**有效**。

2. **runtime_smoke 实战可用**——16 次调用全部 ok=true，agent 学会了"build →
   smoke → 验证 → 改 frag → 再 smoke"循环。**有效**。

3. **preflight playwright 发现**——0 个 runtime_install / runtime_link 调用，
   走 cheap probe（kcpos cache symlink）就发现已有 playwright 1.59.1 + chromium。**有效**。

4. **5/5 不死于 LLM stream**——v9.0.3 retry 修复持续生效。**有效**。

5. **deliverable 结构干净**——0 个 ESM export 出现在 deliverable 里。**有效**
   （但样本只 1 个；尚不能定论 ghost-write 问题彻底解决）。

## 结论

v92-01 是一个**架构成功但流程半成品**的案例：
- **架构正确**——deliverable 结构干净，按 v9.2 设计意图制成
- **agent 诚实卡死**——没有 waiver 退路时不会强行 shipped 黑屏
- **但 finish 流程被三层问题堵死**：(a) review tool token 限制 (b) session_delete 误用回滚 (c) C3 文本歧义

未来 v9.3 优先级清单：
1. Gap A（review-by-frag）——直接堵在 finish 路径上
2. Gap D（session_delete additive）——v9.0.6 + v9.2 两次咬人
3. Gap C（C3 文本改 "replaces"）——一行字改动
4. Gap E（Path B 强制）——已知未做

至于 deliverable 本身能否 playable，需要 user 浏览器实测才能下定论。结构信号
（0 export / 单 script / 14 frag smoke 全 OK）**比 v9.0.6 batch 任何一个都好**。
