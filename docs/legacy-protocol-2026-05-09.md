# AGENT.md — KonceptOS v17 Simulation Framework

> 本文件是 coding agent（Claude Code / Codex）的指令集。
> 目标：在正式编程落地前，用 coding agent 模拟 KonceptOS 全流程，发现设计缺陷并迭代。
> Agent 在模拟阶段一人分饰多角：设计者、实现者、检查器、session 管理器。

---

## 0. 产物层级定义（最重要，先读）

下列每一层都是路径上的工序，**只有 L4 是项目的最终产物**。除非达到 L4，否则**根 session 永远不允许 finished**。

| 层 | 内容 | 是否产物 |
|---|------|--------|
| L0 | `K/defs/*.ts` 签名 + `graph.json` 节点声明 | 工序（不是产物） |
| L1 | `src/*.impl.ts` 实现 + graph 中 `confirmed` 状态 | 工序（不是产物） |
| L2 | `src/*.test.ts` 单元/契约测试通过 | 工序（不是产物） |
| L3 | `integration.test.ts` 集成测试通过 + `dist/bundle.js` 构建成功（如有 bundle 形式）| 工序（不是产物） |
| L4 | 1) `K/checkpoint.json` 所有 must 项已填 `codeProof` 且 `summary.finalVerdict === "PASS"` + 2) 根 session output 字段聚合所有子 session 产物 + 3) 实际可运行的交付物存在于约定路径（`index.html` / `dist/bundle.js` / 等）| **唯一终态产物** |

**最常见的失败模式**：agent 把 L1 / L2 / L3 完成度高当作"做完了"，看到 graph 全 confirmed、测试全绿就停手，但 L4 一个步骤都没动。**这种状态等同于未完成**，必须继续推进至 L4。

测试绿是中点，不是终点。bundle 能打也是中点，不是终点。**checkpoint 全部 codeProof 填齐 + finalVerdict=PASS + 产物文件就位** 才是终点。

> **2026-05-11 v8.8 协议简化**：早期版本（v8.7-）要求 L4 必须包含"实际运行的 gameplayProof（截图/录屏）"。v8.7 5 并发批次显示该要求在 CLI agent 环境下不可能可靠满足——agent 要么编造描述性 gameplayProof（5/5 实例都没真正跑 snap），要么系统性跳过。L4 现以"机械可验证 + 交付物存在"为完成标准；运行时证据（如有需要）将以独立子系统形式提供（`kcpos snap`，v8.9+ 规划），与 checkpoint 解耦。

---

## 1. 核心概念

### 1.1 一切都是类型

| 术语 | 含义 | 超图中的角色 |
|------|------|-------------|
| **属性类型** | 数据类型——在对象之间流动的值的契约 | 节点 |
| **对象类型** | 函数类型——接受若干属性、产出若干属性 | 超边（连接多个输入节点到多个输出节点）|

对象不发明运算——对象本身就是属性运算的定义。编程成功后，对象的实现就成为属性类型的已确认合法运算。

### 1.2 偏序与精化

所有类型构成偏序结构。子类型关系 `a <: A` 表示 a 满足 A 的所有约束，并附加更多约束。

**永远不规定粗类型等于哪些精化类型的总和。** 精化类型可以随时加入或删除。粗类型的实际内涵由被消费的精化类型自下而上反向定义。

### 1.3 四条自动推导规则

以下关系**不写入 graph.json**，由 checker 按需计算，放进 agent 推理上下文：

| # | 规则 | 表述 |
|---|------|------|
| 1 | **逆变** | 若 `a <: A`, `g: A → B`, 则 `g: a → B` |
| 2 | **积精化** | 若 `a <: A`, `b <: B`, 则 `(a, b) <: (A, B)` |
| 3 | **依值偏序** | `B(a) <: B`——若关系只涉及 B 无依值约束，则对任意 `B(a)` 都生效 |
| 4 | **消费驱动定义** | 被消费的精化类型反向定义粗类型的内涵 |

**重要**：使用依值类型、积类型等可构造出无数 trivial 的类型及其关系，不应全部存入超图。graph.json 只存储显式声明的关系。

### 1.4 自下而上原则

属性类型的值空间、运算、Laws 不是预先定义的：

```
阶段 1（只有壳）：名字 + 设计意图 + 偏序关系
阶段 2（实现中）：对象代码逐步确定属性的具体结构
阶段 3（已确认）：实现成功后，回填 valueSpace、confirmedOps、laws

实现失败 → 不回填，回滚，session 删除
```

### 1.5 时序：Event 类型与 .succ()

用 Event 类型表示时序。Event 有合法运算 `.succ()`，返回 Event。

- `e` 是一个 Event
- `e.succ()` 是下一个 Event
- `e.succ().succ()` 是再下一个 Event
- 以此类推——`.succ()` 可以任意链式调用

`velocity(e)` 和 `velocity(e.succ())` 是不同时刻的数据，不可混用。

TypeScript 实现方案：运行时 Event 类 + graph.json 中的 temporal 声明 + checker 验证时序因果性。

---

## 2. 项目结构

