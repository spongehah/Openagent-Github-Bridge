---
name: gh-issue-plan
description: 通过 issue 号用 gh 读取 GitHub issue，结合当前仓库代码做需求分析，并把实现计划以更新评论的方式回写到同一个 issue。凡是用户提到“根据 issue 做方案”“分析 issue 需求”“按 issue 号结合代码给计划”“输出计划内容/补充/疑问”这类场景，都应使用这个 skill。
---

# gh-issue-plan

当用户给出 issue 号，希望你结合当前仓库代码产出一份可执行的实现方案，并把方案回写到 GitHub issue 时，使用这个 skill。

这是一个“直接调用型” skill。起点是当前会话里的用户请求，不是 GitHub webhook 事件、GitHub Actions、slash 命令机器人，也不是仓库里的任何自动化入口。

这个 skill 的核心约定：

1. 先解析目标 GitHub 仓库，再用 `gh` 读取 issue。
2. 在做深入分析前，先立即发一条“处理中”的临时评论。
3. 在给出计划前，必须先检查代码仓库，而不是只看 issue 文本。
4. 可以积极使用 subagent 并行探索，但最终方案和 GitHub 评论更新必须由主 agent 收口。
5. 最终必须更新最开始那条工作评论，而不是再额外发一条总结评论。

## 输入

- 必填：issue 号
- 可选：用户额外给出的范围限制、优先级、实现偏好、风险要求等

如果缺少 issue 号，直接向用户要，不要猜。

## 必备工具

- `gh`
- `git`
- `mcp__auggie_mcp__codebase_retrieval`
- 有 subagent 能力时使用 `spawn_agent` / `wait_agent`

不要假设环境中存在 GitHub webhook 服务、bot 账号、评论触发器或仓库自动化。所有 GitHub 的读取和写回，都应在当前会话中直接通过 `gh` 完成。

## 产出物

最终产出是一条被更新后的 issue 评论。评论里必须保留稳定 marker，便于后续技能或流程识别。

固定使用这个 marker：

```text
<!-- skill:gh-issue-plan -->
```

## 仓库与 GitHub 定位

先解析目标仓库，不要臆测。

解析优先级：

1. 用户显式提供的仓库，例如 `owner/repo`
2. 当前 git 仓库的 `upstream` remote
3. 当前 git 仓库的 `origin` remote

```bash
repo_slug="${user_repo:-}"
if [ -z "$repo_slug" ] && git remote get-url upstream >/dev/null 2>&1; then
  repo_slug=$(git remote get-url upstream | sed -E 's#(git@github.com:|https://github.com/)##; s#\.git$##')
fi
if [ -z "$repo_slug" ] && git remote get-url origin >/dev/null 2>&1; then
  repo_slug=$(git remote get-url origin | sed -E 's#(git@github.com:|https://github.com/)##; s#\.git$##')
fi
```

如果用户明确说了要用 `upstream`，就必须尊重，不要静默降级到 `origin`。如果上述路径都无法解析，就停下来向用户确认仓库。

正式开始前先确认 GitHub 权限：

```bash
gh auth status
```

## 工作评论协议

在大规模读代码或启动 subagent 之前，先创建临时 issue 评论。

建议用临时文件承载多行内容，写完后删除临时文件。

初始评论建议如下：

```markdown
<!-- skill:gh-issue-plan -->
## 状态
- 进行中

正在结合 issue 和仓库代码分析需求，稍后会回填：
- 计划内容
- 补充信息
- 疑问
```

推荐命令模式：

```bash
tmp_comment=$(mktemp)
cleanup() { rm -f "$tmp_comment"; }
trap cleanup EXIT

cat >"$tmp_comment" <<'EOF'
<!-- skill:gh-issue-plan -->
## 状态
- 进行中

正在结合 issue 和仓库代码分析需求，稍后会回填：
- 计划内容
- 补充信息
- 疑问
EOF

gh issue comment "$issue_number" --repo "$repo_slug" --body-file "$tmp_comment"
```

后续完成时，覆盖同一条评论：

