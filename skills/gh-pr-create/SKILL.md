---
name: gh-pr-create
description: 通过 issue 号用 gh 读取 GitHub issue 与已有方案评论，结合当前仓库完成代码实现、创建 PR，并把修改总结和 PR 链接回写到同一个 issue。凡是用户提到“按 issue 实现”“根据方案写代码并提 PR”“读取 issue comment 里的 plan 后落地代码”“关联 issue 和 PR”这类场景，都应使用这个 skill。
---

# gh-pr-create

当用户希望从 GitHub issue 出发，按已有计划完成编码、创建 PR，并把结果回写到 issue 时，使用这个 skill。

这是一个“直接调用型” skill。起点是当前会话里的用户请求，不是 GitHub webhook 事件、GitHub Actions、slash 命令机器人，也不是仓库内的事件驱动自动化。

这个 skill 的核心约定：

1. 先解析目标 GitHub 仓库，再用 `gh` 读取 issue。
2. 读取 issue 下已经存在的 plan 评论。
3. 在开始改代码前，先发一条“处理中”的临时评论。
4. 在当前仓库完成代码实现。
5. 用 `gh` 创建 PR。
6. 用同一条工作评论回填修改总结、验证结果和 PR 链接。
7. 通过 PR body 建立 issue 和 PR 的关联。

## 输入

- 必填：issue 号
- 可选：用户给出的实现限制、reviewer、是否 draft、范围说明、补充约束等

如果没有 issue 号，直接向用户要，不要猜。

## 必备工具

- `gh`
- `git`
- `mcp__auggie_mcp__codebase_retrieval`
- 有 subagent 能力时使用 `spawn_agent` / `wait_agent`

不要假设环境里有 GitHub webhook 服务、bot 账号、issue comment 触发器或仓库自动化流水线。所有 GitHub 的读取和写回，都应在当前会话中直接通过 `gh` 完成。

## 固定 marker

固定使用以下 marker：

```text
<!-- skill:gh-issue-plan -->
<!-- skill:gh-pr-create -->
```

第一个 marker 用于识别计划评论，第二个 marker 用于识别当前 skill 自己创建的工作评论。

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

如果用户明确要求用 `upstream`，就必须遵守，不要静默回退到 `origin`。如果仍无法确定仓库，停下来向用户确认。

先检查 GitHub 权限：

```bash
gh auth status
```

开始改代码前先看本地工作区状态：

```bash
git status --short
```

如果本地已经有与当前任务可能冲突的未提交修改，不要强行继续，不要覆盖用户已有工作。需要先向用户确认。

## 工作评论协议

在深入分析或开始改代码前，先在目标 issue 下创建一条临时工作评论。

建议初始内容：

```markdown
<!-- skill:gh-pr-create -->
## 状态
- 进行中

正在根据 issue 和既有方案编写代码，稍后会回填：
- 修改总结
- 验证结果
- PR
```

推荐命令模式：

```bash
tmp_comment=$(mktemp)
cleanup() { rm -f "$tmp_comment"; }
trap cleanup EXIT

cat >"$tmp_comment" <<'EOF'
<!-- skill:gh-pr-create -->
## 状态
- 进行中

正在根据 issue 和既有方案编写代码，稍后会回填：
- 修改总结
- 验证结果
- PR
EOF

gh issue comment "$issue_number" --repo "$repo_slug" --body-file "$tmp_comment"
```

完成后，覆盖同一条评论：

```bash
gh issue comment "$issue_number" --repo "$repo_slug" --edit-last --create-if-none --body-file "$tmp_comment"
```

## 必读上下文

### 1. 读取 issue 与全部评论

先把 issue 正文和评论区都读全：

```bash
gh issue view "$issue_number" --repo "$repo_slug" --comments --json number,title,body,labels,comments,url
```

### 2. 找到最新的计划评论

找到最近一条正文中包含以下 marker 的 issue 评论：

```text
<!-- skill:gh-issue-plan -->
```

这条评论就是当前实现的主要依据。

如果没有这类评论，停下来并向用户确认下一步是：

- 先运行 `gh-issue-plan`
- 由用户指定另一条已确认的方案
- 明知有风险也要跳过方案直接实现

不要在没有计划的情况下假装方案已存在。

### 3. 读取本地文档与代码上下文

如果仓库里有相关文档，要先读，再开始实现。

优先检查这些位置：

- `docs/`
- `design/`
- `adr/` 或 `adrs/`
- `AGENTS.md`
- `CLAUDE.md`
- 其他 contributor guide、设计说明、服务开发文档

随后用 `mcp__auggie_mcp__codebase_retrieval` 获取精确代码上下文，包括处理入口、业务逻辑、模型、类型、脚本、测试和共享工具。编辑文件前必须先把相关上下文找全。

## 实现策略

### 主 agent 与 subagent 分工

可以积极用 subagent，但分工必须清晰。