```
KonceptOS/
├── K/
│   ├── graph.json            ← 超图：拓扑 + 所有元数据（唯一真相源）
│   ├── checkpoint.json       ← 验收检查点（详见 CHECKPOINT.md，立卷后只允许填证据）
│   ├── proofs/               ← 检查点的玩法证明产物（截图/录屏，按 CHK-XXX/ 分目录）
│   ├── defs/                 ← TypeScript 类型定义与函数签名
│   │   ├── raw_data.ts       ← 属性类型接口
│   │   ├── UpdateVelocity.ts ← 对象类型签名
│   │   └── ...
│   └── sessions/             ← 扁平 session tree（每个活跃/完成的 session 一个 JSON）
│       ├── s_root.json
│       ├── s_physics.json
│       └── ...
├── src/                      ← 实现代码（方程右边）
│   ├── _event.ts             ← Event 类（运行时时序支持）
│   ├── UpdateVelocity.impl.ts
│   └── ...
├── tools/
│   ├── checker.ts            ← 形式检查 / 查询工具（只读，验结构）
│   ├── graph-edit.ts         ← graph.json 结构化编辑工具（写入口）
│   └── parallel-preflight.ts ← 子 session 并行派发前检查（事前约束）
├── AGENT.md                  ← 本文件（同 CLAUDE.md）
├── CHECKPOINT.md             ← 检查点流程协议（立卷 / 逐项填证 / 验收红线）
├── tsconfig.json
└── package.json
```

---

## 3. graph.json 规范

graph.json 是项目的超图表示——唯一的结构真相源。

### 3.0 graph-edit 约定

`graph.json` 虽然是真相源，但 **agent 不应优先用手工文本编辑方式修改它**。默认写路径是 `tools/graph-edit.ts`：

- 新增属性 / 对象：用 `create`
- 建立 `refines` / `consumes` / `produces`：用 `link`
- 小范围修改状态、impl、intent、valueSpace、laws：用 `merge`
- 删除错误连接：用 `unlink`
- 查询某节点/对象的局部邻接关系：用 `show`
- 判断两个对象之间按偏序可自动接通哪些边：用 `autowire`

只有在以下情况才允许直接重写 `graph.json`：

1. 项目初始化时创建空图
2. 结构性大迁移，且 `graph-edit` 当前命令集无法表达
3. 回滚时需要按 `graphDiff` 批量恢复旧值

除上述情况外，凡是“更新 graph.json”都应理解为“优先调用 `graph-edit`，再运行 `checker validate`”。

### 3.1 Schema

```jsonc
{
  "attributes": {
    "<attribute_id>": {
      "def": "defs/<attribute_id>.ts",
      "refines": ["<parent_id>"],           // 显式偏序（仅直接父类型）
      "intent": "设计意图（宁可冗余不可遗漏）",
      "valueSpace": null,                    // null = 待定; 实现后填充
      "confirmedOps": [],                    // 由成功实现自下而上确定
      "laws": [],                            // 由实现推导
      "status": "declared",                  // declared | implementing | confirmed
      "statusSession": null                  // 最近变更 status 的 session ID
    }
  },
  "objects": {
    "<object_id>": {
      "def": "defs/<object_id>.ts",
      "impl": null,                          // null | "src/X.impl.ts"
      "consumes": ["<attribute_id>"],        // 输入属性类型集合
      "produces": ["<attribute_id>"],        // 输出属性类型集合
      "intent": "设计意图（宁可冗余不可遗漏）",
      "temporal": null,                      // null | TemporalSpec（见 3.2）
      "preconditions": "",
      "postconditions": "",
      "status": "declared",
      "statusSession": null
    }
  }
}
```

### 3.2 temporal 字段

```jsonc
{
  "frameVar": "e",                           // 帧变量名
  "consumes": [                              // 数组——同一属性可出现多次（不同帧）
    { "attribute": "velocity", "frame": "e" },
    { "attribute": "acceleration", "frame": "e" }
  ],
  "produces": [
    { "attribute": "velocity", "frame": "e.succ()" }
  ]
}
```

**帧表达式语法**：

```
<frame_expr> ::= <frameVar>              // 基础：e
               | <frame_expr>.succ()     // 后继：e.succ(), e.succ().succ(), ...
```

`.succ()` 是 Event 类型的合法运算，可任意链式调用。不设人为限制。

**多帧输入示例**（平滑滤波器，需要当前帧和上一帧）：

```jsonc
{
  "frameVar": "e",
  "consumes": [
    { "attribute": "velocity", "frame": "e" },
    { "attribute": "velocity", "frame": "e.succ()" }
  ],
  "produces": [
    { "attribute": "smoothed_velocity", "frame": "e.succ()" }
  ]
}
```

### 3.3 状态转换

```
declared ──(session 开始实现)──→ implementing
implementing ──(实现成功 + checker 通过 + 测试通过)──→ confirmed
implementing ──(session 失败/回滚)──→ [session 被删除, 状态恢复为 declared]
confirmed ──(祖先 session 回滚)──→ [级联删除, 状态恢复为 declared]
```

**测试要求**：session 从 active 转为 finished 之前，必须通过测试。测试用例根据接口签名和设计意图（intent 字段的自然语言描述）编写，**不根据源代码编写**——测试的是契约，不是实现细节。

---

## 4. 命名规则与防冲突

### 4.1 命名约定

| 类别 | 格式 | 示例 |
|------|------|------|
| 属性类型 ID | `snake_case` | `raw_data`, `temperature`, `wind_speed` |
| 对象类型 ID | `PascalCase` | `WeatherProcessor`, `UpdateVelocity` |
| TS 定义文件 | 与 ID 同名 `.ts` | `raw_data.ts`, `UpdateVelocity.ts` |
| 实现文件 | ID + `.impl.ts` | `UpdateVelocity.impl.ts` |
| Session ID | `s_` + 描述性短名 | `s_root`, `s_weather_proc` |

属性 snake_case，对象 PascalCase——天然不冲突。

