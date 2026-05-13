# CHECKPOINT.md — 检查点流程约束

> 本文件是 coding agent（Claude Code / Codex）在执行 KonceptOS 模拟流程时的**强制检查点协议**。
> 目标：在前期就把"完成的标准"写死成一份可机械核对的清单，到尾声逐项填证据，避免"自我感觉良好就 finished"。

---

## 0. 总原则

- **前期立卷**：根 session 进入 `active` 状态后、设计任何子签名**之前**，必须创建 `K/checkpoint.json`，列出本次项目所有验收检查项。
- **中途只读**：项目执行期间，checkpoint.json **不允许新增/删除检查项**（避免事后改卷）。如果发现遗漏，必须先回滚到根 session，再重立卷。
- **尾声逐项填证**：根 session 在标记 `finished` 之前，必须为 checkpoint.json 中**每一项**填入：
  1. `codeProof`：代码证明（文件路径 + 行号 + 关键引用）
  2. `verifiedAt`：验证完成时间戳（ISO8601）
- **零容忍**：任意 `must` 项的 `codeProof` 为空 / "TODO" / "稍后补" → **整个项目视为不及格**，根 session 不允许 finished。
- **v8.8 协议简化**：早期版本要求第二证据 `gameplayProof`（截图/录屏）。v8.7 5 并发验证显示该要求在 CLI agent 环境下不可能可靠满足，且其代偿（描述性占位）反而稀释证据质量。v8.8 完全删除 `gameplayProof`。运行时证据采集若有需要，将以独立子系统 `kcpos snap`（v8.9+ 规划）提供，与 checkpoint 解耦。

---

## 1. checkpoint.json 文件位置与命名

```
K/
├── graph.json
├── checkpoint.json          ← 本协议管控的文件
└── sessions/
    └── ...
```

- 路径固定为 `K/checkpoint.json`
- 项目从立卷到验收**只允许存在一份**
- 提交时必须与 graph.json、sessions/ 一起进入版本控制

---

## 2. checkpoint.json Schema

```jsonc
{
  "version": 1,
  "projectName": "<项目代号>",
  "createdAt": "<ISO8601>",
  "createdBy": "<根 session ID>",
  "frozen": false,                // 立卷完成置 true，从此只允许填证据，不允许改条目
  "frozenAt": null,
  "items": [
    {
      "id": "CHK-001",            // 全局唯一，立卷时分配，永不重排
      "category": "<分类>",        // 见 §3
      "title": "<一句话检查项>",
      "description": "<具体可验证的判定标准>",
      "severity": "must" ,        // must（必过）| should（建议过，可豁免但需理由）
      "codeProof": null,          // 立卷时为 null；尾声填写
      "verifiedAt": null,
      "waiver": null              // 仅 should 项可填，结构见 §5
    }
  ],
  "summary": {                    // 验收阶段填写
    "totalItems": 0,
    "passed": 0,
    "waived": 0,
    "failed": 0,
    "finalVerdict": null          // "PASS" | "FAIL"，根 session finished 前填写
  }
}
```

---

## 3. 立卷阶段：检查项分类（仅列出"该写什么类别的项"，不写具体项）

立卷时必须为以下每个类别至少写出 1 条具体检查项。具体内容由根 session 根据当前项目的需求文档生成（例如对于本仓库即根据 [泰拉瑞亚-需求文档.md](泰拉瑞亚-需求文档.md) 拆出来）。

立卷时必须为以下每个类别**至少**写出对应数量的 must 项（`severity: "must"`，禁止降级为 should）。`minMust` 列是硬下限，立卷阶段如果某类别条目数 < `minMust` → 整个 checkpoint 视为不合规，禁止 `frozen: true`。

