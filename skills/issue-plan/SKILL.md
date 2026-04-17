---
name: issue-plan
description: "issue-plan — Analyze a GitHub issue against the current repository and write back an actionable implementation plan to the same issue without changing code or opening a pull request. Use when the user provides an issue number or issue URL and asks for a plan, or when an issue is labeled `ai-plan` and the task is to design the approach before coding."
tags: ["git", "github", "automation", "issue-plan", "planning"]
---

# issue-plan — Agent Skill

You are an autonomous planning agent. Read the GitHub issue, inspect the current repository, decide what implementation work is actually needed, and publish a concrete plan back to the same issue.

## Git Execution Guardrail

For every terminal tool call that runs `git`, you must set `workdir=$pwd`.

- Treat `$pwd` as the root of the already-prepared repository worktree for this task.
- Do not run any `git` command from a parent checkout, sibling checkout, or fallback directory.
- If `$pwd` is not the prepared worktree or the repository context looks wrong, stop and report the mismatch instead of running `git` elsewhere.

This skill intentionally stops before coding:

- Do not modify repository files.
- Do not implement code.
- Do not open a pull request.
- Do not switch branches.

Use the phases below in order.

---

## Progress Checklist

- [ ] Phase 1: Parse Issue Reference
- [ ] Phase 2: Fetch Issue Details
- [ ] Phase 3: Analyze the Issue
- [ ] Phase 4: Design the Plan
- [ ] Phase 5: Publish the Plan

---

## Phase 1: Parse Issue Reference

Extract the target GitHub issue from the task input.

Supported formats:

| Format | Example |
|---|---|
| Full URL | `https://github.com/owner/repo/issues/123` |
| Shorthand | `owner/repo#123` |
| Issue number only | `#123` or `123` |

Parsing logic:

1. If a full URL is present, extract `owner`, `repo`, and `number`.
2. If shorthand is present, extract `owner`, `repo`, and `number`.
3. If only an issue number is present, resolve the repository from local git remotes in this order:
   - `upstream`
   - `origin`
4. If the repository still cannot be determined, stop and ask the user for a full issue reference.
5. Confirm the parsed target in your working notes:
   - `Parsed issue: owner/repo#number`

Example repo detection:

```bash
# Execute with workdir=$pwd
repo_slug=""
if [ -z "$repo_slug" ] && git remote get-url upstream >/dev/null 2>&1; then
  repo_slug=$(git remote get-url upstream | sed -E 's#(git@github.com:|https://github.com/)##; s#\.git$##')
fi
if [ -z "$repo_slug" ] && git remote get-url origin >/dev/null 2>&1; then
  repo_slug=$(git remote get-url origin | sed -E 's#(git@github.com:|https://github.com/)##; s#\.git$##')
fi
```

---

## Phase 2: Fetch Issue Details

Read the full issue before planning. Always include comments because later discussion often changes scope.

Preferred command:

```bash
gh issue view "$issue_number" --repo "$repo_slug" --comments --json number,title,body,labels,comments,url
```

If JSON mode is unavailable, fall back to:

```bash
gh issue view "$issue_number" --repo "$repo_slug" --comments
```

Extract at least:

- Problem summary
- Expected behavior
- Actual behavior or requested new behavior
- Acceptance criteria
- Error messages, logs, screenshots, or repro clues
- Mentioned file paths, modules, APIs, tables, jobs, or services
- Extra constraints or decisions added in comments
- Related issues or pull requests

Before deep analysis, verify GitHub access once:

```bash
gh auth status
```

### Stay on the current branch

The user has already prepared the correct git worktree and target branch before this conversation starts. Treat the current checkout as the intended working environment.

Do not create a new branch, do not switch branches, do not recreate the worktree, and do not try to "fix" the environment by moving to another checkout. Continue working exactly in the branch that is currently checked out in the local repository.

```bash
# Execute with workdir=$pwd
# Detect the current branch and validate the prepared environment
current_branch=$(git branch --show-current 2>/dev/null)
if [ -z "$current_branch" ]; then
  echo "Detached HEAD: stop and report that the prepared worktree/branch environment is invalid. Do not check out another branch yourself."
fi
```

Use `current_branch` as the repository context for all later analysis and GitHub write-back steps.

---

## Phase 3: Analyze the Issue

The planning quality depends on repository context. Do not plan from issue text alone.

### 3.1 Read Nearby Documentation

Before searching code, read obviously related local docs when present:

- `AGENTS.md`
- `README.md`
- `docs/`
- `design/`
- `adr/` or `adrs/`
- feature-specific docs or contributor guides mentioned by the issue

### 3.2 Retrieve Code Context

Use `mcp__auggie_mcp__codebase_retrieval` before making technical recommendations.

Ask for:

- probable entry points for the behavior described in the issue
- relevant business logic, data models, config, scripts, and tests
- nearby modules that are likely to be affected
- concrete files and symbols to inspect next

If the first retrieval is incomplete, ask narrower follow-up queries instead of guessing.

### 3.3 Inspect Candidate Files

After retrieval, read the most relevant files and trace the code path:

1. Find where the current behavior starts.
2. Trace how data flows through handlers, services, models, and shared helpers.
3. Check existing tests to understand the intended behavior.
4. Note related config, migration, or deployment implications if they exist.

### 3.4 Produce Structured Analysis

Before drafting the plan, write a concise analysis for yourself:

```text
### Analysis Result
- Current Behavior: {What the repository does today}
- Root Cause / Gap: {Why the issue exists}
- Affected Files: {file1, file2, file3}
- Constraints: {Existing architecture, config, compatibility, ownership}
- Risks: Low / Medium / High
- Unknowns: {Anything still unclear}
```

### 3.5 Scope Assessment

Decide how large the change is before planning:

- If the issue contains multiple independent problems, plan the most critical path first and call out follow-up work separately.
- If this is a monorepo, narrow the plan to the specific package or service that owns the behavior.
- If the request is still ambiguous after reading issue, comments, docs, and code, carry the uncertainty into the final `Open Questions` section instead of inventing details.

---

## Phase 4: Design the Plan

Turn the analysis into an implementation plan that an engineer or a later coding skill can execute directly.

The plan should be ordered and concrete. It must cover:

1. Which modules or files are most likely to change
2. What code-path changes are needed
3. Any API, schema, config, or workflow impact
4. How to test the change
5. Rollout, migration, or compatibility notes when relevant
6. Open questions that still need confirmation

Keep the plan grounded in the repository as it exists today. Avoid generic architecture advice that is not tied to actual files or symbols.

When possible, estimate blast radius in practical terms, for example:

- `2-3 Go files in handler/service/session flow`
- `1 config struct plus prompt builder tests`
- `new unit test cases in the existing package`

---

## Phase 5: Publish the Plan

Write the user-facing GitHub comment in Chinese.

Publish the final plan by feeding it into `skill github-progress-comment` whenever that skill is available for this task. Reuse the same mutable progress comment instead of creating a second status thread.

- Prefer updating the existing progress comment for the issue through `github-progress-comment`.
- If no managed progress comment exists yet, create or update the issue-thread comment through `github-progress-comment` rather than writing a separate wrap-up comment yourself.
- Do not leave the thread without a user-visible result.

When you need a temporary file for multi-line Markdown, remove it after use.

Recommended reusable marker for a standalone plan comment:

```text
<!-- skill:issue-plan -->
```

Suggested command pattern:

```bash
tmp_comment=$(mktemp)
cleanup() { rm -f "$tmp_comment"; }
trap cleanup EXIT
```

### Final Comment Structure

When you are updating the progress comment owned by `github-progress-comment`, keep its required top-level sections and place the plan content inside `Outcome`.

Use this structure:

```markdown
## Status
- completed

## Summary
- 已完成 issue 与仓库代码分析。
- 核心判断：{一句话概括问题或改动方向}

## Verification
- 已阅读 issue 正文与评论。
- 已检查代码：`path/to/file1`、`path/to/file2`
- 已确认测试或验证入口：{tests or "暂无现成测试"}

## Outcome
### 计划内容
1. ...
2. ...
3. ...

### 补充
- 现状分析：...
- 风险与影响：...
- 测试建议：...

### 疑问
- 暂无
```

Only if `github-progress-comment` is unavailable or the surrounding workflow explicitly does not use it, fall back to posting a standalone issue comment. In that fallback, include the marker and keep the same inner sections:

```markdown
<!-- skill:issue-plan -->
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
- 暂无
```

### Blocked or Ambiguous Cases

Do not silently fail. If the issue cannot be planned reliably, still use `github-progress-comment` to update the GitHub thread in Chinese whenever possible.

Example blocked outcome:

```markdown
## Status
- blocked

## Summary
- 已完成初步分析，但当前信息不足以给出可靠实施方案。

## Verification
- 已阅读 issue 正文与评论。
- 已检查代码：`path/to/file1`

## Outcome
### 计划内容
- 当前无法继续细化实施步骤。

### 补充
- 阻塞原因：缺少明确验收标准 / 目标模块不清晰 / 依赖外部系统信息

### 疑问
- 需要确认：...
```

---

## Default Principles

- Front-load the same issue parsing and code analysis discipline used by `issue-to-pr`.
- Stop at planning; do not drift into implementation.
- Prefer one accurate repository-specific plan over a broad but vague design write-up.
- Reuse the existing GitHub progress comment through `github-progress-comment` when the prompt has already established one.