### 4.2 防冲突机制

1. **全局唯一命名空间**：attributes + objects 所有 ID 不允许重复。
2. **创建前必查**：添加新 ID 前检查 graph.json 中是否已存在。
3. **Checker 强制验证**：`validate` 包含唯一性检查。
4. **冲突时用限定名**：如 `weather_temperature` 而非 `temperature`。
5. **禁止重命名 confirmed 条目**：避免破坏引用链。
6. **Session 记录创建物**：graphDiff 明确记录哪些 ID 由本 session 创建，回滚时精确清理。

### 4.3 文件大小硬约束（防 max_tokens 死循环）

> **历史教训**：曾出现 agent 试图把 60 物块 + 90 配方 + Buff 列表全塞进单个 `data.ts`，单次 `Write` 调用因 32K output token 上限被截断，dev-entry 反复注入"Resume directly"导致 agent 无限重试同一文件，整个执行 phase 死锁。本节是该问题的强制防护。

#### 硬性数值

- **单个 `.ts` 文件严禁超过 1500 行**（含空行、注释；含 `defs/` 与 `src/` 下所有文件）
- 写文件前必须**先估算行数**：
  - 数据表（每条目独立行 + 5+ 字段）：行数 ≈ 条目数 × 1.5
  - 配方表：行数 ≈ 配方数 × 1
  - 函数 / 类逻辑：行数 ≈ 抽象长度 × 1.5
  - **任意估算结果 > 1200 行 → 立刻拆，不要赌**
- 参考阈值：Claude 单次 `Write` 工具调用的 output token 上限为 32K；密集对象字面量约 1000 行就接近上限

#### 大数据模块的强制拆分模板

含大量数据表的模块**必须**按子类别拆为多个 `.impl.ts` 文件（每个 ≤ 1500 行）：

| 模块 | 拆分方式 |
|------|---------|
| 物块（60 种） | `tiles_natural` / `tiles_ore` / `tiles_wood` / `tiles_placed` / `tiles_workstation` / `tiles_special` |
| 敌怪（36 种） | `enemies_surface` / `enemies_underground` / `enemies_dungeon` / `enemies_hell` / `enemies_corruption` / `enemies_jungle` |
| 配方（90 条） | `recipes_workstation` / `recipes_ore` / `recipes_basic` / `recipes_weapon` / `recipes_armor` / `recipes_potion` / `recipes_ammo` / `recipes_summon` |
| Buff/Debuff | `buffs_positive` / `buffs_negative` |
| NPC | `npcs_basic` / `npcs_advanced` |
| Boss | 每个 Boss 单独一个 `boss_<name>.impl.ts` |
| 音效 / BGM | `audio_effects` / `audio_bgm` |

每个子文件单独走 §6.2 实现流程（独立 `defs/`、独立 `*.test.ts`、独立 graphDiff 条目）。

#### 撞 max_tokens 截断时的恢复规则

**绝对禁止**：在撞 `stop_reason: max_tokens` 之后直接 Resume 重写同一文件。这是死循环的来源。

**正确做法**：
1. 立刻识别"内容超出单文件容量"
2. **不再尝试 Write 那个文件**，先在内存中（或临时分析里）把内容拆分为 2–N 个子文件
3. 每个子文件单独 Write，每个 Write 之前再次估算 ≤ 1500 行
4. 拆分后同步更新 `defs/` 与 `graph.json`（每个新子模块作为独立对象）
5. 在当前 session.json 的 `output.newSignatures` 里记录拆分结果

#### 检测工具

审计 agent 与执行 agent 都可以跑下面这条命令，机械验证文件大小约束：

```bash
find src K/defs -name '*.ts' -exec wc -l {} \; | awk '$1 > 1500 {print "OVERSIZE:", $0}'
```

任何输出 → 视为违规，必须拆分后才能让本 session finished。

---

## 5. Session 管理

### 5.1 Session 状态

Session 只有三种状态：

| 状态 | 含义 |
|------|------|
| `waiting` | 已创建，尚未开始工作 |
| `active` | 正在进行 |
| `finished` | 成功完成 |

**没有 "failed" 或 "rolled_back" 状态。** 如果 session 失败或需要回滚，它会被直接删除——连同其 session 文件。失败的尝试不留痕迹。

#### 5.1.1 未达成清单（用法：每完成一轮工作必查；任一不满足就必须**继续干**，而不是停下）

**这份清单不是"准备 finished 时的检查表"，而是"驱动 agent 继续推进的循环条件"。** Agent 每写完一个 `.impl.ts`、跑完一次测试、合并一个子 session，都必须立即把整张表过一遍——任何一项未满足，**强制继续工作**，禁止以"我先停下汇报"的形式让流程中断。

**任何 session（含 root）**：

1. 本 session 在 graphDiff 中创建/接管的每个对象，`status` 必须是 `confirmed`，且 `impl` 字段非 null
2. 每个 `impl` 文件必须真实存在于 `src/`，且文件大小 > 0
3. 每个 confirmed 对象必须有同名 `*.test.ts`，测试已运行且全部通过
4. 没有任何子 session 处于 `waiting` 或 `active` 状态（必须全部 finished 或被删除）

**仅根 session 额外要求**（详见 §5.5 根 session 收尾流程）：

