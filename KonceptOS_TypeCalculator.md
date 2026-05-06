# KonceptOS 类型计算器设计文档

> 版本 v0.1（草案）。覆盖已讨论的场景，未覆盖的留白标注。

---

## 一、设计哲学

### 1.1 类型是名义的，不是结构的

传统类型系统通过内容推断类型：`{ vx: 3, vy: 5 }` 被推断为 `{ vx: number, vy: number }`。

类型计算器不看内容——看**来源、状态和权限**：

```
同一段代码文本：
  从 LLM 直接产出  → Uncompiled<Code>
  经过编译器通过    → Compiled<Code>
  经过测试通过      → Tested<Code, Pass>
  经过 checker 确认 → Confirmed<Code>

内容完全相同。类型完全不同。系统对它们的处理方式完全不同。
```

类型由系统赋予（通过生产该值的操作决定），不由 LLM 或内容决定。LLM 唯一能做的类型决策是在 sum 类型中**选择一个分支**。

### 1.2 一切路径由类型驱动

不预先编写工作流的 if-else 逻辑。每个处理节点声明：

```
我接受类型 A 的输入。
我产出类型 B 或类型 C 的输出。
```

系统根据**当前值的类型**自动路由到下一个处理节点。"流程"不是预先设计的——是类型计算器根据当前的输出类型自动驱动的。

### 1.3 失败是类型，不是异常

编译失败不是"出错了"——是产出了 `CompileError` 类型的值。测试失败不是"中断"——是产出了 `TestError` 类型的值。每种失败类型有自己的处理路径，和成功路径地位平等。

### 1.4 "多"来自"少"的组合

类型种类无穷无尽，但由少数基础类型和少数构造器组合而成。类型计算规则也由少数基本规则组成。本质是少的——"多"是组合结构上的多。

---

## 二、基础类型

### 2.1 内容类型（原子）

```
Code            -- 代码文本（具体内容）
Signature       -- 接口签名（输入输出类型声明）
Description     -- 设计意图描述（自然语言）
TestSuite       -- 测试用例集合
TestCase        -- 单个测试用例
Config          -- 配置数据
Spec            -- 需求文档
Architecture    -- 架构设计（子模块列表 + 中间变量）
Graph           -- 超图快照（K 的当前状态）
```

### 2.2 元信息类型（标签）

```
Task            -- 任务描述（做什么）
ErrorCode       -- 错误代码
ErrorLog        -- 错误日志/报告
Reason          -- 原因说明
Value           -- 运行时观测值
AttrPath        -- 属性的偏序路径（如 player_state.position.y）
```

### 2.3 状态构造器

状态构造器**包裹**一个内容类型，赋予其流程状态。同一个 Code 经过不同状态构造器后成为不同类型：

```
Uncompiled<T>     -- 未编译
Compiled<T>       -- 已编译通过
Tested<T, R>      -- 已测试（R = Pass | Fail）
Confirmed<T>      -- 已确认（checker 通过 + 测试通过）
```

### 2.4 语言构造器

```
Lang<L, T>        -- 标注编程语言
  L ∈ { Rust, TypeScript, Go, Python, Haskell, ... }

例：
  Lang<Rust, Code>           -- Rust 代码
  Uncompiled<Lang<Rust, Code>>  -- 未编译的 Rust 代码
```

### 2.5 通道构造器

```
Chan<S, T>        -- 标注所属 session
  S = session_id

例：
  Chan<s_physics, Uncompiled<Lang<TypeScript, Code>>>
  -- 属于 s_physics session 的、未编译的 TypeScript 代码
```

### 2.6 权限构造器

```
Permitted<C, T>   -- 附加权限清单
  C = { cap₁, cap₂, ... }    -- 能力集合

cap ∈ {
  ReadFile(pattern),     -- 读取匹配 pattern 的文件
  WriteFile(pattern),    -- 写入匹配 pattern 的文件
  RunTool(tool_name),    -- 运行指定工具
  SpawnAgent(config),    -- 创建子 Agent
  ...
}
```

### 2.7 错误/请求类型

