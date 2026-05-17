# PB-class batch-2 + combined verdict — 2026-05-17

批次2:5 个全新、**不剧透**怪癖、覆盖 重构/修bug/优化/重构去重/加功能,专门设计来诱导 LLM 顺手"清理"怪癖(自然诱导,不强制注入)。

## 批次2 原始结果

| fixture | 改动类型 | locked | LLM改 | kcpos说改坏? | preserve | accept | 判定 |
|---|---|---|---|---|---|---|---|
| pb-temps | 重构 | 21 | 是 | 否 | ✅ | ✅ | true-allow ✓ |
| pb-page | 修bug | 7 | 是 | 否 | ✅ | ✅ | true-allow ✓ |
| pb-csv | 优化 | 4 | 是 | 否 | ✅ | ✅ | true-allow ✓ |
| pb-money | 重构去重 | 0 | — | — | ❌ | ❌ | **harness ERR** |
| pb-user | 加功能 | 6 | 是 | 否 | ✅ | ✅ | true-allow ✓ |

## 两批合并(n=10)

**true-allow ×8 / true-catch ×0 / MISS ×0 / false-alarm ×0 / harness-error ×2**(synth 罢工:pb-range、pb-money)。

## 诚实结论

1. **设计核心论点(门禁能否"抓"住真回归)端到端仍 0 验证。** 跨 10 个不同 fixture、两批、批次2 还专门用重构/优化/修bug 任务+不剧透来**诱导**改坏——DeepSeek 依然把每个能保的怪癖都保住了。0 true-catch、0 MISS。**"靠 LLM 自然犯错来产生测试用例"这条路,已用 10 题实证证伪。** 想验证"抓"路径,必须上 forced-break 对照(确定性已知改坏的修改,不赌 LLM 犯错)——这不再是可选项。

2. **被验证的(仍是容易那半)**:扣掉 2 个 harness 错,8/8 恢复出的锁**零误报**,且与独立隐藏 oracle **8/8 吻合**,跨 8 个不同怪癖函数 + 4 类 Feathers 改动。证明:characterization 锁是忠实、不噪、不过度约束的代理,正确棕地改动不会被误杀。真结果,但不是卖点。

3. **可复现缺陷(最实)**:2/10(pb-range `parse_range`、pb-money `format_money`)synthesizer 返回无结构化用例 → characterize 第一步就死 → 无锁。**20% 在 step1 硬失败**。两个都是极简函数,与复杂度无关——是 synth 路径对某些函数 `CANNOT_SYNTHESIZE`/testCode 回退。需 fallback(重试/raw-testCode/换 probe 策略)。ITERATION 级、优先级高。

4. **方法学发现**:我两批 fixture 都想制造 break,都没造出来。说明小型单函数任务上 LLM 行为太保守,不足以触发门禁"抓"路径。forced-break 现在是**必需**,不是补充。

## 不报

- 不报 PB 分数(非 benchmark;2/10 harness 挂)。
- 不声称门禁有效——10 题 0 次"抓",catch 能力仅单测证据,端到端零证据。
- 8/8 true-allow 不等于成功,只等于"不误报"。

## 下一步(有实证依据的优先级)

1. **forced-break 对照**(P0):harness 注入已知改坏怪癖的确定性修改,直接打 catch 路径。这是唯一能端到端验证核心论点的办法。
2. **修 synth 罢工**(P0):pb-range/pb-money 复现,20% 硬失败,characterize 鲁棒性硬伤。
3. 更难/更大任务或更激进模型(P2):若要继续走"自然诱导"路线。