主 agent 负责：

- 最终理解 issue 和 plan 的范围
- 分支策略
- 跨模块的架构判断
- 合并并行结果
- 最终验证
- PR 创建
- 最终 GitHub 评论更新

explorer 类型 subagent 适合做：

- 定位当前实现路径
- 查找测试与相邻代码
- 发现隐藏依赖或历史模式

worker 类型 subagent 适合做：

- 写入范围清晰、互不冲突的局部代码改动
- 分文件并行补测试
- 处理实现过程中暴露出的局部后续修正

如果某个任务的结果会立即阻塞下一步，就不要把它丢给 subagent 后原地等待。subagent 更适合并行做侧边工作，而不是替主 agent 承担关键路径决策。

### 推荐执行顺序

1. 根据 issue 与 plan 评论确认范围。
2. 用 `mcp__auggie_mcp__codebase_retrieval` 读取相关代码上下文。
3. 创建或切换到专用分支。
4. 实现已确认的改动。
5. 做针对性的验证。
6. 只提交本 issue 相关的文件。
7. 创建 PR。
8. 更新工作评论，写入结果。

## Git 工作流

如果能检测到目标仓库默认分支，优先基于默认分支创建工作分支：

```bash
base_branch=$(gh repo view "$repo_slug" --json defaultBranchRef --jq .defaultBranchRef.name)
base_remote=$(git remote | awk '$0=="upstream"{print; found=1} END{if(!found) print "origin"}')
git fetch "$base_remote" "$base_branch"
git switch -c "ai/issue-${issue_number}-short-topic" "$base_remote/$base_branch"
```

如果默认分支无法可靠检测，就向用户确认，或者检查本地 remote 与分支头。不要写死成 `main`。

如果要生成分支名 slug，基于 issue 标题提取一个简短、可读、文件名安全的片段即可。

创建 PR 前至少检查：

```bash
git status --short
git diff --stat
```

只提交当前 issue 相关文件。不要把无关的本地改动顺手带进 PR。

## 验证要求

运行与你改动最相关的测试或检查，并明确记录：

- 实际跑了什么
- 没跑什么
- 没跑的原因是什么

如果无法运行某项测试，就在最终评论和 PR body 里明确说明，不要暗示已经做了完整验证。

## PR 创建

如果仓库里存在 PR 模板、贡献说明或 CI 对 PR 标题/正文有约束，要先读再写。

可以先查这些文件：

```bash
rg --files .github docs . | rg 'pull_request_template|PULL_REQUEST_TEMPLATE|CONTRIBUTING|contributing'
```

PR body 至少应包含：

- 修改摘要
- 测试/验证
- 仓库要求的 checklist
- issue 关联语句，例如 `Closes #123`

可参考这个结构：

```markdown
## Summary
- ...
- ...

## Testing
- `go test ...`
- Not run: ...

## Checklist
- [x] Tests pass
- [x] Required checks for this repo were considered

Closes #123
```

用 `gh pr create` 创建 PR，尽量使用显式参数，避免进入交互流程。

PR 标题也要遵循目标仓库自己的规则。除非仓库明确要求，否则不要擅自假设必须用 Conventional Commits、Jira 前缀或某个固定 house style。

## 最终 issue 评论格式

把工作评论更新成这个结构：

```markdown
<!-- skill:gh-pr-create -->
## 状态
- 已完成

## 修改总结
- ...
- ...

## 验证结果
- 已执行：...
- 未执行：...

## PR
- #1234
- https://github.com/owner/repo/pull/1234

## 备注
- 如无额外风险，写：`- 暂无`
```

这条 issue 评论要简洁。详细设计理由、实现细节和完整验证上下文应主要放在 PR 中。

## issue 与 PR 关联

真正稳定的关联关系应该写在 PR body 里，例如：

- `Closes #<issue_number>`
- `Fixes #<issue_number>`

同时，也要在最终 issue 评论里带上 PR 编号与 URL，方便人直接跳转。

## 失败处理

不要把评论永远停留在“进行中”状态。

如果编码或 PR 创建失败，也必须更新同一条评论，例如：

```markdown
<!-- skill:gh-pr-create -->
## 状态
- 阻塞

## 修改总结
- 已完成：...
- 未完成：...

## 验证结果
- 已执行：...
- 未执行：...

## PR
- 尚未创建

## 备注
- 阻塞原因：...
- 需要用户确认：...
```

## 默认原则

- 默认以 plan 评论为主要依据，除非 issue 后续评论已经明确推翻或补充了原方案。
- 如果 plan 之后 issue 评论新增了要求，实现前要先显式对齐，而不是悄悄忽略。
- 优先做小而可审查的 PR，不要顺手塞进未确认的扩展需求。
- 使用 subagent 是为了并行提速和局部拆分，不是为了逃避主 agent 对最终实现质量的责任。