| # | 类别 | minMust | 立卷要求 |
|---|------|---------|---------|
| A | **核心数据结构完整性** | 4 | 需求文档中定义的每个数据结构（如 World / Tile / Player / Entity / Item / Recipe）各 1 条 |
| B | **世界生成正确性** | 10 | 世界尺寸、层级、群落布局（每群落 1 条）、矿石密度（每矿种 1 条）、特殊地形（漂浮岛/金字塔/地牢/腐化深井）每项 1 条 |
| C | **地图边界与距离映射** | 6 | §7.6 中"走多远遇到什么"的每个距离阈值至少 1 条（含出生点保护半径、世界边界、海洋触发距离） |
| D | **物理与碰撞** | 5 | 重力、跳跃、AABB、平台穿越、踏台阶各 1 条 |
| E | **玩家系统** | 5 | 创建、属性、状态机、死亡复活、HP/MP 恢复各 1 条 |
| F | **物品/背包/制作** | 6 | 堆叠、拖动、工作站靠近判定、配方表覆盖率（≥3 条具体配方 ID） |
| G | **战斗系统** | 5 | 伤害公式、Buff 生效、Debuff 生效、抛射物特例、击退各 1 条 |
| H | **敌怪与 AI** | 5 | 每种 AI 模式 1 条 + 敌怪刷新规则 1 条 + 掉落规则 1 条 |
| I | **Boss 系统** | 5 | **每个 Boss（史莱姆王/克苏鲁之眼/世界吞噬怪/骷髅王/血肉墙）至少 1 条召唤+阶段+掉落** |
| J | **NPC 与房屋** | 3 | 入住条件、商店、房屋判定各 1 条 |
| K | **生物群落** | 8 | 每个群落（森林/沙漠/雪原/丛林/海洋/腐化/地牢/地狱）至少 1 条判定 + BGM + 敌怪池 + 特殊效果 |
| L | **事件系统** | 3 | 日夜循环、血月、陨石坠落各 1 条 |
| M | **UI/HUD** | 5 | HUD、主菜单、背包面板、大地图、暂停菜单各 1 条 |
| N | **存档与崩溃恢复** | 4 | 存档目录、.char 写入与读出、.wld 写入与读出、自动存档、崩溃恢复各 1 条 |
| O | **音效与音乐** | 2 | 音效列表关键事件触发 + BGM 切换 |
| P | **进度推进路径** | 8 | §21 中每个阶段（第 0/1–3/4–7/8–14/15–20/21–30/31–40/41–50 天）至少 1 条"该阶段标志性玩法可达成" |
| Q | **性能与稳定性** | 3 | FPS、内存、崩溃率各 1 条 |
| R | **测试覆盖** | 3 | 单元测试通过、集成测试通过、玩法验证脚本通过各 1 条 |
| S | **验收标准条款** | n | §24 中每条验收标准都拆为独立检查项（n = 该章条数） |

**总下限**：所有 minMust 之和 ≈ 90+（不含 S 类的 n），即 checkpoint.json 的 must 项**至少 90 条**才能 frozen。**禁止只写 50 条粗项交差**——上次 65% 交付率的根因之一就是颗粒度过粗。

**立卷时禁止写**："实现完整"、"功能正确"、"运行正常" 这类不可机械验证的项。每条 `description` 必须给出可观察的判定方式。

**立卷阶段强制校验脚本**（建议立卷完成后先跑一遍）：

```js
// 跑在 frozen 之前
const c = require('./K/checkpoint.json');
const minMust = { A:4, B:10, C:6, D:5, E:5, F:6, G:5, H:5, I:5, J:3, K:8, L:3, M:5, N:4, O:2, P:8, Q:3, R:3 };
const counts = {};
for (const i of (c.items || [])) {
  if (i.severity !== 'must') continue;
  const cat = i.category;
  counts[cat] = (counts[cat] || 0) + 1;
}
const fail = [];
for (const [cat, n] of Object.entries(minMust)) {
  if ((counts[cat] || 0) < n) fail.push(`${cat}: ${counts[cat]||0} < ${n}`);
}
console.log(fail.length ? 'INSUFFICIENT: ' + fail.join('; ') : 'OK: all categories meet minMust');
```

立卷不达标 → 必须先补条目再 `frozen: true`，否则审计 agent 会判定整个 checkpoint 为伪 PASS 直接 FAIL。

---

## 4. 尾声阶段：逐项填证

根 session 收到所有子 session `finished` 后，进入"验收循环"：

```
for each item in checkpoint.items:
  1. 重新阅读 item.description
  2. 在代码中找到实际承载该需求的实现位置
  3. 填 codeProof：
     - 文件路径列表（每条形如 "src/foo.impl.ts:42-58"）
     - 关键函数/常量名
     - 必要时附实现摘要 ≤ 3 行
  4. 填 verifiedAt
  5. 若不通过 → 不允许填假证据；走 §5 决策
```

### 4.1 codeProof 格式

```jsonc
{
  "files": [
    {"path": "src/UpdateVelocity.impl.ts", "lines": "12-30", "reason": "重力施加在此"},
    {"path": "K/defs/velocity.ts", "lines": "1-8", "reason": "签名定义"}
  ],
  "summary": "updateVelocity 在 X 轴施加 0.5/帧 加速、Y 轴施加 0.4/帧 重力，符合 §8.1"
}
```

> v8.8: `gameplayProof` 字段已删除（详见 §0 协议简化说明）。所有 must 项的"是否真的工作"验证由 typecalc 链（test → review）承担，checkpoint 仅记录代码层映射。

---

## 5. 不通过的处理

| 严重度 | 不通过处理 |
|-------|----------|
| `must` | **禁止豁免**。回滚相关子 session（参见 CLAUDE.md §5.3），重新实现，再次走验收循环 |
| `should` | 可填 `waiver` 字段豁免，但必须给出 `reason` + `riskAccepted: true` + `requirementRef`；豁免计入 `summary.waived` |

```jsonc
"waiver": {
  "reason": "音效列表的 5 项装饰音效在当前测试机上无音频设备，无法验证，已用单元测试间接覆盖",
  "riskAccepted": true,
  "decidedBy": "s_root",
  "decidedAt": "2026-04-29T18:23:00Z",
  "requirementRef": "需求文档.md §20.1 行 1280-1294"
}
```

### 5.1 豁免上限红线（防"伪 PASS"）

