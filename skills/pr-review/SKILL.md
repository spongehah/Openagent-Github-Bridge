---
name: pr-review
description: Review pull requests, diffs, or recent code changes with a multi-aspect workflow covering general code quality, tests, comments, error handling, type design, and post-review simplification. Use this skill whenever the user asks for a PR review, pre-commit review, review of recent changes, test coverage gaps, silent-failure checks, comment or documentation accuracy, type or schema design review, or asks whether code is ready to merge. Also use it proactively when the user has just finished a meaningful code change and wants confidence before committing or opening a PR.
---

# PR Review

This skill converts the copied PR review toolkit into a skill-oriented workflow. It coordinates the bundled review prompts under `references/` and uses `commands/review-pr.md` as the high-level orchestration reference.

## What this skill owns

Use this skill for:

- Comprehensive review of a PR, branch, diff, or recent changes
- Targeted review of one or more aspects: `code`, `tests`, `comments`, `errors`, `types`, `simplify`
- Aggregating multiple review passes into one actionable report
- Deciding which review lenses matter for the current change set

Do not use this skill for unrelated implementation work unless the user explicitly asks you to apply fixes after the review.

## Load bundled resources selectively

Start with `commands/review-pr.md` to mirror the original toolkit workflow.

Then load only the analyzer reference files relevant to the current request:

- `references/code-reviewer.md` for general code review, project rules, and bug detection
- `references/pr-test-analyzer.md` for behavioral coverage and critical testing gaps
- `references/comment-analyzer.md` for comment accuracy, documentation drift, and removal suggestions
- `references/silent-failure-hunter.md` for hidden failures, fallback misuse, and weak error handling
- `references/type-design-analyzer.md` for type encapsulation, invariants, and model design
- `references/code-simplifier.md` for post-review simplification while preserving behavior

## Stay on the current branch

The user has already prepared the correct git workspace and target branch before this conversation starts. Treat the current checkout as the intended working environment.

Do not create a new branch, do not switch branches, do not recreate the worktree, and do not try to "fix" the environment by moving to another checkout. Review the current diff or PR context from the checkout that is already prepared for you.

Use `current_branch` as the basis for local diff inspection and any optional simplify-mode edits.

Use `owner/repo/main` as the default review baseline unless the user explicitly requests another base or narrower scope.

## Aspect routing

Always include `code` unless the user explicitly asks for only a different aspect.

Add other aspects based on the request or the changed files:

- `tests`: feature work changed, tests were added or modified, or the user asks whether coverage is sufficient
- `comments`: comments or docs changed, or the user asks whether documentation matches the code
- `errors`: error handling, retries, fallbacks, logging, recovery paths, or catch blocks changed
- `types`: structs, classes, interfaces, schemas, DTOs, models, or invariants changed
- `simplify`: the user explicitly asks to simplify or polish code, or asks to implement cleanup after review findings

If the user asks for `all`, interpret it as a full review of all aspects that are relevant to the change set.

## Execution workflow

1. Determine the review scope.
2. Inspect the change set and map it to the applicable aspects.
3. Read the corresponding analyzer reference files so each aspect uses the right evaluation lens.
4. For every independent review aspect, spawn a dedicated subagent and provide it only the relevant scope plus the matching file from `references/`.
5. Launch all independent review subagents in parallel whenever more than one aspect applies. Do not perform aspect-by-aspect analysis serially in the main thread.
6. Keep the main thread focused on scope selection, dispatch, and aggregation of returned findings.
7. Deduplicate overlapping findings, keep the clearest wording, and preserve the most severe classification.
8. Separate review from implementation:
   - For `code`, `tests`, `comments`, `errors`, and `types`, default to advisory findings only.
   - For `simplify`, edit code only when the user explicitly wants changes applied.
9. When the review is complete, publish the final result back to the PR thread through `skill github-progress-comment`. Reuse the existing PR-thread progress comment if one already exists, create or update it if needed, write the GitHub-facing content in Chinese, and do not finish the task silently.
10. Keep the formal PR review separate. The progress comment is the durable status summary in the PR conversation, not a replacement for the formal review decision.

## Subagent contract

When review aspects are delegated, the main thread is the coordinator, not an analyzer.

- Spawn one subagent per independent aspect, or per clearly separable scope if the user asks for the same aspect across unrelated areas
- Give each subagent the exact review scope, changed files, and the relevant file from `references/`
- Ask each subagent to return only high-signal findings with file and line references
- Run subagents in parallel rather than waiting for one aspect to finish before starting the next
- Aggregate results only after the delegated review passes return
- If only one aspect applies, it can still be delegated to one subagent; do not inline a full analyzer pass in the coordinator thread unless subagents are unavailable

## Review standards

Findings come first. Prioritize real bugs, regressions, broken assumptions, missing tests for critical behavior, misleading comments, silent failures, and weak type boundaries.

Keep the bar high for reporting:

- Prefer high-confidence findings over speculative nits
- Tie every finding to concrete code locations
- Explain the practical impact, not just style preference
- Respect project-specific guidance from repository instructions
- If no meaningful issues are found, say so explicitly and mention residual risk or unreviewed areas

When multiple analyzers surface the same issue, merge them into one finding and mention the strongest rationale.

## Output format

For review requests, present findings before any summary.

Use this structure unless the user requested a different format:

```markdown
# Review Findings

## Critical
- [aspect] Problem summary (`path:line`)
  Why it matters: ...
  Fix direction: ...

## Important
- [aspect] Problem summary (`path:line`)
  Why it matters: ...
  Fix direction: ...

## Suggestions
- [aspect] Suggestion (`path:line`)
  Why it helps: ...

## Strengths
- What is well done in this change

## Open Questions
- Any uncertainty that blocks a stronger conclusion
```

Rules:

- Order findings by severity, then by user impact
- Include `file:line` whenever possible
- Keep summaries crisp and actionable
- Omit empty sections
- If there are no material findings, say `No material issues found.` and then list residual risks or testing gaps if any
- After producing the findings, feed the review summary, verification notes, and final outcome into `github-progress-comment` on the PR thread instead of posting a separate wrap-up comment.
- In that PR-thread progress comment, make the `Summary`, `Verification`, and `Outcome` sections concrete. For example, `Verification` should mention that the review compared the current PR head against `owner/repo/main`, and `Outcome` should capture the review result plus next steps.

## Simplification mode

When the request is to simplify or polish code:

1. Review the changed code first using `references/code-simplifier.md` and, when useful, `references/code-reviewer.md`.
2. Preserve behavior and interfaces unless the user explicitly asks for behavioral changes.
3. Make the smallest set of edits that materially improves clarity or maintainability.
4. After editing, summarize what changed and any tradeoffs.

## Trigger examples

This skill should trigger for requests like:

- "Review this PR before I merge it"
- "Check whether my recent changes have enough tests"
- "Look for silent failures in this diff"
- "Are the comments I added still accurate?"
- "Review the new schema and type design"
- "I think this works now; can you do a final review before commit?"
- "Simplify the code I just changed without altering behavior"

It should not trigger for unrelated code changes that are not about review, verification, or post-review cleanup.
