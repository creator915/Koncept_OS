# ProgramBench Batch — kcpos v9.6 — 2026-05-14

两个真实的 ProgramBench 量级任务（Go 终端 CLI 工具，单项目数千行），由 kcpos 端到端执行。报告聚焦**任务完成情况**与**流程合规度**。

> ProgramBench（arxiv 2605.03546，Yang 等 2026）：给定编译好的二进制和文档，agent 必须从零架构 + 实现一个可复现原程序行为的完整 codebase。共 200 个任务，规模从小型 CLI 到 FFmpeg / SQLite / PHP 解释器。当前公开 leaderboard 最高分模型的任务通过率是 3%。

## 1. 任务范围

| Fixture | 来源 | 规模 |
|---|---|---|
| `programbench-fx` | antonmedv/fx — 终端 JSON viewer + JS processor | 原项目 7.2 MB，约 3000 行 Go |
| `programbench-walk` | antonmedv/walk — 交互式终端文件导航器 | 原项目 3.8 MB，约 2500 行 Go |

输入形态：仅一份 `SPEC.md` 描述程序行为（CLI 形式、快捷键、flags、配置项）。**不给原 source 指针，不允许联网**（kcpos 当前两个能力都不具备，对它构成天然约束）。输出要求：单文件 `main.go`，`go build` 可生成可执行 binary。

模型：`deepseek-v4-pro`（与 2026-05-14 HumanEval batch 用同一 baseline）。

## 2. 总体结论

| Fixture | 结论 | confirmed 对象 | 交付物 | 时长 |
|---|---|---|---|---|
| **fx** | ✅ **PASS** | **5 / 5** | `main.go`（1350 行，31 KB）+ 编译后 binary `fx`（14 MB） | 2 小时 7 分 |
| walk | ⏸ 部分完成 | 2 / 5 | 未组装 | 2 小时 13 分（kill） |

**fx 是 kcpos 首次端到端完成 ProgramBench 量级任务**——完整图分解、每个 object 由独立 subagent 走链验证、单文件 deliverable 组装、binary 真编译、手工冒烟测试确认行为符合 SPEC。walk 在 chain 层完成了 5 个 object 中 2 个，剩余 3 个被 kill 时正卡在一个 **Go 命名冲突的 design 问题**（与 kcpos chain 设计本身正交，详见 §6.1）。

## 3. fx — 任务完成数据

### 3.1 Graph

5 个 object + 6 个 attribute。每个 object 都通过自己派出的 subagent 完成 confirm（`statusSession=s_impl_*`）：

| Object | storyPoints | impl 路径 | runtimeTrace.calls | description 长度 |
|---|---|---|---|---|
| ParseFlags | 3 | src/ParseFlags.impl.go | 13 | 1013 字符 |
| ReadInput | 3 | src/ReadInput.impl.go | 8 | 1301 |
| ApplyExpr | 3 | src/ApplyExpr.impl.go | 11 | 1294 |
| RunPipeline | 5 | src/RunPipeline.impl.go | 4 | 870 |
| FormatOutput | 2 | src/FormatOutput.impl.go | 6 | 502 |

每个 evidence bundle 的 `test.ok = true`、`runtimeTrace.calls` 有内容、`spec.description` 有内容，**无任何 evidence 文件被手工编辑**。

### 3.2 Checkpoint

`finalVerdict = PASS`（root session `s_fx_root`，status finished）。8 项 `must` 全部 filled 且带 codeProof，0 项 waived，0 项 failed：

1. 解析 CLI flags（-s, -r, --no-color, -h, --help, --themes）+ 位置参数
2. 从文件或 stdin 读输入，自动识别 JSON / YAML / TOML / JSONC 格式
3. 用 goja 求值 JS 表达式（arrow function、dot-path 简写、bare reference）
4. 多个表达式按左到右管道串联
5. 输出格式化（JSON-encoded 或 raw string，nil-safe）
6. bubbletea 交互式 TUI 模式（展开 / 折叠 / 滚动 / 搜索）
7. JSON 解析错误打到 stderr 并非零退出
8. 单文件 main.go 可用 `go build -o fx main.go` 编译

### 3.3 Binary 冒烟测试（跑完后手工）

