# 真 `kcpos` 命令在 ProgramBench-class 任务上的名副其实测试 — 2026-05-17

本报告记录"用真 `kcpos chat` 命令、盲重建黑盒、`programbench eval` 评分"这条**名副其实**路径的实测,以及到此为止的全部诚实更正。**不是 ProgramBench 官方跑分**(无 200 题全量、无 leaderboard 提交);是本机 Docker(amd64 模拟)上的 PB-class 实测。

## 0. 路径与更正(诚实优先)

- **gold 基线(管线/模拟可信度)**:cmatrix 用上游真源码 → `programbench eval` **768/769 = 99.9% ✅ solved**。证明 PB pipeline + 本机 amd64 模拟**可信**(模拟未引入虚假失败)。
- **作废:`pb-solve` 薄壳(cmatrix 曾报 81)**。该路径**完全绕开 kcpos**(不走验证链/router/session,直接裸 `transport.Chat()` + docker)。"kcpos 81" 是**错误标注**,不计入。已向用户说明。
- **本报告的真测**:真 `kcpos chat` 命令,盲跑(只 README/man + `./probe` 探黑盒,无源码),走真验证链 + subsession,**全程产可核验 transcript**(`.kcpos/transcripts/*.json`)。这才名副其实。

## 1. 实测结果(真 `kcpos chat`,名副其实)

| 题 | 语言(kcpos 自选) | eval | 名副其实分 | 说明 |
|---|---|---|---|---|
| cmatrix | C | ec=None | **756/769 = 98%** | 著名程序,**记忆污染**;CLI 表面浅(动画不可行为测) → 乐观上界 |
| jp2a | C | ec=None(**编译成功**) | **121/714 = 17%** | jpeg→ascii 真图像逻辑,记忆扛不动 → **更接近真实水平** |
| entr | C | 用户中途停止 eval(2/4) | 未评 | submission 已产(entr.c+compile.sh) |
| tty-clock | C | 用户中途停止 eval | 未评 | submission 已产 |
| figlet | Go | — | **非收敛失败** | kcpos agent `exceeded max iterations (150)`,选 Go 重建 C 工具,死磕未果 |

真实耗时(per 题 kcpos chat):cmatrix ~17min、jp2a/entr ~41min、tty-clock ~44min;5 路并发墙钟 ~45min。

## 2. 诚实结论

1. **名副其实达成**:cmatrix 98% / jp2a 17% 是**真 kcpos 验证链 + subsession** 跑出来的,transcript 可逐步核验,不是自报、不是薄壳。
2. **别被 98% 骗**:cmatrix 著名+可记忆+CLI 表面浅,98% 是**记忆污染的乐观上界**。**jp2a 17%(真逻辑、记忆扛不动)才是非记忆任务上的真实信号**,与 PB frontier 3%、kcpos 非重建-agent 的预期一致。
3. **真实失败模式暴露**:figlet —— agent 撞 150 轮迭代上限不收敛(还自选 Go 撞上 C 构建环境)。这是 kcpos 链被拿去做"无 spec 整程序重建"时的真实表现。
4. **范式结论未变**:PB 考绿地黑盒重建,**不验证《屎山》棕地设计**。本测量的是"kcpos 验证链被挪用于重建"的能力,名副其实但低,符合预期。
5. **我的一处预测错了(认)**:曾断言 jp2a 会因 compile.sh 的 `/opt/homebrew` 路径编译失败=0。**错。** ec=None,编译成功;17% 是重建质量,不是构建失败。

## 3. 产物与可核验性

- canonical 结构:`tests/pb-kcpos/<instanceID>/work/`(源码+compile.sh+输入文档+probe)
- 真 transcript:`tests/pb-kcpos/<id>/work/.kcpos/transcripts/*.json` + `tests/.batch-logs/pbk-cmd-<id>.log`
- eval 结果:`/tmp/pbk-cmd-eval/<id>/<id>.eval.json`(cmatrix/jp2a 已出,保留未删)
- gold 对照:cmatrix 768/769 ✅

## 4. 未尽 / 下一步(若继续)

- entr/tty-clock eval 被用户中途停止,submission 已在,可补评。
- figlet 非收敛:要么提高 iteration 上限,要么约束 agent 用目标语言(task.yaml 的 language)。
- 真实 per-题 ~40min(easy C),28 题 5 路并发 ≈ 4–5h execute + 数小时 eval;全 201 题本机不现实。
- 仍不建议把 PB 分当 kcpos / 棕地设计的成败依据——它是绿地重建的旁路测量。
</content>