```
CompileError<Task, ErrorCode, ErrorLog>
  -- 编译失败产物。三部分：原始任务、错误代码、编译器日志

TestError<TestCase, Value, Value>
  -- 测试失败产物。三部分：测试用例、期望输出、实际输出

Request<Task, ...Context>
  -- 请求。Task 是必须的，后面可以附加任意多的上下文信息
  -- Request 可以不断 enrich：
  --   Request<Task> + CompileError → Request<Task, ErrorCode, ErrorLog>
  --   Request<Task, ErrorCode, ErrorLog> + TestError → Request<Task, ErrorCode, ErrorLog, TestCase, ...>

Obstacle<Task, Reason>
  -- 当前 session 无法完成，需要上报

ClarificationNeeded<Task, Questions>
  -- 需求不清楚，需要澄清
```

### 2.8 Sum 类型

```
A | B             -- A 或 B，互斥，无交集
```

LLM 在需要选择输出类型时，**在输出的第一行写类型标签**。一个极简的选择器（非 LLM）提取标签并路由。

---

## 三、类型计算规则

### 3.1 规则的一般形式

```
rule <name>:
  input:  T₁ × T₂ × ... × Tₙ
  actor:  <谁执行这一步：LLM | 编译器 | Checker | Tester | 选择器 | 人>
  output: O₁ | O₂ | ... | Oₘ

语义：
  actor 接收类型为 T₁ × ... × Tₙ 的输入
  产出类型为 O₁ 或 O₂ 或 ... 或 Oₘ 的输出
  输入被消耗（线性）——同一个值不会被两个规则使用
```

### 3.2 核心规则表

#### 需求阶段

```
rule parse_spec:
  input:  Spec
  actor:  LLM
  output: Declared<Signature[]> × Description[]
        | ClarificationNeeded<Task, Questions>

  LLM 从需求文档中提取签名集合和设计意图描述。
  如果需求不清楚，产出澄清请求。
```

#### 架构设计阶段

```
rule design_architecture:
  input:  Declared<Signature[]> × Description[]
  actor:  LLM
  output: Architecture × Graph

  LLM 列出子模块和中间变量，构造初始超图。
  即使任务小到可以一次性完成，也必须先产出 Architecture。

rule validate_structure:
  input:  Graph
  actor:  Checker
  output: Validated<Graph>
        | StructureError<ErrorCode, ErrorLog>

  Checker 验证超图结构（produce/consume 平衡等）。
```

#### 编译阶段

```
rule generate_code:
  input:  Declared<Signature> × Description × Graph
  actor:  LLM
  output: Uncompiled<Lang<L, Code>>

  LLM 产出的内容被自动标记为 Uncompiled。
  语言 L 由 Signature 或项目配置确定。

rule compile:
  input:  Uncompiled<Lang<L, Code>>
  actor:  Compiler(L)
  output: Compiled<Lang<L, Code>>
        | CompileError<Task, ErrorCode, ErrorLog>

rule compiler_in_the_loop:
  input:  CompileError<Task, ErrorCode, ErrorLog>
  actor:  系统（非 LLM）
  output: Request<Task, ErrorCode, ErrorLog>

  系统自动将错误信息附加到原始请求中，形成 enriched request。

rule retry_compile:
  input:  Request<Task, ErrorCode, ErrorLog>
  actor:  LLM
  output: Uncompiled<Lang<L, Code>>

  LLM 收到带有错误上下文的请求，重新生成代码。
  → 回到 rule compile，形成循环。
```

#### 签名提取阶段

```
rule extract_signature:
  input:  Compiled<Lang<L, Code>>
  actor:  系统（AST 分析）
  output: Signature(实际接口)

  从编译通过的代码中提取实际的输入输出接口。
  这是"自下而上确定类型内涵"的机械化——
  编程前 Signature 可能只有名字和粗类型，
  编程后从实现中提取出精确的字段和结构。

rule refine_description:
  input:  Description(粗) × Signature(实际接口)
  actor:  LLM
  output: Description(细)

  结合实际接口细化设计意图描述。
  写清输入什么、输出什么、有什么约束。
```

#### 测试阶段

