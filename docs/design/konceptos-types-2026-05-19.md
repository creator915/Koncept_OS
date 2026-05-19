# KonceptOS 类型枚举 — Phase 2.1（从三 Handler 真实签名反推）

> 策略遵循文档 Phase 2.1："从三个 Handler 的输入输出枚举所有信息类型，
> 给每个类型命名"。**不预设**——每个类型都从 kcpos 的真实签名提取，
> 附 `文件:行` 出处，可在代码中指认。
> 日期 2026-05-19 ｜ 分支 feature_260519 ｜ 纯文档，无代码改动

---

## Handler 1.1 Hypergraph — 信息类型

| 类型名 | kcpos 源 | 角色 |
|---|---|---|
| `Graph`(超图) | [domain/graph/types.go:11](../../internal/domain/graph/types.go#L11) | attributes=节点, objects=超边 |
| `Object`(超边/函数类型) | [types.go:27](../../internal/domain/graph/types.go#L27) | def/impl/consumes/produces/mutates/status |
| `Attribute`(数据类型节点) | [types.go:16](../../internal/domain/graph/types.go#L16) | def/refines(偏序)/valueSpace/status |
| `Expansion`(节点→子超图超链接) | [types.go:116](../../internal/domain/graph/types.go#L116) | `*string`=子 session id；nil=未展开 |
| `GraphPath`(顶层) | [persistence/graph_store.go:15](../../internal/infra/persistence/graph_store.go#L15) | `K/graph.json` |
| `ExpansionGraphPath`(子层) | [persistence/expansion_store.go:21](../../internal/infra/persistence/expansion_store.go#L21) | `K/expansions/<sid>/graph.json` |
| `ActiveGraphPath`(层解析) | [persistence/active_graph.go:31](../../internal/infra/persistence/active_graph.go#L31) / `FromFocus`:65 | 当前 session 写哪层 |
| `ValidationReport`{Issues,Severity} | internal/domain/graph/checker.go:21/29 | validate/validate-deep 输出 |
| `PreflightResult`{Waves,Cycle} | internal/domain/graph/preflight.go:12 | dep-order 拓扑波 |
| graph-read 输入/输出 | tools/graph/graph_read.go | id/attribute/object_ids(string) → 文本报告 |
| graph-write 输入 | tools/graph/graph.go (create/link/merge) | id/intent/parentAttr/patch(string/JSON) |

## Handler 1.2 Code-Compile-Test — 信息类型

| 类型名 | kcpos 源 | 角色 |
|---|---|---|
| `TypedValue`(载体) | [core/types.go:166](../../internal/typecalc/core/types.go#L166) | (Kind,State,Lang,Channel)+Payload |
| `Kind` | [types.go:39](../../internal/typecalc/core/types.go#L39) | Code/TestSuite/CompileError/TestError… |
| `State` | [types.go:97](../../internal/typecalc/core/types.go#L97) | Uncompiled→Compiled→Tested\<Pass\> |
| `Lang`(六语言) | [types.go:112](../../internal/typecalc/core/types.go#L112) | TS/JS/Go/Rust/Python/**C**(124) |
| `CompileErrorDetail`{Task,ErrorCode,ErrorLog} | [core/request.go:80](../../internal/typecalc/core/request.go#L80) | 编译错原样回传(无enrich) |
| `TestErrorDetail`{TestCase,Expected,Actual} | [request.go:88](../../internal/typecalc/core/request.go#L88) | brownfield 特征化产物 |
| `CompileLanguageInvoker` 签名 | [lang/compile.go:55](../../internal/typecalc/lang/compile.go#L55) | (ctx,RuleEnv,src TypedValue)→TypedValue |
| `TestRunInvoker` 签名 | [lang/test.go:139](../../internal/typecalc/lang/test.go#L139) | (ctx,RuleEnv,compiled,suite)→TypedValue |

## Handler 1.3 Session — 信息类型

| 类型名 | kcpos 源 | 角色 |
|---|---|---|
| `Session`{ID,Parent,Children,Status,…} | [domain/session/types.go:28](../../internal/domain/session/types.go#L28) | 一个工作 session |
| `ExpandsObject` | [types.go:40](../../internal/domain/session/types.go#L40) | 本 session 展开的顶图对象 id |
| `Status`(waiting→active→finished) | [types.go:19](../../internal/domain/session/types.go#L19) | 生命周期 |
| `Input`{Signatures,Context} / `Output`{…GraphDiff} | types.go:47 / 52 | 输入签名/产出+回滚 diff |
| `GraphDiff`{Added,Modified,Removed} | [types.go:76](../../internal/domain/session/types.go#L76) | 回滚用结构 |
| `GateReport`{Status,Issues[]} | internal/app/workflow/session_gate.go:17 | finish gate 原因列表 |
| `ExpansionFinishReasons` 签名 | [workflow/session_expansion_finish.go:32](../../internal/app/workflow/session_expansion_finish.go#L32) | (sessionDir,graphPath,id)→[]reason |
| `PropagateExpansion` 签名 | [session_expansion_finish.go:102](../../internal/app/workflow/session_expansion_finish.go#L102) | 父图 Expansion+confirmed |
| `Rollback` 签名 | [workflow/session_rollback.go:20](../../internal/app/workflow/session_rollback.go#L20) | (sessionDir,graphPath,id)→deleted[] |

---

## 跨 Handler 共享/流转类型（Phase 2.2 类型流的边）

- **`Graph` / `Object` / `Attribute`** 是三 Handler 的公共总线：Session
  Handler 经 `ActiveGraphPath` 决定 Hypergraph Handler 写哪层；
  Code-Compile-Test 的 Confirmed 经 `services.mutateGraph` 改的也是同一
  `Object.Status`。
- **`Object.Status`**(declared→implementing→confirmed) 是 Code-Compile-Test
  与 Session-finish-gate 的共享判据；`confirmed` 只能由验证链/扩展 gate
  机械赋予（① 非手设，[tools/graph/graph.go](../../internal/tools/graph/graph.go) merge 守卫）。
- **`session id`** 同时是 `Expansion` 的值、`ExpansionGraphPath` 的键、
  `ActiveGraphPathFromFocus` 的解析结果——分层超图的连接键。
- **`[]reason`(string)** 是 gate 失败的统一形状（GateReport.Issues /
  ExpansionFinishReasons），非单条 error。

> 这些"共享类型 + 谁产谁消"即 Phase 2.2 类型流图的节点与边的素材。

---

## Phase 2.2 — 类型流图（真实运行观察）

**诚实范围声明**：文档原意是"在 2-3 个真实项目中观察"。实际：用户授权
A 后启动 3 实例最小批（tests/0519_p22 ReverseString/SumInts/IsPalindrome，
deepseek 真跑），**用户在 p22-01 跑到 `confirm_object` 后中止整批**，故
得 **1 条完整真实观察序列**（p22-01，跑到验证链触发）+ P2.1 静态共享类型，
**非** 2-3 条完整跑。这是真实数据、只是样本少；如实标注，不夸大为"多项目
观察"。原始日志：`tests/.batch-logs/p22-01.log`（89 行，DeepSeek）。

### 观察到的真实调用序列（p22-01 ReverseString）

```
list_dir, read_file                         ← 探查 (非 Handler)
└─ session_start                            ← Session Handler: 开 session
   └─ graph_create_attribute                ← Hypergraph Handler: 声明属性节点
      └─ graph_create_object                ← Hypergraph Handler: 声明对象超边
         ├─ write_file ×2                    ← impl/def 落盘
         ├─ graph_link_produce               ← Hypergraph Handler: 连 object→attr 边
         ├─ session_set_architecture         ← Session Handler: 记设计
         ├─ graph_merge_object (impl=...)    ← Hypergraph Handler: 回填 impl 路径
         ├─ write_file/edit×N                ← impl 迭代
         └─ confirm_object                   ← Code-Compile-Test Handler: 触发验证链
```

### 由真实序列归纳的类型流（P2.3 Router 规则的依据）

| 边(类型流) | 源 Handler | 产出类型 | 宿 Handler | 触发条件 |
|---|---|---|---|---|
| open-session | (agent) | `Session`{active,focus} | Session | 任务开始、无活动 session |
| declare-node | Session(focus) | `Attribute`/`Object`(declared) | Hypergraph | session active |
| wire-edge | Hypergraph | `Object`.produces/consumes | Hypergraph | 节点已存在(完整性校验) |
| bind-impl | (impl 落盘) | `Object`.impl 路径 | Hypergraph(merge) | impl 文件存在 |
| trigger-verify | Hypergraph(`Object` declared+impl) | `TypedValue`\<Code\> | Code-Compile-Test | 对象有 impl、未 confirmed |
| confer-confirmed | Code-Compile-Test(链通过) | `Object`.status=confirmed | Hypergraph(机械,非手设) | 验证链/等价 oracle 通过 |
| finish-gate | Session | `GateReport`/`[]reason` | Session | 全对象 confirmed+validate+子完成 |

**关键观察（验证文档"先 Handler 后 Router"）**：真实顺序恒为
`Session 开场 → Hypergraph 反复声明/连边/回填 → Code-Compile-Test 收口
(confirm_object) → Session finish-gate`。Router 不需要自由组合——它只需把
**这条固定偏序**固化为匹配规则（P2.3）。`confirm_object` 是唯一从
Hypergraph 态跨到 Code-Compile-Test 态的边，且 confirmed 只能由该边机械
赋予（① 非手设，全程贯穿）。