```bash
gh issue comment "$issue_number" --repo "$repo_slug" --edit-last --create-if-none --body-file "$tmp_comment"
```

这里说的“最开始的评论”，始终指的是这个 skill 自己创建的工作评论，不是 issue 作者最初正文，也不是别人发的第一条评论。

## 调研流程

### 1. 读取 issue 及讨论区

除了 issue 正文，还要看评论，因为很多补充约束、边界条件、后来修订都在评论里。

```bash
gh issue view "$issue_number" --repo "$repo_slug" --comments --json number,title,body,labels,comments,url
```

至少提取这些信息：

- 用户真正想解决的问题
- 明确写出的验收标准
- issue 评论里新增的隐含约束
- issue 中是否已经点名了模块、API、文件或服务

### 2. 读取本地相关文档

如果仓库里有明显相关的本地文档，要先读，再做计划。

优先检查这些位置：

- `docs/`
- `design/`
- `adr/` 或 `adrs/`
- `AGENTS.md`
- `CLAUDE.md`
- 其他 contributor guide、设计说明、服务级开发文档

### 3. 在计划前先取代码上下文

在给实现建议前，必须先用 `mcp__auggie_mcp__codebase_retrieval` 获取代码上下文。关注处理入口、业务逻辑、数据模型、共享工具、测试、脚本等。

好的检索请求通常会同时说明：

- 这个 issue 需要实现什么行为
- 现有实现大概率在哪一层或哪个模块
- 哪些模型、接口、脚本、测试可能相关
- 哪些文件最可能需要修改

第一次检索不够时，不要猜；继续用更窄的问题递归检索。

### 4. 用 subagent 做并行探索

当 issue 涉及多个相对独立的方向时，应该积极用 subagent。

常见拆分方式：

- API 层 vs 业务逻辑层 vs 存储/模型层
- 实现文件 vs 现有测试
- 主服务模块 vs 共享包 vs 部署/自动化脚本

这些职责必须留在主 agent：

- 决定最终计划
- 处理 subagent 之间结论冲突
- 判断哪些内容在范围内，哪些不在
- 编写并更新 GitHub 评论

不要把关键路径上的最终综合判断外包给 subagent。subagent 负责找证据，主 agent 负责下结论。

### 5. 形成可执行计划

这份计划要具体到足以让后续 `gh-pr-create` 或人类工程师直接开始实现，而不是还要重新做一轮需求分析。

至少覆盖：

- 目标模块与高概率修改文件
- 核心代码路径会怎么改
- 数据模型 / API / 配置层面的影响
- 测试策略
- 如有必要，给出上线、迁移或兼容性注意点
- 当前还不确定、需要确认的决策点

## 最终评论格式

把工作评论更新成这个结构：

```markdown
<!-- skill:gh-issue-plan -->
## 状态
- 已完成

## 计划内容
1. ...
2. ...
3. ...

## 补充
- 现状分析：...
- 风险与影响：...
- 测试建议：...

## 疑问
- 如无疑问，写：`- 暂无`
- 如有阻塞或待确认项，逐条列出
```

质量要求：

- `计划内容` 必须是按执行顺序组织的行动项。
- `补充` 用来补现状、风险、测试、兼容性，不要和计划内容重复。
- `疑问` 只放真正需要确认或会阻塞实现的问题。

## 失败处理

不要把评论永远停留在“进行中”状态。

如果被阻塞，也要更新同一条评论，例如：

```markdown
<!-- skill:gh-issue-plan -->
## 状态
- 阻塞

## 计划内容
- 当前无法给出可靠计划

## 补充
- 已完成的分析：...
- 阻塞原因：...

## 疑问
- 需要用户确认：...
```

## 默认原则

- 优先输出贴近仓库实际情况的计划，而不是泛泛的架构建议。
- 如果 issue 本身信息不足，读完正文和评论后仍不清晰，就把缺失项明确写进 `疑问`。
- 如果后续评论里的新要求和 issue 正文不一致，以更新的要求为准，并在 `补充` 中说明。