```
rule generate_test:
  input:  Description(细) × Signature(实际接口)
  actor:  LLM
  output: TestSuite

  关键约束：输入不包含 Code。
  LLM 看不到源码——只看到接口签名和设计意图。
  测试的是契约，不是实现细节。

rule run_test:
  input:  Compiled<Lang<L, Code>> × TestSuite
  actor:  Tester
  output: Tested<Code, Pass>
        | TestError<TestCase, Value, Value>

rule review_test_error:
  input:  TestError<TestCase, Value, Value> × Description(细) × Signature
  actor:  LLM
  output: TestCorrect<TestError>       -- 测试用例正确，代码有 bug
        | TestWrong<Reason>            -- 测试用例有误
        | DescriptionUnclear<Reason>   -- description 不够清晰，需人工补充

  LLM 审查的信息是 description + signature + 一条测试的三元组。
  不看源码——即使模块很大，审查的输入仍然很小。

rule debug_from_test:
  input:  TestCorrect<TestError> × Chan<S, Compiled<Lang<L, Code>>>
  actor:  LLM
  output: Uncompiled<Lang<L, Code>>

  测试用例正确、代码有 bug → LLM 根据错误信息修复代码。
  → 回到 rule compile，形成循环。

rule regenerate_test:
  input:  TestWrong<Reason> × Description(细) × Signature
  actor:  LLM
  output: TestSuite

  测试用例有误 → 根据 description 和 signature 重新生成。
  → 回到 rule run_test。
```

#### 确认阶段

```
rule confirm:
  input:  Tested<Code, Pass> × Validated<Graph> × Signature
  actor:  Checker
  output: Confirmed<Code>
        | StructureError<ErrorCode, ErrorLog>

  Checker 验证：
    - 实现与签名一致
    - produce/consume 仍然平衡
    - 时序正确
```

#### 联调阶段

```
rule merge_for_integration:
  input:  Confirmed<Code>[] × Description[] × Signature[]
  actor:  系统
  output: IntegrationTarget<Code[], Description(整体), Signature(外部接口)>

  合并所有已确认的模块：
    - Description(整体) = 所有 Description 的合并
    - Signature(外部接口) = 过滤掉被内部消费的接口，只留外部接口

rule generate_integration_test:
  input:  Description(整体) × Signature(外部接口)
  actor:  LLM
  output: TestSuite(集成)

rule run_integration_test:
  input:  IntegrationTarget × TestSuite(集成)
  actor:  Tester
  output: IntegrationPass
        | IntegrationError<TestCase, Value, Value>
```

#### 插针法（联调失败后的定位）

```
rule plan_probes:
  input:  IntegrationError<TestCase, Value, Value> × Graph
  actor:  系统（非 LLM，从超图读拓扑）
  output: ProbePlan<AttrPath[]>

  从超图的拓扑排序中提取中间属性列表。
  每个中间属性就是一个插针点。
  不需要 LLM——纯机械操作。

rule execute_probe:
  input:  ProbePlan<AttrPath[]> × IntegrationTarget × TestCase
  actor:  Tester（带插针的测试运行器）
  output: ProbeResult<(AttrPath, Value)[]>

  在每个插针点观测中间变量的值。

rule locate_fault:
  input:  ProbeResult<(AttrPath, Value)[]> × Description[] × Signature[]
  actor:  LLM
  output: FaultLocated<ModuleId, AttrPath, Reason>

  LLM 沿属性的偏序结构逐层检查：
    从最终输出开始 → 哪个中间属性的值开始偏离预期？
    → 该属性的 producer 模块就是嫌疑模块。
  
  注意：沿属性偏序（子类型关系）追踪，不沿对象追踪。
  因为属性间有偏序结构（可以逐层精化缩小范围），对象间没有。

rule fix_fault:
  input:  FaultLocated<ModuleId, AttrPath, Reason> × Chan<S, Compiled<Code>>
  actor:  LLM
  output: Uncompiled<Lang<L, Code>>

  → 回到 rule compile，进入修复循环。
```

#### 用户反馈