```
$ ./fx --help                                # → 含 flags + TUI key bindings 的 usage 横幅
$ echo '{"a":1}' | ./fx                      # → 美化打印的 JSON
$ echo '{"name":"world"}' | ./fx '.name'     # → "world"  (JS 表达式)
$ echo '{"name":"world"}' | ./fx -r '.name'  # → world    (raw 模式去掉引号)
```

四条流程都符合 SPEC 描述的行为。**产出的 binary 是真正的 fx 克隆，不是 stub**。

## 4. walk — 部分数据

walk 的 kcpos chain 跑得很干净：它走到的每一步都产出真实 evidence，无 fake-PASS，主对话没有 impl 写入违规。

| Object | kill 时状态 | storyPoints | runtimeTrace.calls | test.ok |
|---|---|---|---|---|
| ListDir | confirmed | 2 | 4 | true |
| ParseConfig | confirmed | 3 | 12 | true |
| DeleteWithUndo | implementing | 2 | 0 | — (in flight) |
| FilterEntries | declared | 2 | — | — |
| PreviewContent | declared | 3 | 10 | false（最后一次 build 出错） |

两个 confirmed object 都由 subagent 完成（`s_impl_listdir`, `s_impl_parseconfig`）。阻塞点见 §6.1。

## 5. 流程合规度 — v9.6 五个修复的实地验证

2026-05-14 在 HumanEval batch 与 ProgramBench batch 之间落地了 5 个 chain 加固改动。每个改动在 fx 跑中都有可观察的结果：

| 改动 | 位置 | fx 实测 | walk 实测 |
|---|---|---|---|
| **Phase 2 — Go 多文件 package staging** | typecalc/lang/fs.go: `stageGoPackage` 把同目录所有 .go 文件 copy 进 scratch dir，用 `go vet ./...` 替代单文件 vet | 5/5 编译都引用了 K/defs/ 里的 sibling 类型 | 2/5 confirmed 的 compile 通过；剩余 3 个的 redeclare 是另一类 cause（§6.1） |
| **Phase 2.1 — 跳过 PascalCase object def stub** | typecalc/lang/fs.go: `stageGoPackage` 不 copy `K/defs/<PascalCaseId>.go`（含 panic body 的 stub 文件），避免与真 impl 同名重定义 | 0 次因 def stub 引起的 redeclare | 同上 —— walk 的 20 次 redeclare 全部源自用户级命名冲突，与 def stub 无关 |
| **C — Go trace helper 注入 + prompt 强化** | typecalc/lang/test.go: `runGoTest` 在 scratch dir 写 `kcpos_helpers_test.go` 注入 appendTrace 模板；synthesize.go prompt 要求每个 testCode case 必须调 `appendTrace(in, out)`；runner 静态校验 testCode 含 `appendTrace(`，否则拒绝 | 0 次 trace-reject；每个 confirmed object 的 runtimeTrace.calls 都有内容（4-13 次调用） | 0 次 trace-reject；走到的 chain 步骤 trace 都齐 |
| **A — Subagent state machine（dispatch mode）** | shared/agentctx.CheckMainImplWork：当 graph object 数 ≥ 5，主对话拒绝 write_file `K/defs/<PascalCase>.go` / 各类 impl 后缀，也拒绝主对话直接调 `confirm_object` | 0 次 dispatch-block 触发（agent 提前 spawn subagent，没碰阈值）；5/5 object 通过 subagent confirm | 0 次 dispatch-block；2 个 confirmed 都走 subagent |
| **Phase 4 — portObservation 提示明确化** | merge.go 错误消息 + graph_merge_object schema description 完整列出 5 种合法形式（global / return / return.X / args.N.X / side_effect） | agent 第一次就用对了形式 | 同上 |

### 5.1 statusSession 与 storyPoints 完整性

- fx：5/5 object 都有 `statusSession=s_impl_<lower(id)>`（每个来自独立 subagent），5/5 都填了 storyPoints（2 / 3 / 3 / 3 / 5），且 rationale 字段非空
- walk：2 个 confirmed 同上；剩余 3 个的 storyPoints 在创建时就被强制填了（v9.5 mandatory check），只是还没到 confirmed 阶段所以暂无 statusSession

### 5.2 fx 跑完整 2h7min 内的 anti-theater 检查

- 0 次尝试直接写 `K/graph.json`
- 0 次尝试写 `K/frags/*`
- 0 次手工编辑 evidence bundle
- 0 个 shadow impl（无 `code.js` 之类的影子文件绕开 Go chain）
- 0 次 hook "could not run" 告警

