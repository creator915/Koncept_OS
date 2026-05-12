# Terraria 5 并发批次报告（2026-05-11，v9.0.2 binary）

**Binary**: kcpos v9.0.2（未提交，含 P0 FixImpl 真 retry + P1 describe/synthesize prompt 收紧 + P1 SymbolHash 隔离 + P2 bug-D fixture + 与 v9.0.1 A/E/G）
**触发条件**: 5 个 terraria-01..05 目录同时跑 `请实现 SPEC.md 描述的简化版单机泰拉瑞亚游戏。最终产物是 index.html，使用 HTML + Canvas + JavaScript 单文件浏览器实现，不引入外部依赖。`
**SPEC**: `泰拉瑞亚-需求文档.md` 1510 行 24 章 (60 物块 / 35 敌怪 / 90 配方 / 4 主线 Boss / 7 NPC / 9 生物群落)
**结果**: **0/5 finished**，**0 confirm_object 调用**，**0 个对象 confirmed**

---

## 0. 一句话结论

**这 5 个实例没有暴露 kcpos v9.0.2 的 design bug，暴露的是 protocol gap**：当任务的 deliverable 远超单次 LLM 调用承载能力时，kcpos 没有任何机制阻止 agent 走"declare graph → 一把生成完整 impl"的死亡路径。v9.0.2 的 5 项修复**根本来不及触发就死了**。

---

## 1. 五实例终态矩阵

| 实例 | 死因 | log | declared 对象 | confirmed | 持续时长 | kill 时点 agent 在做 |
|---|---|---|---|---|---|---|
| terraria-01 | `context deadline exceeded` | 28K | 7 | 0/7 | ~5min | 决定"在一次 write_file 里写完整个 index.html (~4000-8000 行)" |
| terraria-02 | `context deadline exceeded` | 36K | **19** | 0/19 | ~8min | 同上，agent 已 deliberate "3000-5000 line" 60s 后死 |
| terraria-03 | `context deadline exceeded` | 24K | 7 | 0/7 | ~5min | 显式宣布 "in one go — all systems integrated" 然后死 |
| **terraria-04** | **`unexpected EOF`** | **24K** | 7 | 0/7 | **~5min, 最早死** | 仍在 thinking, 还没发 write_file. **TCP server-side close, 非超时** |
| **terraria-05** | `context deadline exceeded` | **30K** | 8 | 0/8 | **~19min, 最晚死** | **唯一尝试 chunking 策略**: 第一次 write_file 42KB 成功 → 第二次 edit 追加 10KB **fatal** |

**总计**: 5/5 死, 0 confirm_object, 24 declared objects 全部停在 declared 阶段。SPEC 设计阶段 (read SPEC → 决定 architecture → 声明对象 → 链 edges → graph_validate) **全部 5 实例通过**，证明 v9.0.x design 阶段健康。

---

## 2. 共同结构（前 5 分钟 5/5 全跑通）

所有实例都完成了:
1. 读 SPEC.md (1510 行)
2. 设计 architecture (sub-modules + intermediate variables)
3. 声明 7-19 个对象 + 6-10 个属性
4. 写 14-29 个 def.ts 文件
5. 链 produces/consumes/mutates 边
6. graph_validate **通过**

然后**全部**死在第一次试图实现 index.html 的瞬间。**graph 模型本身没暴露问题**，问题集中在 impl 生成阶段。

---

## 3. 三种死法（同根不同表）

### 3.1 `context deadline exceeded` (01/02/03/05)

**发生**: HTTP client 30s 超时。agent 发起一次"单 write_file 写整 index.html"的调用，**响应流**在 30s 内未完成。

**为什么 30s 不够**: DeepSeek 输出 token 速度 ~50 tokens/s，4000-8000 行 JS = ~20K-40K tokens = ~400-800s。30s 客户端超时**结构性**不够。

### 3.2 `unexpected EOF` (04)