5. `src/` 中 `.impl.ts` 文件总数 ≥ checkpoint.json 中 `must` 项数 × 0.5（粗略下限，防止只画图不写代码；单文件多函数交付物的项目按 SPEC 约定的交付物 ≥ 1 计）
6. `K/checkpoint.json` 中所有 `must` 项的 `codeProof` 已填，且 `summary.finalVerdict === "PASS"`
7. 根 session 的 `output.implementations` / `output.tests` 字段已聚合所有子 session 的产物列表（不能为空数组）

**绝对不允许的"伪完成 / 提前停手"模式**（出现任一即视同流程违规，必须立即继续）：

- ❌ 只创建 `defs/*.ts` 签名，`src/` 为空，就把 session 标 finished 或停下
- ❌ 子 session 都没开就把根 session 标 finished
- ❌ 测试文件存在但内容是 `it.skip` / `expect(true).toBe(true)` 形式
- ❌ 把"立卷完成"或"签名设计完成"当作里程碑交付——它们只是工序（参见 §0）
- ❌ **写完代码 + 测试全绿 + bundle 打包成功 就以为做完了** —— 这只是 §0 的 L3，离 L4 还差 checkpoint 全部 codeProof 填齐 + finalVerdict=PASS + 产物文件就位
- ❌ 子 session 都 finished 了就报告"完成"而停手——根 session 还有 §5.5 一整套收尾流程要走
- ❌ 在 token / 注意力下降时悄悄缩减交付范围（必须显式声明阻碍而不是缩减）

### 5.2 session.json 格式

```jsonc
{
  "id": "s_weather_proc",
  "parent": "s_root",                          // null 仅限根 session
  "children": ["s_extract_temp", "s_extract_wind"],
  "status": "active",                          // waiting | active | finished
  "task": "实现 WeatherProcessor: raw_data → temperature, wind, pm",

  "input": {
    "signatures": ["WeatherProcessor"],         // 本 session 负责的对象
    "context": ["raw_data", "temperature", "wind", "pm"]
  },

  "output": {
    "implementations": [],                      // 成功后填充
    "newSignatures": [],                        // 拆分时新增的子对象
    "newAttributes": [],                        // 新发现的中间属性类型
    "graphDiff": {                              // 对 graph.json 的修改记录
      "added": { "attributes": {}, "objects": {} },
      "modified": {
        "attributes": {},
        "objects": {}
      },
      "removed": { "attributes": [], "objects": [] }
    }
  }
}
```

**graphDiff.modified 格式**（存 before/after 对）：
```jsonc
"modified": {
  "attributes": {
    "temperature": {
      "before": { "status": "declared", "valueSpace": null },
      "after":  { "status": "confirmed", "valueSpace": { "celsius": "number" } }
    }
  }
}
```

### 5.3 回滚与删除

当 session 失败时：

```
1. 读取本 session 的 output.graphDiff
2. 递归处理：先回滚并删除所有子 session（深度优先）
3. 逆向应用 graphDiff：
   - added → 从 graph.json 删除
   - modified → 用 before 值覆盖
   - removed → 重新加入
4. 删除本 session 创建的实现文件（src/*.impl.ts）
5. 删除本 session 创建的定义文件（仅 graphDiff.added 中的）
6. 删除本 session 文件（sessions/s_xxx.json）
```

回滚完成后，这个 session 不存在了——没有 "rolled_back" 状态，没有残留文件。

说明：回滚优先按 `graphDiff` 逆向恢复；若只是移除单条连接或小范围字段，也可调用 `graph-edit unlink` / `graph-edit merge` 完成。

### 5.4 Session 生命周期

```
1. 创建 session.json（status: waiting）
2. 开始工作（status: active）
3. 判断任务复杂度：
   a. 能一次性完成 → 路径 A（直接实现）
   b. 需要拆分 → 路径 B（开子 session）
   c. 无法完成 → 路径 C（上报 + 删除）
```

**路径选择硬阈值**：

- 当前 session 的对象数 ≥ 3 个 **或** 累计 .impl.ts 行数预估 ≥ 400 行 **或** task 跨越 ≥ 2 个需求文档章节 → **必须**走路径 B
- 仅当对象数 = 1 且预估实现 < 400 行时才允许路径 A
- 不确定时**默认走路径 B**——虚构子 session 比把活咽下去再翻车便宜得多

**路径 A：直接实现（仅适用于单一细粒度对象）**

```
1. 【必须】架构设计（即使一次性完成，也先理清结构再写）：
   a. 列出子模块和中间变量（不要求创建子 session，但必须在当前 session 中记录）
   b. 把粗对象拆成多个细对象
   c. 可能引入中间属性类型
   d. 用 graph-edit 更新 graph.json（新签名 status: declared）
   e. 创建 defs/*.ts
   f. 记录 graphDiff
   g. 运行 checker validate（拆分后 produce/consume 是否平衡）
2. 编写 TypeScript 实现 → src/<ObjectId>.impl.ts
   （子模块可以写在同一个 impl 文件中，或分成多个 impl 文件）
3. 运行 checker validate（验结构）
4. 编写测试：
   a. 根据每个对象的签名（输入输出类型）和 intent（设计意图）编写测试用例
   b. 不根据源代码写测试——测试的是契约，不是实现细节
   c. 典型测试项：
      - 接口测试：输入合法类型，输出符合签名声明的类型
      - Laws 测试：属性的不变量在操作前后成立
      - 时序测试：output.event.step === input.event.step + 1
      - 边界测试：根据 intent 描述的约束，测试边界条件
   d. 运行测试，全部通过
5. checker PASS + 测试通过：
   a. 自下而上回填属性类型（valueSpace, confirmedOps, laws）—— 每个被本对象
      produces 的属性都要回填，不允许只填一部分
   b. 用 graph-edit 回填 graph.json（impl, status → confirmed）
   c. 记录 graphDiff
   d. session.status → finished
6. 不通过：
   a. 如果可修复 → 修改实现，重新走 3、4、5
   b. 如果不可修复 → 路径 C
```

