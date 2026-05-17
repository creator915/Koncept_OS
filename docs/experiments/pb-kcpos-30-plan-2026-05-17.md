# 计划:kcpos 在 30 道真 ProgramBench 题上的真分 — 2026-05-17

目标:得到 kcpos(基建 + characterize 探黑盒 + LLM 重建循环)在 30 道真 PB
任务上的**真实分数**,本机 Docker(amd64 模拟,已实证可行)。

## 0. 诚实预期(informed consent)

- PB frontier ≈ 3% 题全解、无 100%。本配置**预期 30 题全解 ≈ 0**。
  真产出 = 每题行为测试**部分通过率分布**,不是"解出 N 题"。
- "kcpos 分" = "kcpos 基建 + characterize 探黑盒 + LLM 重建" 的分,如实标注。
- PB 考绿地从零重建,**不验证《屎山》棕地设计**。
- 成本:多天机时(模拟编译慢)+ 可观 LLM 花费 + 逐题镜像拉/清(磁盘
  161GB,跑完一题 `docker rmi` 再下一题,控制器可断点续跑)。

## 1. 技术脊梁(characterize 的真正落点)

PB 给:黑盒参照二进制 + 文档(无源码)。重建循环:

1. 起 `:task_cleanroom` 容器(amd64,**inference 阶段无网**)。
2. host 侧 orchestrator(持 DeepSeek key):
   a. 读容器内文档(README / man page / --help)。
   b. **探黑盒(characterize 复用点)**:从文档合成输入探针 → 在容器内
      跑参照 `executable` → 抓观察 I/O → golden 行为契约(= de-facto SPEC)。
      这就是 characterize 引擎指向二进制而非 .py。
   c. **重建**:把 golden 契约喂 LLM 重建循环 → 在 /workspace 写源码 +
      compile.sh(产出 `./executable`)。
   d. **验证迭代**:容器内 build → 探针重测 build 产物 vs 参照 → diff →
      回灌 LLM 修。recover→construct→verify。
3. 导出 /workspace(自包含 源码+compile.sh,不依赖参照存在)→ submission.tar.gz。
4. `programbench eval` 评分(已验证本机可跑,gold cmatrix ✅ 768/769)。

## 2. 30 题选型

- 优先 28 道 `easy` 全取 + 2 道最小 `medium`(按镜像大小/语言简单度)。
- 偏好 C / Go 单或少文件工具(类 cmatrix);**避开** rust-at-scale /
  ffmpeg / sqlite / php-interpreter(镜像大、模拟编译以小时计)。
- 选型脚本:按 `load_all_instances()` 的 language+difficulty 排序产出名单。

## 3. 阶段与 kill 标准

| 阶段 | 内容 | kill 标准 |
|---|---|---|
| P1 适配器(本机,**零 LLM 花费**) | 写重建 orchestrator:读文档→characterize 探二进制→LLM 重建→容器内构建/迭代→导出 submission | 编不出能调 PB pipeline 的 submission 结构 → 重新设计,不进 P2 |
| P2 pilot(3 道最易,含 cmatrix) | 端到端真跑(kcpos **不看**上游源码,只看黑盒+文档) | 3 道全部连"可编译的 submission"都产不出 → **停**,不烧 30 题的钱 |
| P3 30 题批 | 断点续跑控制器(仿 HE batch:逐题日志 + 跑完 rmi + resume) | 前 10 题 0 个 compile 成功 → 暂停复盘 |
| P4 诚实报告 | 每题:compile 成功率 / 行为测试部分通过率 / 全解数(预期≈0) / 成本 | — |

## 4. 产物

- `tests/pb-kcpos/` 选题名单 + 每题 run 目录
- 重建 orchestrator(复用 internal/legacy/characterize + llm/transport)
- 控制器 + `_pb_kcpos_progress`,日志入 tests/.batch-logs/
- `pb-kcpos-30-result-*.md` 最终诚实报告(不报"解出 N 题"为标题,
  报部分通过率分布;明确这是绿地重建分,非棕地设计验证)

## 5. 立即开始(P1,不烧钱)

先做 P1 适配器 + P2 pilot 的 cmatrix(它的 task/cleanroom 镜像 + gold
基线我已有,正好做对照:kcpos 盲跑 vs gold ✅ 768/769)。P3 烧钱批次
在 P2 pilot 通过后再启,且需你明确放行。
