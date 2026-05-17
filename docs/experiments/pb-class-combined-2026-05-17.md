# PB-class 棕地闭环 — 两批合并报告 — 2026-05-17

合并 batch-1(pb-discount/slugify/fee/range/retry)+ batch-2(pb-temps/page/csv/money/user)。**写本报告前已逐题读过 10 个执行日志**(隐藏 oracle 的 pytest 实际输出 + synth 失败串),结论据此修正,不只看 JSON 汇总。

> 这不是 ProgramBench。本地无 200 题数据集;这是 10 个自构 PB 形态任务的模拟。完整性:`_oracle/` 从不进任何 LLM prompt;改动步骤只喂 `TASK.md`+legacy 代码。

## 1. 合并结果

| # | fixture | 批 | 改动类型 | locked | 判定 | preserve/accept pytest |
|---|---|---|---|---|---|---|
| 1 | pb-discount | 1 | 加功能 | 9 | true-allow ✓ | 3p / 3p |
| 2 | pb-slugify | 1 | 加功能 | 14 | true-allow ✓ | 3p / 2p |
| 3 | pb-fee | 1 | 加功能 | 5 | true-allow ✓ | 3p / 2p |
| 4 | pb-range | 1 | 加功能 | 0 | **HARNESS-ERR** | — |
| 5 | pb-retry | 1 | 加功能 | 6 | true-allow ✓ | 3p / 2p |
| 6 | pb-temps | 2 | 重构 | 21 | true-allow ✓ | 3p / 1p |
| 7 | pb-page | 2 | 修bug | 7 | true-allow ✓ | 2p / 2p |
| 8 | pb-csv | 2 | 优化 | 4 | true-allow ✓ | 3p / 1p |
| 9 | pb-money | 2 | 重构去重 | 0 | **HARNESS-ERR** | — |
| 10 | pb-user | 2 | 加功能 | 6 | true-allow ✓ | 3p / 2p |

**门禁混淆矩阵(n=10):true-allow ×8 / true-catch ×0 / MISS ×0 / false-alarm ×0 / harness-error ×2。**

## 2. 读日志才看得到的事实(修正 JSON 汇总)

1. **8 个 true-allow 是实的,不是假阳性。** 每题 pytest `N passed` 的 N 与 oracle 文件里的测试函数数**逐一吻合**(pb-temps/csv accept=1p ↔ 我只写 1 个函数;pb-page preserve=2p ↔ 2 个函数)。不存在"0 collected 退 0"的伪过关。harness 以 pytest exit 0 判过关,被实际输出佐证。
2. **2 个 synth 罢工的日志不足以定位根因。** pb-range/pb-money 仅有 `synthesizer produced no structured cases` 一句,**原始 synth 回复没落任何日志**——查不出是 `CANNOT_SYNTHESIZE` / JSON 坏 / testCode-only。这是**可观测性缺口**,修 synth 前必须先补这段日志。
3. **锁厚度 4–21,JSON 汇总掩盖了"薄网"。** csv=4、fee=5、retry/user=6 是薄锁:与隐藏 oracle 吻合不假,但薄锁既是更弱的"未过度约束"证据,也是更弱的回归网。"忠实代理"这一结论对厚锁(temps=21、slugify=14)成立度高,对薄锁打折。
4. **非确定性守卫整批 0 触发**(10 个 fixture 全确定性)。Part 3.2 守卫这两批端到端零验证,仅单测。

## 3. 诚实结论

- **设计核心论点(门禁能否"抓"住真回归)端到端仍 0 验证。** 10 题、两批、batch-2 专门用 重构/优化/修bug + 不剧透**诱导**改坏,DeepSeek 一个怪癖没改坏 → 0 true-catch、0 MISS。**"靠 LLM 自然犯错产生测试样本"已被 10 题实证证伪。** catch 能力只有单测证据,端到端零证据。
- **被验证的仅是容易那半**:8/8 锁零误报、与独立隐藏 oracle 8/8 吻合,跨 8 个怪癖 + 4 类 Feathers 改动;但对薄锁(§2.3)证据强度打折。**8/8 ≠ 成功,= 不误报。**
- **可复现硬伤**:2/10 characterize 第一步 synth 罢工(pb-range `parse_range`、pb-money `format_money`,均极简函数,与复杂度无关)→ 20% step1 失败。

## 4. 不报

- 不报 PB 分数(非 benchmark;2/10 harness 挂)。
- 不声称门禁有效(0 次端到端"抓")。
- 不把 8/8 当成功。

## 5. 下一步(实证优先级)

1. **P0 forced-break 对照**:注入确定性已知改坏怪癖的修改,唯一能端到端验证 catch 路径的办法(自然诱导路线已 10 题证伪)。
2. **P0 synth 罢工**:先补 raw-synth-reply 日志(§2.2)定位根因,再加 fallback(重试 / testCode 模式 / 换 probe 策略)。
3. **P1**:厚锁化(薄锁 4–6 的回归网太弱);非确定性守卫的端到端用例;覆盖更难任务以让自然诱导路线可能产出 true-catch。

