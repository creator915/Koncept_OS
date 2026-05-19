# PB-kcpos 第一组诚实客观分 — entr 69 / figlet 29 — 2026-05-18

> 不改写任何旧文件。法医见 [`pb-kcpos-FORENSIC-2026-05-18.md`](pb-kcpos-FORENSIC-2026-05-18.md);
> 清算/四轴见 [`pb-kcpos-ACCOUNTING-2026-05-18.md`](pb-kcpos-ACCOUNTING-2026-05-18.md)。
> 本文记录:在防作弊+平台正确条件下,kcpos 在 2 个 PB easy 任务上**第一组真实、可归因的得分**。
>
> 一手证据(日志/transcript/产物/eval.json/submission)已归档:[`../../../tests/0518/`](../../../tests/0518/)
> —— 每任务 `kcpos.log` `setup.log` `transcript.json` `deliverable/` `pb-eval/`。

## 1. 一句话

| 任务 | 分 | error_code | 测试 | 平台硬门 | pb-audit | LLM-EOF |
|---|---|---|---|---|---|---|
| eradman__entr.8e2e8b4 | **69/100** | None | 484✓/200✗/1skip(685) | ✅ ×8 | CLEAN | 无 |
| cmatsuoka__figlet.202a0a8 | **29/100** | None | 380✓/664✗(1044) | ✅ ×17 | CLEAN | 无 |

两个数都不是作弊灌的、不是平台缺陷造的 0、可干净归因到 kcpos 本身。这是这条实验链真正要的东西。

## 2. 流程(每步可复核,非框架)

每任务:从 pristine `:task_cleanroom` linux/amd64 镜像起两容器 —— `pbh-ref-*`(参照,
`/workspace/executable` 为 `---x--x--x root:root`,agent 只能经 `./probe` 触达,读不到字节)
+ `pbh-bld-*`(`--network none` 构建沙箱,compile.sh 物理上 curl 不出去)。host kcpos
`chat --contract blackbox`(CapsBlackbox:无 bash、无 shell 串,仅 compile/run_local/probe
+ 文档读)。`KCPOS_AMD64_CONTAINER` 把 compile/run_local 路由进 linux/amd64 容器。
**axis-2 修复**:两镜像都带 `/workspace/.git`(原始源码史),launcher `rm -rf .git`
并扫无 `*.c` 残留(实测 pre-agent 无源码泄漏)。

完成后三门:① 平台硬门 = transcript 内 `built on linux/amd64 in pbh-bld-*` 标记必须存在
(否则构建跑在 host = 缺陷,作废重跑);② `kcpos pb-audit` transcript(TAINTED⇒分作废);
③ 提交仅 = agent 亲手产出的 `compile.sh + 源码`(不手改、不替它 bundle 任务数据,
与 entr 同规则)→ `programbench eval` 官方 grader(linux/amd64 Docker,固定测试集)。

## 3. entr 69/100 — 强、可信

- 真重建:手写 `entr.c` 12.4 KB + `compile.sh`(`cc -std=c11 … entr.c`),transcript 内
  大量 probe↔run_local 对照迭代(169 probe / 107 run_local 量级),非背诵。
- 产物 `file` = ELF x86-64 GNU/Linux(真 linux 二进制);上次 0/compile_failed 的
  macOS-kqueue 缺陷,本次实测结构性堵死。
- 失败 200 项集中:usage/help 精确措辞、`-r` restart 模式、env-var 信号边界 ——
  黑盒下难逆出的细节,**诚实能力缺口,非作弊**。
- **轴4 干净**:法医证实 entr 模型权重里没有,必须真重建 → 69 反映**能力**非记忆。
- 横向印证:法医里最干净那次 entr(还**带 bash**、防御性双路径)= 71%;本次更严
  (无 shell、无源码可抄、构建断网、`.git` 剥)= 69%。约束更紧、分略降,高度一致 →
  数字扎实。

## 4. figlet 29/100 — 低,但同样诚实可归因

- 干净构建+运行(error_code None,产物 ELF x86-64 静态链接)。
- 单一主因(证据):eval log 内 `Unable to open font file` ×839,占 664 失败的绝大头。
  agent 的 `figlet.c` 把字体目录硬编码为 `/usr/local/share/figlet`、**不读
  `FIGLET_FONTDIR`**;而 PB harness 用 cwd `fonts/` + `FIGLET_FONTDIR=fonts` 供字体
  (`run("-R","-l","-f","standard","RTL")`→`executable: standard: Unable to open font file`;
  `test_info_code_2…` 期望 `/custom/fonts`,agent 输出 `/usr/local/share/figlet`)。
  参照二进制 `strings` 含 `FIGLET_FONTDIR` 与 `/usr/local/share/figlet` 二者。
- **归因(诚实):能力缺口,非打包错。**(a)交付按 entr 同规则忠实打包,entr 同法 69
  分证明打包法成立;(b)PB 对 reference/submission 同 harness 供字体,参照能找到、
  agent 找不到,是因 agent 没用 probe 复现参照的字体解析(任务本就是"probe 对齐黑盒");
  (c)bundle fonts 也无用 —— agent 二进制根本不看 cwd/fonts。
- **轴4 反向警示(重要发现)**:figlet 是知名程序,agent 极可能凭权重记忆直接写下
  figlet 上游"惯例"安装路径 `/usr/local/share/figlet`,而非靠 probe 经验重建实际机制
  —— **记忆在这里是害不是利**。这印证 ACCOUNTING §8 轴4:对著名任务,分不能等同
  能力;此处记忆把分**拉低**(误导实现),与"记忆灌高分"互为镜像。
- agent 自陈 smushing 未完全对上、退化为 kerning —— 与部分重建一致,诚实信号。

## 5. 四轴限定(对外引用必并读)

- 轴1 跨任务状态:干净 ✅(两任务各自全新容器/workdir)。
- 轴2 镜像源码泄漏:本次脚本化剥 `.git` 并核验无 `*.c` ✅(仍是脚本步,应继续审计化)。
- 轴3 compile.sh 抓源码:本次**硬墙** ✅(`--network none` 构建容器);pb-audit 双保险。
- 轴4 记忆污染:环境无解 ❌。**entr 干净;figlet 知名,29 这个数受记忆反向干扰
  (记忆误导 → 拉低),不能纯当能力,但也绝非作弊灌水**。

## 6. 结论

- 目的2(kcpos 在 PB 上的真分,真非虚构):**首次取得** —— entr 69、figlet 29,
  防作弊+平台正确+可干净归因。
- 目的1(验证链按设计工作):本组为 PB-as-run 裸 agent 黑盒模式,**不**走 kcpos
  SPEC/HE 验证链(法医长期立场:PB 不验 kcpos 棕地设计);验证链证据仍在法医对照。
- LLM-流-EOF:本组 2/2 跑全程未发生(历史 4/6 死);未实现 retry,属运气好,
  仍是已知最高价值待办(memory `project_kcpos_llm_retry`)。