**路径 B：拆分**

```
1. 设计子结构：
   a. 把粗对象拆成多个细对象
   b. 可能引入中间属性类型
   c. 用 graph-edit 更新 graph.json（新签名 status: declared）
   d. 创建 defs/*.ts
   e. 记录 graphDiff
2. 运行 checker validate（拆分后 produce/consume 是否平衡）
3. 提取依赖关系，按拓扑排序
4. 【关键】检测值依赖——决定哪些子 session 可以并行：
   a. 结构依赖：对象 A produces 属性 X，对象 B consumes X → B 依赖 A
   b. 值依赖：对象 A 和对象 B 都 consume 同一属性 X，
      且 A 的输出值域（如坐标范围、尺寸）必须与 B 的假设兼容
      → A 和 B 之间存在值依赖，不能并行
   c. 典型值依赖场景：
      - 生产关卡数据的对象 与 执行物理模拟的对象（共享物理常量）
      - 定义 UI 布局的对象 与 处理输入坐标的对象（共享尺寸参数）
   d. 值依赖的对象必须串行：先实现产出共享属性的对象，
      其值确认后再实现消费该属性的对象
5. 在真正派发子 agent 之前，必须运行事前检查：
   `npm run parallel:preflight -- --session <parent_session_id> --fail-on-conflict`
   a. 若脚本返回 SAFE，才允许把该批对象一次性并行派发
   b. 若脚本返回 UNSAFE，则禁止整批并行
   c. 必须按脚本给出的 waves 顺序推进
   d. 若出现 serial group，则这些对象必须串行执行，或合并为一个更大 session 统一处理
6. 为每个细对象创建子 session（status: waiting）
7. 按顺序执行子 session：
   - 先完成产出共享属性的对象（如 InitGame）
   - 再并行执行有值依赖的消费者（此时它们可以读取已确认的值）
   - 无依赖的可自由并行
8. 每个子 session 完成后运行 checker validate
9. 某个子 session 失败：
   → 该子 session 被删除（5.3 流程）
   → 父 session 尝试解决（见 6.3 Obstacle 处理）
   → 如果无法解决 → 父 session 自身也走路径 C
10. 所有子 session finished → 父 session 在标记 finished 之前必须：
    a. 编写集成测试（根据签名和 intent，不根据源代码）
    b. 运行集成测试，全部通过
    c. 完成 §6.5 集成验证（跨模块一致性 + 集成测试）
    d. 【仅根 session】**继续进入 §5.5 完成 R1–R5 全部步骤**——不允许在 §6.5 通过后立即 finished
    e. 之后才允许 status → finished（最终闸门：§5.1.1 全部 8 项满足）
```

**路径 C：失败上报**

```
1. 整理失败原因（缺什么？为什么做不到？建议方向？）
2. 执行回滚（5.3 流程）——本 session 被删除
3. 将失败信息传递给父 session 的上下文
4. 父 session 决定如何处理
```

### 5.5 根 session 专属收尾流程（不可与子 session 共用）

**这是 agent 最常忽略的章节。** 所有子 session finished 不等于根 session 可以 finished——根 session 还有一整套**唯一属于它**的收尾步骤。下列每一步**逐项执行、逐项验证**，禁止跳步：

```
前提：所有子 session 已 finished，§5.1.1 第 1–4 项已满足。

步骤 R1：产物聚合
  a. 把每个子 session 的 output.implementations 收集进 s_root.output.implementations
  b. 把每个子 session 的 output.tests 收集进 s_root.output.tests
  c. 把每个子 session 的 output.newSignatures / newAttributes 合并到 s_root.output 对应字段
  d. 写入 s_root.json（不允许保持空数组）—— session_aggregate 工具自动完成

步骤 R2：构建与本地验证（按交付物形态决定，不强制 bundle）
  a. 若 SPEC 要求 bundle / 多文件项目：运行 npm run build，产出 dist/bundle.js
  b. 若 SPEC 要求单文件交付物（如 index.html）：检查文件存在且大小 > 0
  c. 运行全部 *.test.ts（vitest run），必须全绿
  d. 运行 integration.test.ts（如存在），必须全绿

步骤 R3：checkpoint 逐项填证（参见 CHECKPOINT.md §4）
  a. 打开 K/checkpoint.json，确认 frozen === true 且 items 与立卷时一致
  b. 对每个 item：
     - codeProof：填具体 src/X.impl.ts:行号 + 关键导出名（或单文件项目下 index.html:行号 + 函数名）
     - verifiedAt
  c. 计算 summary：totalItems / passed / waived / failed
  d. 任一 must 项缺 codeProof → finalVerdict = "FAIL"，回到本步骤补
  e. 全部填齐 → finalVerdict = "PASS"

步骤 R4：最终闸门
  a. 重新跑一次 §5.1.1 的 7 项条件（v8.8: 简化自原 8 项，去除 gameplayProof）
  b. 跑 CHECKPOINT.md §5.1 的豁免上限校验（waiver 比率上限、重复理由检测、章节级 must 不缺证）
  c. 全部满足 → s_root.status: active → finished
  d. 任一不满足 → 不允许 finished，回到对应步骤继续
```