**发生**: TCP socket 被 DeepSeek **服务端**先关。这不是客户端超时，是上游主动断流。

**可能原因**: 5 并发同时塞 ~50K token prompt + 长生成 → 服务端检测到 abnormal pattern / rate limit 主动杀连接。

### 3.3 chunking 也救不了 (05)

terraria-05 是唯一**显式选择 chunking** 的实例:
- 20:29:47 第一次 write_file 写 42KB **成功** (HTML head + tile defs + enemy defs + NPC defs)
- 20:32:10 第二次 edit 试图追加 10KB game logic — **fatal**

**为什么 chunking 也死**: Agent 05 的 forensics 揭示了真根因——

> **每次 `edit` 调用, LLM 都要在响应里 "看见" 完整文件来确认 old_string 匹配**。第二次 edit 时上下文已累积:
> - transcript (~50K+ tokens)  
> - 完整 index.html (42KB ≈ 10K tokens, 因为 edit 要 verify old_string)  
> - 新 chunk (10K bytes)  
> - system prompt (~20K tokens)
>
> 累积 context **结构性膨胀**，**chunking 减小单 chunk 大小，但累积上下文仍线性增长**。

---

## 4. v9.0.2 5 项修复的命中情况

| 修复 | 触发? | 原因 |
|---|---|---|
| F port-observation-orphan-key | ❌ 没机会 | 没人跑到 typecalc_compile/review |
| G waiver kind | ❌ 没机会 | 没人跑到 typecalc_waive |
| E chain Obstacle 落盘 | ❌ 没机会 | 没人跑到 confirm_object |
| A implSymbol allowlist | ❌ 没机会 | 没人 graph_merge_object implSymbol |
| **P0 FixImpl 真 retry** | ❌ 没机会 | 没人跑到 confirm_object |
| C SymbolHash 隔离 | ❌ 没机会 | 没人 typecalc_compile |
| describe prompt 收紧 (4.2) | ❌ 没机会 | 没人 typecalc_describe |
| synthesize 强制 between (4.4) | ❌ 没机会 | 没人 typecalc_synthesize_tests |

**0/8 v9.0.2 修复在本批次被触发**。这个 batch 对 v9.0.2 设计**无信息量**——既不能证伪也不能证实。

---

## 5. 真正的失败链 (synthesis from 5 forensics)

**Layer 1 — Protocol gap**: kcpos system prompt **没有任何指引**告诉 agent "当 spec 超过 N 行或 estimated impl 超过 N 行时, 不要尝试 single-shot write_file"。
- 反而 v9.0.2 protocol 里写 "single-file HTML projects no longer trigger spec-stale on EVERY object" (本意是 SymbolHash 隔离的收益), agent 可能误解为"single-file 是受鼓励的"。

**Layer 2 — Agent 决策方差**: 同一份 SPEC + 同一份 system prompt → 4/5 决策"single-shot", 1/5 决策"chunked"。这 80%/20% 的死亡分布是 LLM 决策随机性，不是 agent 无能。

**Layer 3 — Workflow 工具语义**: `edit` 工具内置 "verify old_string match" 把整文件灌进 LLM context，**使 chunking 失去本应的效果**。
- 这是 tool design 选择，不是 LLM 限制。

**Layer 4 — Streaming client 不容错**: kcpos `internal/llm/client.go` 流断开就直接 process exit, 无 retry, 无 transcript 保留, 无 `--resume latest` 接续能力。
- 这是 robustness 短板。

---

## 6. v9.0.3 优先级清单 (合并 5 份 forensics 结论)