```
rule receive_feedback:
  input:  UserFeedback(自然语言) × Graph × AttrPartialOrder
  actor:  LLM
  output: ValueAdjust<AttrPath, NewValue>         -- 调参数
        | LawMissing<AttrPath, NewLaw>             -- 补约束
        | DesignChange<Reason>                      -- 需要重构（人工介入）
        | CannotReproduce<Reason>                   -- 无法复现

  LLM 翻译用户的体验语言为技术语言。
  关键输入是 Graph + AttrPartialOrder——
  超图缩小搜索范围，属性偏序提供追踪脉络。

rule apply_value_adjust:
  input:  ValueAdjust<AttrPath, NewValue> × Graph
  actor:  系统
  output: Graph(更新后) × AffectedModules<ModuleId[]>

  超图自动列出所有受影响的下游模块。

rule apply_law_missing:
  input:  LawMissing<AttrPath, NewLaw> × Graph
  actor:  系统
  output: Graph(更新后) × AffectedModules<ModuleId[]>

  新 Law 加到 Graph 的对应属性上。
  产出受影响的模块列表 → 这些模块需要重新测试甚至重新实现。
```

---

## 四、类型组合的结构

### 4.1 构造器叠加

构造器可以自由叠加，形成精确的复合类型：

```
Chan<s_physics, Uncompiled<Lang<TypeScript, Code>>>
```

含义：属于 s_physics session 的、TypeScript 语言的、未编译的代码。

**叠加顺序**：最外层是最"宏观"的标签（通道归属），最内层是最"微观"的内容（Code）。

```
推荐的叠加顺序（从外到内）：
  Chan<S, ...>              -- 归属哪个 session
    Permitted<C, ...>       -- 有什么权限
      状态<...>             -- 处于什么状态（Uncompiled / Compiled / ...）
        Lang<L, ...>        -- 什么语言
          内容类型           -- Code / Signature / Description / ...
```

### 4.2 Request 的 enrichment

Request 可以不断附加上下文信息，每次 enrichment 产出一个更具体的 Request 类型：

```
Request<Task>
  + CompileError → Request<Task, ErrorCode, ErrorLog>
  + 第二次 CompileError → Request<Task, ErrorCode, ErrorLog, ErrorCode₂, ErrorLog₂>
  + TestError → Request<Task, ..., TestCase, Expected, Actual>
```

**enrichment 规则**：

```
rule enrich:
  input:  Request<...Context> × Error<...NewInfo>
  actor:  系统
  output: Request<...Context, ...NewInfo>

  把错误信息追加到 Request 的上下文中。
  LLM 收到的 enriched Request 包含了完整的失败历史。
```

### 4.3 Sum 类型与选择器

当一个规则的输出是 sum 类型（`A | B | C`）时：

**如果 actor 是系统/工具**：工具自己决定输出哪个分支。例如编译器返回 `Compiled | CompileError`。

**如果 actor 是 LLM**：LLM 在输出的第一行写类型标签。选择器提取标签并路由。

```
LLM 输出格式：
  第一行：TYPE: <类型标签>
  后续行：内容

选择器（非 LLM）：
  1. 读第一行
  2. 提取类型标签
  3. 检查标签是否在当前 sum 类型的合法分支中
     合法 → 包裹内容为对应类型，传给下一个处理节点
     不合法 → TypeError，要求 LLM 重新输出

```

（参考claude code关于子agent机制的设计： ---

## 六、权限模型

### 6.1 子 Agent 的权限

父 Agent 创建子 Agent 时，传递一个权限集合：

```
rule spawn_agent:
  input:  Task × Permitted<ParentCaps, ...>
  actor:  父 Agent
  output: Permitted<ChildCaps, Task>
  
  约束：ChildCaps ⊆ ParentCaps
  子 Agent 的权限不能超过父 Agent。
```

### 6.2 权限检查

子 Agent 的每个操作在执行前经过权限检查：

```
rule permission_gate:
  input:  Permitted<Caps, Action>
  actor:  系统
  output: Action                           如果 Action ∈ Caps
        | PermissionDenied<Action, Caps>   如果 Action ∉ Caps
```

**这不是 prompt 说"你不能做 X"——是类型系统不包含 X。** 即使 LLM 试图输出 X，选择器/类型检查器会在 LLM 输出之后、执行之前拦截。

### 6.3 权限的典型配置

