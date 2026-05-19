# KonceptOS 落实 — 设计锁定（P0.2）

> 配套 `KonceptOS_落实计划_2026-05-19.md`。在动核心代码前冻结术语映射、
> 关键取舍、风险锚点，防止执行中反复横跳。**本文档每个 kcpos 落点都经
> grep/ls 接地核查为真实存在路径。**

日期 2026-05-19 ｜ 分支 `feature_260519` ｜ 本项不改业务代码

---

## (1) 文档术语 → kcpos 落点对照表

| 文档术语 | kcpos 落点（已核查存在） | 现状 vs 目标 |
|---|---|---|
| 分层超图 / 节点超链接 | `internal/domain/graph/types.go`（`Graph{Attributes,Objects}`，`Object` 加 `Expansion *string`） | 现：单层平图，`Object` 无 `Expansion`。目标：加字段 |
| `K/expansions/<sid>/graph.json` 分层存储 | `internal/infra/persistence/graph_store.go`（新增 `ExpansionGraphPath/LoadExpansionGraphOrInit/SaveExpansionGraph/ActiveGraphPath`） | 现：`const GraphDefaultPath="K/graph.json"`，API 已路径参数化（`LoadGraph(path)`:18 / `LoadGraphOrInit(path)`:38 / `SaveGraph(path,g)`:50）→ 干净扩展 |
| `graph-read`（show/show-expanded/validate/validate-deep/dep-order/query-downstream） | `internal/tools/graph/`（包 `graphtools`） | 现有 `graph_show` / `graph_validate`。**缺** show-expanded / validate-deep / dep-order / query-downstream → P1.1.3 新增 |
| `graph-write`（create-*/link/unlink/merge-*） | `internal/tools/graph/`（包 `graphtools`） | 现有 `graph_create_{attribute,object}` / `graph_link_{consume,produce,mutate,refine}` / `graph_unlink_*` / `graph_merge_{attribute,object}` / `graph_autowire` / `graph_preflight` / `graph_render` |
| **自动偏序** | 同上 + `Attribute.Refines`（types.go:18） | ⚠ 现：`graph_link_refine` 是 LLM **手动**声明偏序的工具。文档要 **graph-write 自动计算**（子图 produces 精化属性 ≤ 父属性，无需 LLM 手动）→ P1.1.4 真实改造点，非已实现 |
| `session-start/finish/rollback/show` | `internal/tools/session/{session.go,builtins.go,build.go}` + `internal/domain/session/{types.go,diff.go}`（`Session` struct types.go:28） | 现：有会话模型，**无** expansion 绑定 / finish gate / 级联 rollback 删 expansions |
| `compile` handler（6 语言固定编译器） | `internal/typecalc/lang/compile.go`（+`compile_test.go`） | 复用对齐，不重写 |
| `test` handler（6 语言固定框架，TestError→brownfield） | `internal/typecalc/lang/test.go` + `internal/legacy/characterize/{engine,next,persist}.go` | 复用对齐 |
| `Router` / `Format Checker` | `internal/router/{router.go,llm.go,feedback.go,types.go}` + `internal/router/chains/{chain.go,payloads.go,types.go}` | Phase 2 固化匹配规则 + Format Checker |
| `characterize` handler（brownfield 特征测试） | `internal/legacy/characterize/` + 上一轮 `internal/app/services/equiv_oracle.go` | P3.4 规整为独立 Handler，**不回退①②③** |
| `describe/synthesize/review/feedback` handler | `internal/typecalc/{review,synthesize,feedback}`、`internal/router/feedback.go` | P3 规整为独立 Handler 接 Router |

---

## (2) 冻结取舍（不再讨论，照此执行）

1. **会话作用域图改动的权威回滚 = 删 `K/expansions/<sid>/`**（session-rollback）。
   现有 `internal/domain/checkpoint` 机制**仅管文件级**回滚，不接管会话子图。
   两者边界：子图状态 → expansions 目录删除；磁盘文件 → checkpoint。

2. **无 `Expansion` 的对象 = 退化为今天的扁平 `K/graph.json` 行为**（向后兼容）。
   `Expansion` 为 `nil` 时一切走 `GraphDefaultPath`，现有 30 个绿包行为不变。
   这是"加层不砸旧"的护栏，P1.1.1 必须保证。

3. **①②③ 流程正义栈保持不变、不得被本计划任何一项回退**。
   涉及（24 个未提交改动，已在 BASELINE.md 记录）：
   `internal/app/services/equiv_oracle.go`、`internal/app/agent/loop_confirmgate.go`、
   `internal/tools/fs/write.go`（上游门）、`internal/tools/graph/graph.go`
   （`status=confirmed` 非手设硬拒）、`confirm_object_service.go`（reconstruction oracle）。
   多项验收已显式要求"这些旧测试必须仍绿"。

---

## (3) 风险清单（带代码锚点）