**最常见的错觉**："R2 完成（测试绿 + bundle 打）就觉得做完了"——这只是 §0 的 L3。**R3 / R4 是根 session 真正的工作量**，token / 注意力必须为它留够预算。

**v8.8 协议简化说明**：原 v8.7- 版本 R3 是"运行 npm run snap → K/proofs/CHK-XXX/ 截图"。v8.7 5 并发批次显示该步骤在 CLI agent 环境下不可能可靠满足，全部跳过或写描述性占位。v8.8 删除此步骤；运行时证据采集（如有需要）将作为独立子系统 `kcpos snap`（v8.9+ 规划）提供，与 checkpoint 解耦。当前 L4 完成定义：codeProof 全填 + finalVerdict=PASS + 交付物文件就位。

**waiver 边界**：当 checkpoint 某 must 项写不出 codeProof 时（如该项验证依赖 UI / 异步事件 / 浏览器 API），通过 `checkpoint_waive` 标记并给出具体理由。CHECKPOINT.md §5.1 的比率上限和重复理由检测是 waiver 的硬约束——批量 waiver 会被审计直接打回 FAIL。

---

## 6. 工作流

### 6.1 启动

用户给出项目目标后：

```
1. 初始化项目目录结构
2. 读取并理解 tools/ 下所有 3 个工具：checker / graph-edit /
   parallel-preflight，必须知道每个工具的命令面与时机
3. 创建 K/graph.json（空图）
4. 创建 K/sessions/s_root.json（status: active）
5. 【强制立卷】创建 K/checkpoint.json（参见 CHECKPOINT.md）：
   a. 通读用户给出的需求文档
   b. 按 CHECKPOINT.md §3 列出的所有类别（A–S）逐类拆出可机械验证的检查项
   c. 每条 description 必须给出"如何观察才算通过"的判定方式
   d. 全部写入后置 frozen: true、记录 frozenAt——从此 items 不允许增删改
   e. 此时所有 item 的 codeProof 必须为 null（尾声才填）
6. 设计顶层签名（仅作为后续拆分的根，不是终态）：
   a. 识别粗属性类型（数据通道）
   b. 识别粗对象类型（处理单元）
   c. 用 graph-edit create/link 写入 graph.json（status: declared）
   d. 创建对应 defs/*.ts
7. 运行 checker validate
8. 【强制】立刻开始路径 B 拆分——禁止把 6 步生成的粗对象当作终态：
   a. 每个粗对象至少再拆成 ≥ 3 个细对象（细对象 = consume/produce ≤ 4 个属性）
   b. 拆完后子 session 必须真正进入实现：写 .impl.ts、写测试、回填 confirmed
   c. 子 session 数量 = 0 时根 session 永远不允许 finished
9. 进入主驱动循环（直到 §5.1.1 全部 8 项满足才允许退出）：
   while not §5.1.1 全部 8 项已满足:
     a. 找出当前 graph.json 中 status != confirmed 的对象 → 走 §5.4 路径 B 实现
     b. 每完成 1 个子 session：立即对照 §5.1.1 第 1–4 项
     c. 第 1–4 项满足后：进入 §5.5 根 session 收尾流程（R1 → R5）
     d. R3 / R4 任何一步缺证据 → 必须返回 R3 补采集 / 补复现，不允许跳到 finished
   循环唯一退出口：§5.1.1 全部 8 项满足 + s_root.status 改为 finished
```

> **硬性顺序**：第 5 步未完成（checkpoint.json 不存在或 frozen != true）时，第 6 步禁止开始。第 6 步完成后**禁止跳过第 8 步**——只画签名不写实现 = 未完成。第 9 步是**唯一的循环条件**——agent 不允许在循环内自行决定"我做完了"，只能依据 §5.1.1 的机械判据。

### 6.2 实现一个对象类型

```
1. 先调用 graph-edit show <ObjectId>，读取该对象的信息（consumes, produces, temporal, intent）
2. 读取相关属性类型的当前状态（可能只有 intent，无 valueSpace）
3. 【关键】读取所有已完成的上游实现：
   a. 在 graph.json 中找到所有 produces 本对象所 consumes 属性的对象
      （优先用 graph-edit show / autowire 辅助定位）
   b. 如果这些对象的 status 为 confirmed 且 impl 非 null，必须读取其实现文件
   c. 从上游实现中提取具体数值（物理常量、尺寸、坐标范围、约束条件）
   d. 本对象的实现必须与上游的具体值兼容
4. 如果需要时序信息，调用 checker query temporal 确认帧流
5. 编写 TypeScript 实现：
   a. 创建 src/<ObjectId>.impl.ts
   b. 实现函数逻辑——所有数值假设必须与上游实现一致
   c. 如有时序，使用 Framed<T> 包装 + Event.succ() 推进
6. 运行 checker validate（验结构）
7. 编写测试：
   a. 创建 src/<ObjectId>.test.ts
   b. 根据签名的输入输出类型和 intent 编写测试用例
   c. 不根据源代码写测试——测试的是契约
   d. 运行测试，必须全部通过才能进入第 8 步
8. 通过 → 自下而上回填（每个 produces 的属性都要回填，不允许省略）：
   a. 根据实现确认属性的 valueSpace
   b. 将本对象加入属性的 confirmedOps
   c. 从实现中推导 laws（如果有不变量）
   d. 用 graph-edit merge 回填 graph.json
9. 不通过（checker 或测试任一失败）→ 分析并修复或上报
```

### 6.3 Obstacle 处理

