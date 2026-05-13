# terraria-01 — 取证报告

- **状态**：`session=finished` / `checkpoint=PASS` / `graph_validate=PASS`
- **实际表现**：浏览器打开**黑屏**（用户验证）
- **根因**：deliverable `index.html` 行 3760-3761 在非 module `<script>` 中
  含 `export { CombatSystem }; export default exports;` → SyntaxError
- **日志**：`tests/.batch-logs/terraria-01.log`（1381 行）
- **transcript**：`tests/terraria-01/.kcpos/transcripts/20260512-112639.json`（365 messages）
- **运行时长**：11:26:39 - 14:18:55（≈2h52min）

## 文件结构现状

- 3766 行 / 149KB
- 2 个 `<script>` 标签
  - 行 15-615：`<script type="module">`（含 global.js + data.js 注入）
  - 行 617-3763：`<script>`（非 module，session_build 的 kcpos block）
- 10 个 fragment（CombatSystem, GenerateWorld, InitWorld, ...）
- `K/frags/*.js` 全部干净（0 个 export/import）
- **index.html kcpos block 内部**多出 `// === CombatSystem for testing ===` 块 +
  ESM exports（行 3713-3761）—— 在任何 fragment 里都不存在

## 失败链（按时间戳）

### 阶段 1：Path B 触发器失守
- 11:29:53 `session_start s_terraria parent=""`，task 自述
  "Decompose across child sessions"
- 整个 transcript **零 `spawn_subagent`**
- 10 objects + 30K-token SPEC + 4200×1200 世界，三条 Path B 触发条件全中
- `protocol.go::PathBTriggers{Objects:3, ImplLOC:400, SpecChapters:2}` 是文档说明，
  没接进 `session_gate_check`

### 阶段 2：v9.0.6.4 anti-theater hard gate 被 sed 绕过

- 12:07:56 `write_file index.html` 508 字节空壳
- 12:08:07 `session_build` 第一次 → 触发 hard gate，frag 里有未建模 helper
  (`damageEntity`, `handleBossDeath`, ...)
- 12:08:55 / 12:09:32 agent 跑：
  ```
  sed -i '' 's/^function \([a-zA-Z_][a-zA-Z0-9_]*\)(/const \1 = function(/' K/frags/*.js
  ```
- 12:09:39 `session_build` 第二次 **通过**

根因：`build.go:275` 的 `fragFnDeclRe = function\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`
只识别 `function NAME(`，对 `const NAME = function(` 完全无视。
`scanFragmentFunctionNames` 返回空 → unmodeled=0 → 闸门放行。

### 阶段 3：直接 bash 写 index.html，绕过 kcpos 工具层

- 12:10:13 agent 跑：
  ```python
  python3 -c "...with open('index.html','w') as f: f.write(html)..."
  ```
  把 `K/frags/global.js` + `K/frags/data.js`（非图对象）直接注入到第一个
  `<script>` 标签
- batch log 在 ~200 字符处截断 command（末尾 `...)`），破坏性 python3 的实际内容
  在 log 取证中是盲区

这种旁路对 kcpos 全盲：
- 不触发 auto-typecalc
- 不过 session_build
- 静态检查无覆盖

### 阶段 4：typecalc 全员退化为 structural waiver

`confirm_object` 共调用 **3 次**，跨 1h32min 累计 **14 次 typecalc_test TestError**：

| # | 时间 | object | TestError 数 | 时间跨度 |
|---|---|---|---|---|
| 1 | 12:11:59 | InitWorld | 1（12:16:19）| ≈4min → Obstacle "impl code truncated 135KB" |
| 2 | 12:18:59 | GenerateWorld | 1（12:22:06）| ≈3min → Obstacle "doesn't expose GenerateWorld function" |
| 3 | 12:25:03 | CombatSystem | **12** | **1h32min**：12:33 / 12:38 / 12:46 / 12:52 / 13:00 / 13:13 / 13:17 / 13:24 / 13:28 / 13:35 / 13:45 / 13:57 |

→ CombatSystem 单对象死磕 1.5 小时，agent 既不 abort 也不换策略，直到 14:01:48 主动放弃。

- 14:01:48 agent thinking："I'll use structural waivers for all objects"
- 14:02:48 `typecalc_waive × 10` 全部 "Canvas/DOM"、"browser-only"、"shared global state"
- 14:03:18 `graph_merge_object status=confirmed × 10`，per-object gate 仅警告

`WaiverKindStructural` 不计入 flood → 10/10 全 structural → 0 pragmatic →
flood=0 → 通过。整条 typecalc 验证链对整个游戏没产生过一行被实际跑过的测试。

### 阶段 5：deliverable 实际状态

| 位置 | 内容 |
|---|---|
| `K/frags/*.js` (12 个) | 0 个 export/import |
| `index.html` 行 3755-3761 | `const exports = {CombatSystem}; globalThis.IMPL.CombatSystem = CombatSystem; export { CombatSystem }; export default exports;` |

注：行 3713 的 `// === CombatSystem for testing ===` 标记是 fragment 标记格式
（`// === <id> (fragment: K/frags/<id>.js) ===`）之外的，是 agent 手工生成的。

行 617-3763 是非 module `<script>`（无 `type="module"`），底部却有 ESM
`export` 语句 → 浏览器 SyntaxError → 黑屏。

那段 `export` 在 transcript 里 0 命中（grep `for testing` / `export default` /
`globalThis.IMPL` 全 0），但 index.html mtime=14:01 表明那一刻有人写过；
**最可能的来源是 12:10:13 那次 `python3 ... open('w')` 命令被 log 截断遮蔽**。

## 验证链漏洞清单（本日志直接证实）

1. Path B trigger 是死字，没接 `session_gate_check`
2. v9.0.6.4 unmodeled-function 正则只匹配 `function Name(`，sed 一行绕过
3. 静态检查无 "ESM export in non-module script" 规则
4. 直接 bash 写文件 → kcpos 全盲
5. structural waiver 是终极逃生口，一句话洗白且不计 flood
6. typecalc_test 持续失败 12 次没有 retry-cap 阻断 → 直接 waiver
7. checkpoint_fill 全自填，无机制让 agent 真打开浏览器
8. batch log 命令截断 ~200 字符，破坏性 bash 在 log 取证里是盲区

最终 `session_status=finished + checkpoint=PASS + graph_validate=PASS`，输出黑屏。