1. **【最高】confirm 写图硬编码扁平图，3 处调用点**：
   `internal/app/services/confirm_object_service.go` 在 **L77 / L285 / L435** 调
   `persistence.LoadGraphOrInit(persistence.GraphDefaultPath)`，并在 **L384**
   直接 `obj.Status = graph.StatusConfirmed`。
   Phase 1.3 后这三处必须改为经 `ActiveGraphPath` 写**当前 session 子图**。
   P1.2.3 / P1.3.2 验收特意加"confirm 不污染顶层图"逐字段 diff 断言来守这条。

2. **偏序语义迁移**：`graph_link_refine` 今天是手动工具。改成 graph-write
   自动计算后，旧依赖手动 refine 的测试/调用可能行为变化 →
   P1.1.4 必须保留手动路径可用（自动是叠加，不是替换），并跑全量回归。

3. **会话模型耦合**：`internal/domain/session` 现无 expansion 概念，
   `internal/tools/session` 与 `persistence`/`graphtools` 的联动是新接线
   （ActiveGraphPath 需 session 状态）。注意**域→基础设施单向依赖**，
   persistence 不得反向 import tools（P1.1.2 验收用 grep 自查）。

---

## 执行铁律（每一项都先读后写）

计划提示词里"先读 X"是强制步骤。**若执行中发现现状与本对照表冲突，
停下、报告、不硬套**——本文档可被后续项以"发现 + 修正"方式追加修订，
但不静默偏离。

## 执行中"发现+修正"追加记录

- **[P1.1.4] 跨层偏序归属修正**：文档 graph-write 段说"偏序由 graph-write
  自动计算"，但其 §1.1/§1.3 又把"自动计算子超图和父超图之间的偏序关系"
  明确列为 **session-finish** 的职责。现有域模型 `Attribute.Refines []string`
  是**同图内**引用，跨文件（子图 velocity_x ≤ 父图 velocity）无法塞进
  Refines 而不破坏 `checkRefinesDAG`/`g.Show`。**裁决**：P1.1.4 实现
  graph-write spec 那条 `create-attribute(parentAttr?)` 的**同图内**自动偏序
  （已落地+测试）；**跨层 produce-对应偏序按文档 §1.3 归 P1.3.2
  session-finish 实现**，非静默丢弃，已在计划 P1.3.2 验收项覆盖。
- **[P1.1.4] graph.go 体积**：`graph.go` 是预存在的 ~1000 行大文件（非本计划
  新建）。P1.1.4 只在唯一写入瓶颈 `mutateGraph` 做最小原位改动（+activeWritePath
  helper）。<200 行约束针对本计划**新建**文件；retro-split 既有 graph.go
  风险高、不在 P1.1.4 范围，如实声明不视作违规。

- **[P1.2.2] Rust 测试 = 单文件 `rustc --test`**：文档 Rust→`cargo test`，
  但 cargo 需 Cargo.toml 工程脚手架，与 kcpos 单文件 impl 模型不符。忠实
  等价用 `rustc --test`（构建 #[test] 谐 harness 二进制后运行）。已披露，
  非静默偏离。本机无 rustc/cargo → 相关测试 skip 并标注。
- **[P1.2.2] TestError→brownfield 的实现形态**：文档要"独立 brownfield
  入口"；kcpos 实现为 `KindTestError`（结构化 testCase/expected/actual
  特征化产物）→ chain 的 `Deps.Characterize`/StartCharacterize 阶段 +
  TestLoop 内的 TestReview 三分类(代码错/测试错/描述不清)。同一意图、
  kcpos chain 架构落地，非分歧。已被 `chains` 既有测试锁定。

- **[P1.2.3] confirm 写图分层 + 范围**：新增 `persistence.ActiveGraphPathFromFocus()`
  为唯一解析器；`services.mutateGraph`(typecalc_builtins.go,MarkConfirmed经此)
  与 `graphtools.activeWritePath` 统一调它。confirm_object_service.go 的
  object-resolution 读点(77/285/435)同步改活动层（与写配对）。**已知 P1.3
  依赖**：更广 chain-step 读点(typecalc_service 78/242/438、synthesize_service
  65、review_service 56/203/371)仍读 GraphDefaultPath——仅子会话 focus 时
  相关，P1.3 集成触发并验证，此处显式记录非静默半迁移。valueSpace 回填
  已存在(synthesize_service:144/feedback/merge)经 mutateGraph 自动落活动层。

- **[P2.2] 观察样本降级**：文档要 2-3 个真实项目观察。用户授权 A 后启
  动 3 实例批，p22-01 真实跑到 `confirm_object`（一条完整 Handler 链）后
  用户指令中止整批。故 P2.2 以 **1 条真实完整观察序列 + P2.1 静态共享
  类型**完成，非 2-3 条完整跑。真实数据、样本少，已在 types 文档与计划
  表显式标注，不夸大为"多项目观察"。P2.3 Router 规则据这条真实固定偏序
  + 静态类型流固化。

**P0.2 完成判据**：本文件存在；表中每个落点路径 `ls`/`grep` 真实存在
（已核查）；取舍 3 条、风险 3 条齐全；未改业务代码（`go test ./...` 维持
P0.1 全绿基线）。