```
子 session 失败并被删除 → 父 session 收到失败信息

父 session 的处理步骤：
1. 分析失败原因（缺什么数据？类型冲突？不可能实现？）
2. 检查自己的作用域：
   a. 自己的输入中是否包含缺失数据？
   b. 其他子 session 的产出是否有所需数据？
   c. 能否通过偏序关系推导？（调用 checker query 辅助）
3. 如果能解决：
   → 修改拆分方案（补传数据 / 新增子 session）
   → 更新 graph.json + checker validate
   → 创建新的子 session 重试
4. 如果不能解决：
   → 父 session 自身也失败
   → 执行回滚（5.3）——父 session 被删除
   → 继续上报给祖父 session
5. 到达根 session 仍无法解决：
   → 报告给用户，请求人工介入或修改 spec
```

### 6.4 修改传导

当属性类型定义被修改时：

```
1. 查找 graph.json 中引用该属性的所有对象（consumes/produces）
2. 对每个受影响的 confirmed 对象：
   a. 检查实现是否仍满足新约束
   b. 满足 → 不动
   c. 不满足 → status 改为 declared，开新 session 重新实现
3. 运行 checker validate
```

### 6.5 组装后集成验证（仅覆盖跨模块一致性 + 集成测试；根 session 收尾全套见 §5.5）

> **本节定位**：父 session（含根 session）合并子 session 产物时的"L3 闸门"——验证模块间数值一致与集成测试通过。
> **不是终点**：本节通过仅相当于 §0 的 L3。**根 session 通过本节后还必须走 §5.5 R3–R5**，不要把本节当成 finished 前的最后一步。

```
1. 跨模块值一致性检查：
   a. 对每个被多个 confirmed 对象消费的属性，提取各消费者对该属性值的假设
   b. 验证这些假设是否与产出者的实际值兼容
   c. 典型检查项：
      - 物理参数一致性：跳跃高度 = jumpSpeed² / (2 * gravity)，
        所有使用跳跃高度的模块（关卡设计、可达性检查）必须使用相同的值
      - 坐标空间一致性：tileSize、gridCols、gridRows 在所有模块中一致
      - 碰撞体尺寸一致性：玩家宽高、宝石尺寸在所有模块中一致

2. 集成测试：
   a. 端到端数据流：从 producer → consumer 的数据能正确流通
   b. 可达性验证：如涉及空间/路径，验证设计出的结构在物理引擎下可达
   c. 状态机完整性：所有预期的状态转换路径能被触发
   d. 若产出物是 web 应用：必须实际执行（如 esbuild 打包 + headless 加载
      index.html 或浏览器人工验证），不能仅靠"打包成功"代替运行验证

3. 如果第 1、2 步验证不通过：
   → 定位不一致的模块
   → 回滚有问题的子 session（§5.3）
   → 用修正后的值约束重新实现
   → 重新走第 1、2 步

4. 第 1、2 步通过后：
   → 父 session（非根）：可以标记 finished
   → 根 session：**必须继续进入 §5.5（产物聚合 / 实际运行 / checkpoint 填证 / 最终闸门），不允许在此 finished**
```

---

## 7. 时序类型实现

### 7.1 运行时 Event 类

```typescript
// src/_event.ts
export class Event {
  constructor(public readonly step: number) {}
  succ(): Event { return new Event(this.step + 1) }
  equals(other: Event): boolean { return this.step === other.step }
  toString(): string { return `Event(${this.step})` }
}

export interface Framed<T> {
  data: T
  event: Event
}
```

### 7.2 签名定义示例

```typescript
// defs/UpdateVelocity.ts
import type { Framed } from '../src/_event'
import type { Velocity } from './velocity'
import type { Acceleration } from './acceleration'

export type UpdateVelocity = (
  vel: Framed<Velocity>,
  acc: Framed<Acceleration>
) => Framed<Velocity>
```

graph.json 中的 temporal 字段提供帧语义（checker 读取此字段，不解析 TS）。

### 7.3 实现示例

```typescript
// src/UpdateVelocity.impl.ts
import type { UpdateVelocity } from '../K/defs/UpdateVelocity'

export const updateVelocity: UpdateVelocity = (vel, acc) => {
  if (!vel.event.equals(acc.event)) {
    throw new Error(`Event mismatch: vel=${vel.event}, acc=${acc.event}`)
  }

  return {
    data: {
      vx: vel.data.vx + acc.data.ax,
      vy: vel.data.vy + acc.data.ay,
    },
    event: vel.event.succ(),
  }
}
```

### 7.4 Checker 的时序验证

Checker 从 graph.json 的 temporal 字段读取帧规约，验证：

1. 帧表达式语法合法（符合 `<frameVar>(.succ())*` 格式）
2. 时序因果性：所有输出帧深度 ≥ 所有输入帧深度（不能输出到"过去"）
3. temporal 中引用的属性必须存在于 graph.json
4. temporal 中的属性集合应覆盖对象的 consumes/produces

---

## 8. Checker 使用

三个工具配套使用，**任何一个都不能跳过**：

| 工具 | 角色 | 强制时机 |
|------|------|---------|
| `graph-edit` | 结构化写入 graph.json | 任何写改图操作 |
| `parallel-preflight` | 子 session 派发前事前约束 | 派发并行子 session 前 |
| `checker` | 形式 / 结构验证 + 偏序推导查询 | 每次改图后；每次实现前查时序 |

工作流：

1. 先用 `graph-edit` 改图
2. 若要并行派发子 session，先用 `parallel-preflight` 做事前检查
3. 用 `checker validate` 验结构
4. 写完实现后编写测试（根据签名和 intent），运行测试
5. 需要推导关系时再调用 `checker query`