```
-- 实现 session 的子 Agent
ImplementerCaps = {
  ReadFile("K/defs/*"),       -- 可以读签名
  ReadFile("src/*"),          -- 可以读已有实现
  WriteFile("src/<ObjectId>.impl.ts"),  -- 只能写自己的 impl
  RunTool("checker"),         -- 可以运行 checker
  RunTool("compiler"),        -- 可以运行编译器
}

-- 测试 session 的子 Agent
TesterCaps = {
  ReadFile("K/defs/*"),
  ReadFile("src/*.impl.ts"),  -- 可以读实现（但测试用例不根据实现写）
  WriteFile("src/<ObjectId>.test.ts"),  -- 只能写测试
  RunTool("tester"),
}

-- 集成验证的子 Agent
IntegratorCaps = {
  ReadFile("**"),             -- 可以读一切
  RunTool("checker"),
  RunTool("tester"),
  RunTool("builder"),
  -- 注意：没有 WriteFile → 只能读和运行，不能修改代码
}
```

---）
### 4.4 类型检查器

在关键环节插入类型检查器，验证内容是否符合类型的格式约束：

```
rule type_check:
  input:  Claimed<T, RawContent>    -- LLM 声称输出了类型 T
  actor:  TypeChecker
  output: T(content)                -- 验证通过，正式赋予类型 T
        | FormatError<T, Reason>    -- 格式不符，要求重新输出

典型检查项：
  - Lang<Rust, Code>：内容是否是合法的 Rust 语法？
  - Signature：是否包含输入输出类型声明？
  - Architecture：是否列出了子模块和中间变量？
  - TestSuite：是否包含至少一个断言？
```

**类型检查器不理解语义——只验证格式。** "代码是不是正确的"由测试验证。"代码是不是 Rust"由类型检查器验证。

---

## 五、与超图（K）的关系

### 5.1 两个正交维度

```
超图（K）：空间维度
  "这个数据从哪来、到哪去"
  模块之间的 produce/consume 关系
  属性之间的子类型偏序

类型计算器：时间维度
  "这个数据处于什么状态、能做什么操作"
  开发过程中的状态转换
```

### 5.2 交叉点

超图中每个对象和属性都有一个 `status` 字段。这个 status 就是类型计算器赋予的状态标签：

```
graph.json 中：
  objects.UpdatePhysics.status = "confirmed"
  
类型计算器中：
  UpdatePhysics 的代码处于 Confirmed<Lang<TypeScript, Code>> 状态
```

**status 的转换必须遵守类型计算规则。** 不能从 `declared` 直接跳到 `confirmed`——必须经过 `implementing → compiled → tested → confirmed`。

### 5.3 插针法中超图的作用

联调失败时，插针法需要知道"在哪里插针"。超图提供了这个信息：

```
超图的拓扑排序 → 中间属性列表 → 插针点

ProbePlan 由系统从 Graph 自动生成（rule plan_probes），不需要 LLM。
```

### 5.4 用户反馈中属性偏序的作用

用户反馈时，沿属性的偏序结构追踪问题：

```
属性偏序：
  player_state
    └── position ≤ player_state
          ├── position.x ≤ position
          └── position.y ≤ position

追踪路径：
  "player_state 不对"
    → 精化到 "position 不对"
    → 精化到 "position.y 的值没达到预期高度"
    → 查看 position.y 的 producer → UpdatePhysics
    → 查看 UpdatePhysics 的 Laws → 缺少水平遮挡检查

每一步精化缩小搜索范围——属性偏序提供了二分搜索的维度。
对象（函数）间没有偏序关系，无法进行这种逐层精化。
```



## 七、循环与终止

### 7.1 编译循环

```
generate_code → compile → CompileError → compiler_in_the_loop → retry_compile → compile → ...
```

**终止条件**：`compile` 产出 `Compiled`（成功）而非 `CompileError`。

**死循环风险**：LLM 反复产出无法编译的代码。

**应对**：设置最大重试次数 N。超过 N 次后：

```
rule compile_give_up:
  input:  Request<Task, ErrorCode₁, Log₁, ..., ErrorCodeₙ, Logₙ>
  actor:  系统
  output: Obstacle<Task, "编译重试 N 次仍失败">

  → 上报给父 session。
```

### 7.2 测试循环

```
run_test → TestError → review_test_error → TestCorrect → debug_from_test → compile → run_test → ...
```

**终止条件**：`run_test` 产出 `Tested<Code, Pass>`。

