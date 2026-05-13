# Terraria batch v9.3 — 2026-05-13

Run: 5 并行 instance，DeepSeek deepseek-v4-pro，约 11:58 启动，因发现 P0 设计漏洞 13:50 全部 killed。
归档：[tests/archive/v93-batch1/](../../../tests/archive/v93-batch1/)

## 5 实例最终状态

| 实例 | objects | confirmed | implFragment | smoke 通过 | review 通过 | 终止方式 |
|---|---|---|---|---|---|---|
| v93-01 | 8 (all implementing) | 0 | 8/8 | — | — | 死于 stream timeout × 2，第三次 resume 后救回但还没到 confirm |
| **v93-02** | 7 (1 confirmed, 6 imp) | **1** | 7/7 | — | — | user kill；已开始批量 confirm |
| v93-03 | 16 (1 confirmed, 2 imp, 13 declared) | 1 | 16/16 | — | — | user kill；过度分解 |
| **v93-04** | 10 (all imp) | 0 | **0/10 ✗** | — | — | user kill；**monolithic 模式**，1176 行 inline index.html |
| **v93-05** | 8 (all imp) | 0 | 8/8 | **6/6 ✓** | **0/6 ✗** | user kill；**P0 漏洞实证** |

## 与 v9.2 / v9.0.6 对比

| 维度 | v9.0.6 | v9.2 | **v9.3** |
|---|---|---|---|
| typecalc_waive / obstacle 误用 | 5/5 | 0（工具已删） | 0 |
| 假阳 ship 黑屏 deliverable | 4/5 | 0/5 | n/a（提前 kill） |
| 链式 spawn 退化为 chain | 4/5 | 4/5 | **0/5**（v9.3 Phase 1.2 修复有效） |
| session_delete 误用 | 1 | 2 | **0/5**（v9.3 Phase 1.1 改名 + AP14 提示） |
| HTML 真用 runtime_smoke | 0 | 27 次 | 多次（v93-04 sequential，v93-05 6 次 parallel） |
| HTML 走 confirm_object 链 | n/a | n/a | **0 个能过**（P0 漏洞） |

## 关键发现：v9.3 自身的 P0 设计漏洞

v93-05 第一时间暴露：HTML 分支 review 阶段**必然命中 `[runtime-trace-missing]`**。原因——

- v9.3 Phase 2.3 confirm_object 链对 HTML 分支 **skip 了 synthesize_tests + test**
- 但 review 阶段的 `RuntimeCheck` 仍要求 `.kcpos/typecalc-runtime/<id>.json` 存在
- HTML 分支既不跑 test 也不写 trace → trace 文件永远不存在 → review 永远 fail

实证数据 (v93-05 InitWorld bundle)：
```
compile.OK = True
runtimeSmoke.OK = True       ← v9.3 HTML 分支按设计通过
accepted.OK = False          ← 但 review 阻塞
  staticIssues: [port-observation-required, value-space-empty x2, evidence-stale, spec-stale, frags-content-matches-def]
  runtimeIssues: [runtime-trace-missing]   ← 这条是 v9.3 自己引入的
```

也就是说**整个 v9.3 HTML 分支对所有 HTML 项目都是死胡同**——smoke 通过，review 必 fail。

## v9.3.1 修复（已 ship）

| 优先级 | 修复 | 文件 | 单测 |
|---|---|---|---|
| **P0** | review 在 HTML 分支跳过 runtime-trace-missing / port-observation-required / value-space-empty / runtime-trace-stale；orphan-key 仍触发 | [internal/typecalc/static_check.go](../../internal/typecalc/static_check.go), [runtime_check.go](../../internal/typecalc/runtime_check.go) | [html_branch_replay_test.go](../../internal/typecalc/html_branch_replay_test.go) — 用 v93-05 InitWorld 状态回放 |
| **P1a** | session_build 增加 `mode` 参数，**默认 `reference`** 模式（emit `<script src>` 引用而非 inline 拼接）；chain HTML 分支在 smoke 前自动调用 build | [internal/tools/session/build.go](../../internal/tools/session/build.go), [internal/router/typecalcchain/chain.go](../../internal/router/typecalcchain/chain.go) | session/build_test.go 新增 4 个 case + chain_test.go HTML 分支补 Build 断言 |
| **P1b** | graph_merge_object 设 `impl=*.html/.htm` 时强制配 `implFragment`（同 patch 设或对象已有），否则 reject | [internal/tools/graph/graph.go](../../internal/tools/graph/graph.go) | [html_impl_fragment_test.go](../../internal/tools/graph/html_impl_fragment_test.go) |
| **P2** | system.md 增加 "session ≡ subagent 两个 facet" 说明；protocol.go 增加 AP16 (monolithic-html-no-fragments) 和 AP17 (html-without-incremental-build) | [internal/agent/system.md](../../internal/agent/system.md), [internal/protocol/protocol.go](../../internal/protocol/protocol.go) | system prompt hash 更新 |

## 设计层面学到的两件事

### 1. inline session_build 制造 chicken-and-egg

老协议把 session_build 放在 R2 root finish 步，理由是 inline mode 重写整个 deliverable 太重不能频繁跑。结果链路里没有任何时刻能把 fragment 装进 deliverable 给 smoke 看——所有 fragment 都是孤立验证。v93-02 的 agent 在日志里几乎一字不差描述了这个困境。

v9.3.1 reference mode 让 build **足够便宜**（写一行 `<script src>`），就可以**频繁地、增量地、自动地**跑。chain 就在 smoke 前 build，每次 fragment 改了下次 smoke 自动看到。

### 2. session 和 subagent 是同一件事的两个 facet

v9.3 修复 chain-spawn bug 时改的是 spawn_subagent.go，但 bug 本质是 session linking 的决策。这次 P0 漏洞修的是 review/static_check，但本质是"链路 skip 了 test，结果检查器还在等 test 产物"——又是同一个工具被两个角度描述。

v9.3.1 没做硬性合并（不动 spawn_subagent 这个名），只在文档里点明两者关系，让未来 retro 不再绕弯子。

## 这一批的方法论亮点

**没有跑完一轮就发现关键 bug**。v93-05 跑到第 6 个 confirm_object 时（约 35 分钟），我们意识到 0/6 confirmed 不是偶然——直接 kill 全部，写一个 Go 单测把 v93-05 InitWorld 的实际 bundle 当 fixture 回放，**10 分钟内**确认 P0 修复有效。

这个"用已死 batch 的归档状态当回归 fixture"模式比"每改一行重跑 5 实例"快几个数量级，建议未来 retro 标准化。

## 下一步

v9.3.1 全套已 land，所有单测 green，二进制 build OK。下一轮 batch (v9.3.1) 预期：

- 5/5 instance HTML 分支至少能跑通 review（P0 修复有效）
- 0/5 instance monolithic（P1b 拒绝）
- session_build 反复跑（P1a chain 自动 hook）
- 没有 chain-spawn 退化（v9.3 已修）

如果 v9.3.1 batch 仍发现新 gap，按同样套路：snapshot + replay + iterate。