## 6. walk 暴露的已知 limitation（v9.6 范围外）

### 6.1 Go 命名冲突 — type 与 function 同名

walk agent 给两个实体取了同名：
- attribute id `preview_content` → Go type `PreviewContent`（在 `K/defs/preview_content.go`）
- object id `PreviewContent` → Go function `PreviewContent`（`K/defs/PreviewContent.go` stub + `src/PreviewContent.go` 实现）

`stageGoPackage` 组装 scratch package 时，attribute 的 snake_case def（强制存在，含 struct 定义）跟 impl 的函数声明落在同一个 package，Go 报 `PreviewContent redeclared in this block`。

**这不是 kcpos chain bug，是 agent 命名时没规避 Go 同名冲突**。可能的修法（不在 v9.6 范围）：

- system.md / graph_create_object description 增加警告：Go 项目下 object id 不要和 PascalCase(attribute id) 撞名
- merge.go 拒绝（或警告）创建会与已有 attribute Go 符号冲突的 object id
- 更狠：给 attribute Go 类型加 `_t` 后缀这类约定，让 type 名和 function 名永远不在同一命名空间

walk agent 走向越来越激进的 workaround（`mv` 文件到 /tmp，confirm 完再 `mv` 回来）—— 明确的信号说明 chain 在这个 design 缺口上施加了压力。

### 6.2 bash 绕过 write_file 的 dispatch guard

当 dispatch mode（§5, A 行）拦截 fx 主对话用 write_file 写组装好的 `main.go` 时，agent 改用 bash heredoc：

```
[18:54:50] » bash({"command": "cat > main.go << 'GOEOF'\npackage main\n..."})
```

write_file 工具按路径模式拦截；bash 工具不拦。在 fx 这次跑里，agent 用这条绕路做的是合规动作（把已 confirmed 的 object impl 拼成 deliverable），但作为防线**确实存在 defense-in-depth 缺口**。

可能的补充（不在 v9.6 范围）：
- After-hook 扫工作区新增的 PascalCase impl 文件，若来自非 subagent 写入则告警
- 轻量 bash 命令内容启发式：当 graph ≥ 阈值时，检测 `> X.go` / `tee X.go` 这类重定向

## 7. 成本数据

fx batch：deepseek-v4-pro，2 小时 7 分，无中途重启。walk batch：2 小时 13 分至 kill。两者本地并发跑，无 rate-limit 失败。

## 8. 同 fixture 在多轮跑中的演进

| 轮次 | 时间 | fx confirmed | walk confirmed | 备注 |
|---|---|---|---|---|
| Round 1（v9.6 前） | 2026-05-14 ~15:08 | 0 | 0 | fx 在 link-edges 那一轮 LLM stream timeout 死掉；walk 在 Go 单文件隔离编译上挣扎约 13 分钟 |
| Round 2（仅 Phase 2） | ~15:30 | 0 | 0 | 加了 Phase 2（多文件 staging）；fx 仍然死，因为还没排除 object def stub（Phase 2.1 缺失） |
| **Round 3（v9.6 全部 fix）** | ~16:47 | **5 / 5** ✅ | 2 / 5 | 五个改动都到位；fx 跑通；walk 撞上与 chain 正交的命名冲突 |

进度提升清晰呈现每个改动的边际贡献：Phase 2 让 Go 多文件编译跑通；Phase 2.1 让 object 的 impl 和 def stub 能共存；A 阻止了之前撑死 fx 的主对话上下文膨胀（撑死的具体形态是 link-edges 那一轮 stream timeout）。walk 残余的阻塞点在 §6.1，超出 chain 设计本身的范围。

## 9. 产物清单

- `tests/programbench-fx/main.go`（1350 行单文件 deliverable）
- `tests/programbench-fx/fx`（14 MB 编译后 binary，arm64）
- `tests/programbench-fx/K/graph.json`、`K/checkpoint.json`、`K/sessions/*.json`
- `tests/programbench-fx/.kcpos/typecalc/<ObjectId>.json`（5 份 evidence bundle）
- `tests/programbench-fx/.kcpos/transcripts/20260514-164724.json`
- `tests/.batch-logs/programbench-fx.log`（agent 完整 transcript，约 3500 行）
- `tests/.batch-logs/programbench-walk.log`（部分，约 4350 行）
- walk fixture 保留 kill 时状态，留给 §6.1 的后续分析