**同样设置最大重试次数。**

### 7.3 全局进度保证

整个流程的进度由**超图中 confirmed 对象的数量**单调递增保证。回滚会减少 confirmed 数量，但回滚后的重新实现应该最终增加 confirmed 数量。

**如果 confirmed 数量长时间不增加——系统应该告警，可能需要人工介入。**

---

## 八、实现考量

### 8.1 选择器的实现

```typescript
interface TypedValue<T> {
  type: string;         // 类型标签
  content: unknown;     // 内容
  channel?: string;     // 所属 session
  lang?: string;        // 编程语言
  permissions?: string[]; // 权限清单
}

function parseFromLLM(raw: string, expectedSumType: string[]): TypedValue {
  const firstLine = raw.split('\n')[0];
  const match = firstLine.match(/^TYPE:\s*(\w+)/);
  if (!match) throw new TypeError("LLM 输出第一行缺少 TYPE 标签");
  
  const tag = match[1];
  if (!expectedSumType.includes(tag)) {
    throw new TypeError(`类型 ${tag} 不在合法分支 [${expectedSumType}] 中`);
  }
  
  const content = raw.split('\n').slice(1).join('\n');
  return { type: tag, content };
}
```

### 8.2 路由器的实现

```typescript
type Handler = (input: TypedValue) => Promise<TypedValue>;

const router: Record<string, Handler> = {
  'Uncompiled':    compileHandler,
  'Compiled':      testHandler,
  'CompileError':  compilerInTheLoopHandler,
  'TestError':     reviewTestErrorHandler,
  'TestCorrect':   debugHandler,
  'TestWrong':     regenerateTestHandler,
  'Tested_Pass':   confirmHandler,
  'Confirmed':     nextModuleHandler,
  'Obstacle':      escalateHandler,
  // ...
};

async function step(value: TypedValue): Promise<TypedValue> {
  const handler = router[value.type];
  if (!handler) throw new Error(`没有 ${value.type} 类型的处理器`);
  return handler(value);
}

// 主循环：不断 step 直到终止状态
async function run(initial: TypedValue): Promise<TypedValue> {
  let current = initial;
  while (!isTerminal(current.type)) {
    current = await step(current);
  }
  return current;
}
```

### 8.3 类型检查器的实现

```typescript
interface FormatChecker {
  (content: string): { ok: boolean; reason?: string };
}

const formatCheckers: Record<string, FormatChecker> = {
  'Lang_Rust': (code) => {
    // 检查是否是合法的 Rust 语法（调用 rustfmt --check）
    // 不检查语义，只检查格式
  },
  'Lang_TypeScript': (code) => {
    // 检查是否是合法的 TypeScript 语法
  },
  'Signature': (text) => {
    // 检查是否包含输入输出类型声明
  },
  'TestSuite': (text) => {
    // 检查是否包含至少一个断言语句
  },
  'Architecture': (text) => {
    // 检查是否列出了子模块列表和中间变量
  },
};
```

---

## 九、待设计的部分

### 9.1 类型的持久化格式

类型标签和元信息如何存储在 session.json 和 graph.json 中？当前的 `status` 字段是类型状态的雏形，但缺少语言、通道、权限等其他标签。

### 9.2 并行 session 的类型隔离

多个无关的 session 并行时，它们的类型空间如何隔离？Chan 构造器提供了理论基础，但具体的隔离机制需要设计。

### 9.3 类型的版本管理

类型定义本身可能演变（比如 Signature 的字段增加了）。类型的版本管理和向后兼容性需要考虑。

### 9.4 自定义类型规则

企业版中用户可能需要添加自定义的类型计算规则（如"所有 API 相关的代码必须经过安全审查才能 confirmed"）。自定义规则的注册和验证机制需要设计。


### 9.5 测试用例本身的类型化

目前 TestSuite 是一个原子类型。也许测试用例本身也应该有更精细的类型——如 `UnitTest<ModuleId>` vs `IntegrationTest<ModuleId[]>` vs `PropertyTest<Law>`。

---

*本文档版本 v0.1 | 覆盖了编译循环、测试流程、插针法、用户反馈、权限模型。*
*未覆盖：并行类型隔离、类型版本管理、自定义规则、平台对接。*