### 8.1 命令

```bash
# graph.json 结构化编辑（首选写入口）
npm run graph:edit -- show <Id>
npm run graph:edit -- create attribute --id <attribute_id>
npm run graph:edit -- create object --id <ObjectId>
npm run graph:edit -- link refine --child <attribute_id> --parent <attribute_id>
npm run graph:edit -- link consume --object <ObjectId> --attribute <attribute_id>
npm run graph:edit -- link produce --object <ObjectId> --attribute <attribute_id>
npm run graph:edit -- merge attribute --id <attribute_id> --patch '<json>'
npm run graph:edit -- merge object --id <ObjectId> --patch '<json>'
npm run graph:edit -- autowire --producer <ObjectId> --consumer <ObjectId>

# 子 session 并行派发前检查（事前约束）
npm run parallel:preflight -- --session <session_id> --fail-on-conflict
npm run parallel:preflight -- --objects <ObjectId1> <ObjectId2> <ObjectId3> --fail-on-conflict

# 全量形式检查（每次修改 graph.json 后必须运行）
npx ts-node tools/checker.ts validate

# 按需查询：逆变推导
npx ts-node tools/checker.ts query contravariant --object <ObjectId> --input <AttributeId>

# 按需查询：精化覆盖
npx ts-node tools/checker.ts query coverage --attribute <AttributeId>

# 按需查询：时序一致性
npx ts-node tools/checker.ts query temporal --object <ObjectId>

# 按需查询：积精化
npx ts-node tools/checker.ts query product --attributes <AttrId1> <AttrId2>
```

### 8.2 validate 检查清单

| # | 检查项 | 级别 | 说明 |
|---|--------|------|------|
| 1 | Produce/Consume 平衡 | ERROR | 每个被 consume 的属性，至少有一个对象 produce 它或其超类型 |
| 2 | 偏序 DAG | ERROR | refines 关系无环 |
| 3 | 精化覆盖 | ERROR | 产出者必须覆盖消费者需要的精化子集 |
| 4 | 帧一致性 | ERROR | temporal 声明的帧流因果合法 |
| 5 | 命名唯一性 | ERROR | 全局命名空间无重复 |
| 6 | 引用完整性 | ERROR | 所有 refines/consumes/produces 指向已存在 ID |
| 7 | 孤立类型 | WARN | produce 但未 consume |
| 8 | 实现对应 | WARN | impl 字段与文件系统一致 |
| 9 | 元数据完整性 | WARN | 每条目有 def 和非空 intent |
| 10 | 值约束一致性 | WARN | confirmed 属性的 laws 在所有消费者之间不矛盾 |

### 8.3 使用时机

- **初始化 graph 或新增签名时**：先用 `graph-edit create/link`
- **查看局部拓扑和上游下游对象时**：先用 `graph-edit show`
- **判断两个对象能否通过偏序直接接通时**：先用 `graph-edit autowire`
- **状态回填、impl 回填、laws / valueSpace 更新时**：用 `graph-edit merge`
- **派发子 agent 之前**：必须先运行 `parallel-preflight`
- **每次修改 graph.json 后**：运行 `checker validate`
- **设计签名时**：`checker query contravariant` 确认连接合法
- **拆分任务时**：`checker query coverage` 确认覆盖
- **实现时序对象时**：`checker query temporal` 确认帧流
- **写完实现后**：编写测试用例（根据签名和 intent），运行测试
- **session 标记 finished 之前**：所有测试必须通过

---

## 9. 测试

测试是与实现等同的硬产物，**不是收尾装饰**。

### 9.1 测试原则

- **根据签名和 intent 编写**，不根据源代码——测试的是契约，不是实现细节
- **session 从 active 转为 finished 的硬前提**：测试必须全部通过
- **每个对象实现完成后必须编写测试**，不允许"先全部实现再集中测试"

### 9.2 测试类型

```
1. 接口测试（每个对象必须有）：
   - 输入合法类型 → 输出符合签名声明的类型
   - 输入边界值 → 不崩溃
   - 根据 intent 描述的约束条件，验证输出满足约束

2. Laws 测试（有 laws 的属性必须有）：
   - 属性的不变量在操作前后成立
   - 例：magnitude(velocity) >= 0

3. 时序测试（有 temporal 的对象必须有）：
   - output.event.step === input.event.step + 1
   - 不同帧的数据不混用

4. 集成测试（父 session 在所有子 session 完成后编写）：
   - Produce/consume 链的数据流通
   - 跨模块值一致性（物理参数、坐标空间、尺寸）
   - 端到端场景（从初始状态到预期结果的完整路径）
```

### 9.3 测试文件命名

```
src/UpdatePhysics.impl.ts     ← 实现
src/UpdatePhysics.test.ts     ← 测试（与实现同名，后缀 .test.ts）
```

---

## 附录 A：graph.json 空模板

```json
{
  "attributes": {},
  "objects": {}
}
```

## 附录 B：session.json 模板

```json
{
  "id": "",
  "parent": null,
  "children": [],
  "status": "waiting",
  "task": "",
  "input": {
    "signatures": [],
    "context": []
  },
  "output": {
    "implementations": [],
    "newSignatures": [],
    "newAttributes": [],
    "tests": [],
    "graphDiff": {
      "added": { "attributes": {}, "objects": {} },
      "modified": { "attributes": {}, "objects": {} },
      "removed": { "attributes": [], "objects": [] }
    }
  }
}
```
