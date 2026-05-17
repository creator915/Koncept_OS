# Legacy Characterization MVP — 落地实跑 — 2026-05-17

把《屎山代码维护Agent设计文档 v1.0》规定的 **MVP 核心切片**实现进 kcpos，并跑通文档定义的"first real agent task"。报告聚焦：**做了什么 / 守住了什么范式 / 第一跑暴露了什么**。

## 1. 实现范围（严格按文档自身纪律裁剪）

文档明令：`不要为了"看起来完整"而填充未设计部分`；迭代触发器 = `first real agent task exposing ≥ 3 unidentified issues`。因此**没有**实现全部 11 部分，只实现文档规定的 MVP 核心：

| 文档条目 | 落地 |
|---|---|
| Part 6.6 Characterization 五步 | `internal/legacy/characterize` 引擎 |
| Part 6.6 Method Use Rule（硬规则） | `session_gate.go` `[method-use-rule]`（**仅遗留路径**） |
| Part 2.4b Finite / Reproducible Evidence | `TestRunRecord` / `ExecutableTestSuite` |
| Part 2.4 / 原则C Oracle + conditional_on + 置信向量 | `Oracle` + `ConfidenceVector`（**无标量压缩**） |
| Part 2.1 / 11.A 选项3 Assumption | string + metadata，定在 BusinessLogic 层 |
| 原则A 类型驱动 workflow | router 前置 stage `StartCharacterize→Characterized` |

## 2. 范式结论（关键）

kcpos 是绿地构造器（朝给定 SPEC 构造并验证）。棕地是反的：契约不存在，必须从不可信产物**反推**。

落地方式 = **SPEC 生产前置 stage**：characterize 反推出 golden 契约 → 喂给现有 `compile→describe→synthesize→test→review→gate` 链**原样消费**。`一切都是对契约验证的 TypedValue` 范式**完整保留**，只是契约从"给定"变为"派生"。

加性证据：
- `go test ./...` **21/21 包全绿，无 FAIL**；现有 chain/gate 测试字节级不变。
- router 前置 stage 是**可选 Dep**：未注入时链条 == 改动前的绿地机；现有转移零修改。
- bundle 新 section 是加性指针字段，老 bundle 反序列化无感；绿地对象 `Characterization==nil`，门禁规则对绿地**惰性**。

## 3. 第一跑（文档要求的 real task）

fixture：`tests/legacy-char-demo/fuel_share.py` —— 无测试、作者已离职、下游发票依赖其确切数字十年的"屎山"。`fuel_share(gallons,drivers)` 用 floor-division，截断是 **load-bearing**（车队会计按司机向下取整是故意的）。

`kcpos characterize fuel_share.py --symbol fuel_share --produces result` 实跑（真实 DeepSeek synthesize）：

- LLM 合成 8 个探针（无可信期望）；harness 把探针跑在**不可信遗留文件**上；引擎把观察到的输出**确定性转录**成 golden Expect。
- **8 锁定 / 0 未覆盖**，coverage=1.000，置信逐维度报告（independence 等诚实标注 "not measured"），2 条 conditional 假设（env 快照 + artifact hash），Finite+Reproducible Evidence 落 bundle。

锁住的关键真实行为（**正是文档论点**——char test 记录现实，不评判对错）：

| 调用 | 锁定值 | 含义 |
|---|---|---|
| `fuel_share(10, 3)` | `3.0` | 截断被冻结（不是 3.333）——load-bearing 行为保住 |
| `fuel_share(-10, 3)` | `-4.0` | Python 负数 floor-div 反直觉但**真实**，照实锁定（不是 -3） |
| `fuel_share(5, -1)` | `0.0` | drivers≤0 守卫行为锁定 |

`-10//3 == -4` 这条：人和裸 LLM 都倾向"修正"成 -3，characterization **拒绝评判**、照实冻结——这是 Q3（怎么知道没改坏）的根。

## 4. 暴露的问题 → 已写入 ITERATION-v1.1

按文档反馈机制（`未识别的问题 → 加入 Part 11 清单`），第一跑暴露 **5 个**问题（>3，达成下一版触发条件），详见仓库根 `ITERATION-v1.1.md`。其中 1 个（PortObservation 未透传 → return 值函数零锁）在 demo 前发现并修复+记录；4 个为开放设计缺口（其中"覆盖充分性/黑箱独立输入生成"是文献也未解的硬问题，对应 ProgramBench 最强 3%）。

## 5. 产物清单

- 引擎：`kcpos/internal/legacy/characterize/`（types/engine/adapter/persist + 单测，引擎纯 seam、无网络）
- 加性证据：`kcpos/internal/typecalc/core/characterization.go` + `bundle.go` 一个加性字段
- 门禁：`session_gate.go` `[method-use-rule]`（仅遗留路径）
- 前置 stage：`router/chains/{types,payloads,chain}.go`（可选 Dep）
- 命令：`kcpos characterize <file>`（`cmd/kcpos/commands/characterize.go`）
- fixture + 锁：`tests/legacy-char-demo/fuel_share.py` + `.kcpos/typecalc/fuel_share.json`
- 迭代清单：`ITERATION-v1.1.md`（文档 v1.1 触发已满足）