历史上出现过将整章功能（Boss / NPC / 存档 / 音效）批量降级为 `should` 然后用 waiver 跳过的"伪 PASS"。为堵这个洞，立卷与验收两端都要加约束：

**立卷阶段**：

1. 凡是对应需求文档**整章/整节级别**功能的检查项，`severity` 必须 = `must`。例如：
   - 整章 §14 Boss 系统 → 至少 5 个 must 项（每个 Boss 1 条）
   - 整章 §15 NPC 系统 → 至少 3 个 must 项（入住 / 商店 / 房屋判定）
   - 整章 §19 存档 → 至少 2 个 must 项（写入 / 读出）
   - 整章 §20 音效 → 至少 1 个 must 项（关键事件触发音效）
2. 立卷时每个 `should` 项必须填 `requirementRef`，明确指向需求文档具体行/节，便于反向追溯。
3. 立卷完成后，根 session 必须列出所有 `should` 项的降级理由——降级理由**不能仅基于"实现成本高"或"core gameplay is fully playable"**，必须有具体技术约束（如外部 API 不可用、平台不支持）。

**验收阶段**：

4. **豁免比率上限**：`summary.waived / summary.totalItems ≤ 0.10`（10%）。超过该比率 → `finalVerdict` 强制 = `FAIL`，无视其它项是否填齐。
5. **章节级 must 不可豁免**：任何由立卷阶段第 1 条规则强制为 must 的项，若缺 `codeProof`，整体强制 FAIL。
6. **重复理由侦测**：如果 ≥ 3 个 waiver 共享同一 `reason` 模式（例如都写 "not implemented in current version"），视同**批量跳过**，强制 FAIL，要求拆分为各自独立的具体理由再审。

### 5.2 验证脚本（建议在审计 agent 中运行）

```js
// 简化判定逻辑，可塞进审计 agent 的 Bash 工具调用里
const c = require('./K/checkpoint.json');
const items = c.items || [];
const waived = items.filter(i => i.waiver);
const ratio = waived.length / Math.max(items.length, 1);

const reasons = {};
for (const i of waived) {
  const key = (i.waiver?.reason || '').toLowerCase().slice(0, 40);
  reasons[key] = (reasons[key] || 0) + 1;
}
const dupReason = Math.max(0, ...Object.values(reasons));

const verdict =
  ratio > 0.10                                            ? 'FAIL: waiver ratio > 10%' :
  dupReason >= 3                                          ? 'FAIL: 3+ waivers share same reason' :
  items.some(i => i.severity==='must' && !i.codeProof)    ? 'FAIL: must item missing codeProof' :
  c.summary?.finalVerdict === 'PASS'                       ? 'PASS' : 'FAIL: finalVerdict not PASS';

console.log(verdict, '|', 'waived:', waived.length, '/', items.length, '|', 'top dup reason:', dupReason);
```

---

## 6. 与 CLAUDE.md 工作流的衔接

修改/扩展 CLAUDE.md 中的相应阶段，使本协议生效：

- **§6.1 启动**：第 4 步"创建 s_root.json"之后，**新增第 4.5 步**——立 `K/checkpoint.json` 卷，并将 `frozen` 置 true。
- **§5.4 路径 B 的第 10 步**（父 session 标记 finished 前）：在"完成 §6.5 集成验证"之后，**新增**——根 session 必须额外完成本协议 §4 的逐项填证。
- **§6.5 集成验证**：在原步骤 3 之后**新增**步骤 4——比对 `checkpoint.json` 全部项已填证据且 `summary.finalVerdict === "PASS"`。
- **§24 验收标准**：每条都对应到 checkpoint.json 中至少一项，反向追溯。

---

## 7. 防作弊红线

下列行为视同**整体不及格**，根 session 必须回滚至立卷前：

1. 在 `frozen: true` 之后修改 `items` 的 `id` / `title` / `description` / `severity`
2. `codeProof.files` 指向不存在的文件或行号
3. 同一段代码被 ≥ 3 个不同检查项复用作为唯一证据
4. `summary.finalVerdict = "PASS"` 但仍存在 `must` 项的 `codeProof` 为 null

---

## 8. 验收最终判定

```
if any item.severity == "must" and item.codeProof == null:
    finalVerdict = "FAIL"
elif any item.severity == "must" and item.waiver != null:
    finalVerdict = "FAIL"   # must 不允许豁免
else:
    finalVerdict = "PASS"
```

只有 `finalVerdict == "PASS"` 时，根 session 才允许 `status` → `finished`，项目才算交付。

---

## 附录 A：checkpoint.json 立卷期空模板

```json
{
  "version": 1,
  "projectName": "",
  "createdAt": "",
  "createdBy": "",
  "frozen": false,
  "frozenAt": null,
  "items": [],
  "summary": {
    "totalItems": 0,
    "passed": 0,
    "waived": 0,
    "failed": 0,
    "finalVerdict": null
  }
}
```

## 附录 B：单条 item 立卷期模板

```json
{
  "id": "CHK-XXX",
  "category": "",
  "title": "",
  "description": "",
  "severity": "must",
  "codeProof": null,
  "verifiedAt": null,
  "waiver": null
}
```