| 优先级 | 修复 | 救活哪些实例 |
|---|---|---|
| **P0** | **system prompt 增加 "大任务必须 spawn_subagent" 强引导** — 当 SPEC ≥ 1000 行 OR estimated impl ≥ 2000 行时，protocol 明确"**fresh-context-per-object via spawn_subagent**, 父 agent 仅做 graph + concat"。同时 system prompt 删除 "single-file is fine" 这类被误解的话术 | 01/02/03/05 (单 agent 单 context 路径死亡) |
| **P1** | **LLM 流重试 + `--resume latest` 接续** (见 `project_kcpos_llm_retry.md` memory) — `internal/llm/client.go` 流接收循环加 max=3 指数退避 + 流中断时保留 transcript + 顶层接 SIGPIPE | 04 (server-side EOF 瞬断) |
| **P1** | **context budget telemetry** — kcpos 在每次 LLM 调用前/后估算 prompt token + response token, log 一行 `[context-budget] X/128K used`。**不改变行为, 只让 agent 看到自己快撑爆**, 在死前有机会自我截胡 | 给所有实例可见性, 不直接救 |
| **P2** | **共享 impl 阈值告警** — ≥3 个对象共享同一 impl 时, graph_merge_object hook 触发结构性提示: "shared-impl detected, consider spawn_subagent per object". 阻止"19 个对象塞进一个 index.html" 这种模式悄悄通过 | 04 (19 个共享 impl) + 间接救其他 |
| **P2** | **chunked write 工具支持** — write_file/edit 加 `--append-only` 模式, 用流式追加而非 read-rewrite, 让 edit 不必把整文件灌回 LLM | terraria-05 的 chunking 策略真正有效化 |

---

## 7. 对 batch 验证策略的含义

**Terraria SPEC 在当前 kcpos 下不可测**：长上下文 + 期望大单文件 deliverable 这两条同时存在时, agent 100% 走"design → 一把生成"的死亡路径，v9.0.2 的修复一项都触发不了。

**v9.0.2 修复想被验证, 推荐路径**:
- **(a) 先做 P1 LLM 流重试** — 拯救 server-side EOF type 的失败 (即 terraria-04 一类)
- **(b) 然后用 pong 或 forest 规模任务 (200-500 行 SPEC) 跑 batch** — 这种规模 agent 一次能写完 impl, 进入 typecalc 链, v9.0.2 各项修复才能被触发
- **(c) 暂时不要拿 Terraria 做 v9.0.x 验证** — 直到 v9.0.3 P0 spawn_subagent 引导落地, 否则就是 5 份"agent 死在写文件前"的重复日志

---

## 8. 附录: 5 实例代价量化

```
实例          log     declared  cfm  cc  死法                            partial 产出
terraria-01   28K     7         0    0   context deadline exceeded       0 lines code
terraria-02   36K     19        0    0   context deadline exceeded       0 lines code  
terraria-03   24K     7         0    0   context deadline exceeded       0 lines code
terraria-04   24K     7         0    0   unexpected EOF (server-side)    0 lines code
terraria-05   30K     8         0    0   context deadline exceeded       42KB partial index.html (truncated mid-world-gen, browser parse fail)
```

唯一有部分 impl 产出的是 terraria-05 的 42KB index.html — 但截断在 dungeon generation 循环中间, 缺 `</script>/</body>/</html>`, 浏览器打开即语法错。**不可执行, 不可救**。

---

## 9. 总评

**Terraria batch 不该被算成 v9.0.2 的判决**——v9.0.2 修复一项都没触发到。它真正说明的是: **kcpos 当前 protocol 没教 agent 如何处理"超大 deliverable"，工具语义又把唯一可行的 chunking 策略给抵消了**。

**最有价值的产出**: 这个 batch 第一次清晰暴露了 4 层失败链 (protocol gap → agent 决策方差 → tool 语义 → client 鲁棒性), 给 v9.0.3 提供了明确优先级。

**建议落地顺序**:
1. P1 LLM 流重试 (memory 已记 `project_kcpos_llm_retry.md`)
2. **暂用 forest/pong 中等规模 batch 跑 v9.0.2 真验证**
3. P0 spawn_subagent 强引导落地后, 再用 Terraria 重测
