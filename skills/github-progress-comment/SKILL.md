---
name: github-progress-comment
description: Maintain exactly one mutable GitHub progress comment for issue or pull request work. Use this skill early whenever an agent is working from a GitHub thread and the user should see progress, blockers, or a final wrap-up in GitHub comments, even if the user did not explicitly ask for progress updates. Create the comment immediately, then keep updating that same comment with summary, verification, and outcome instead of posting separate wrap-up comments.
---

# github-progress-comment

Use this skill when work is happening for a GitHub issue or pull request and the GitHub thread should show visible progress while the task is running.

This skill is the default transport for GitHub comment updates. It owns the lifecycle of one mutable progress comment. Other skills may decide what work to do, but they should feed content into this comment instead of creating extra progress or wrap-up comments.

This skill is not the global protocol owner. If the repository prompt defines a broader GitHub interaction rule, treat that prompt as the source of truth and use this skill to execute the comment portion of that protocol.

## What this skill owns

- Create one progress comment early, before substantial reading, editing, or review work.
- Reuse a stable marker so the same comment can be found and updated later.
- Keep the status current: `in_progress`, `completed`, or `blocked`.
- Update the same comment with final `Summary`, `Verification`, and `Outcome`.
- Leave formal PR reviews, PR bodies, labels, and other GitHub surfaces to the task-specific workflow.

## Boundaries

- Do not create a second summary comment if updating the existing progress comment is enough.
- Do not turn this skill into the final PR review workflow. For review tasks, use this skill for status updates and submit the final review separately.
- Prefer the main agent to create and update the GitHub comment. Subagents can prepare content, but they should not independently post or edit GitHub comments unless the main workflow explicitly delegates that responsibility.
- If the repository prompt gives a specific marker, required sections, or wording, follow that prompt instead of the generic examples below.

## Workflow

1. Determine the target `owner/repo` and issue or PR number.
2. Read the task prompt for any required marker or required sections.
3. Build a marker that uniquely identifies this progress comment.
4. Before substantial work, find any existing progress comment you already own for that marker.
5. If none exists, create one with status `in_progress`.
6. When the task finishes or becomes blocked, update that same comment to `completed` or `blocked`.
7. Keep the final content concise, factual, and tied to real work performed.

## Comment Contract

Use this structure unless the task prompt requires a different one:

```markdown
<!-- openagent:progress-comment owner/repo#123 -->
## Status
- in_progress | completed | blocked

## Summary
- Short user-facing summary of what changed or what was found

## Verification
- Tests run, manual checks, or reason verification could not be completed

## Outcome
- PR link / branch / review result / blocker / follow-up needed
```

Guidance:

- `Summary` should describe the actual work completed, not intentions.
- `Verification` should mention real checks only. Do not invent tests or success.
- `Outcome` should point the user to the next concrete GitHub artifact or blocker.
- For the initial comment, it is fine to omit `Summary`, `Verification`, and `Outcome` and instead say they will be filled in later.

## Preferred Command Pattern

Prefer exact comment-id updates over `gh issue comment --edit-last`. In automation, `--edit-last` is fragile because someone else may comment after you, or you may have posted another comment for a different purpose.

Use `gh issue comment` for quick manual creation only when you are sure there is no ambiguity. For reliable agent execution, resolve the exact comment id by marker and patch that specific comment.

Example shell pattern:

```bash
repo_slug="owner/repo"
issue_number="123"
marker="<!-- openagent:progress-comment owner/repo#123 -->"
gh_login=$(gh api user --jq '.login')
tmp_comment=$(mktemp)

cleanup() { rm -f "$tmp_comment"; }
trap cleanup EXIT

find_progress_comment_id() {
  gh api "repos/$repo_slug/issues/$issue_number/comments" --paginate \
    --jq 'map(select(.user.login == "'"$gh_login"'" and (.body | contains("'"$marker"'")))) | last | .id // empty'
}
```

Create the initial body:

```bash
cat >"$tmp_comment" <<'EOF'
<!-- openagent:progress-comment owner/repo#123 -->
## Status
- in_progress

Working on this task. I will update this comment with:
- Summary
- Verification
- Outcome
EOF
```

Create or reuse the progress comment:

```bash
comment_id=$(find_progress_comment_id)

if [ -z "$comment_id" ]; then
  comment_id=$(
    gh api "repos/$repo_slug/issues/$issue_number/comments" \
      --method POST \
      -f body="$(cat "$tmp_comment")" \
      --jq '.id'
  )
fi
```

When work is finished or blocked, overwrite the file and patch the same comment id:

```bash
cat >"$tmp_comment" <<'EOF'
<!-- openagent:progress-comment owner/repo#123 -->
## Status
- completed

## Summary
- ...

## Verification
- ...

## Outcome
- PR / review / blocker / follow-up
EOF

gh api "repos/$repo_slug/issues/comments/$comment_id" \
  --method PATCH \
  -f body="$(cat "$tmp_comment")"
```

If the task becomes blocked before the work is finished, still update the same comment:

```markdown
<!-- openagent:progress-comment owner/repo#123 -->
## Status
- blocked

## Summary
- Work could not be completed yet.

## Verification
- Explain what was checked before the blocker was hit.

## Outcome
- Blocked by: missing access / failing dependency / unclear requirement / waiting for user input
```

## Templates

Initial comment:

```markdown
<!-- openagent:progress-comment owner/repo#123 -->
## Status
- in_progress

Working on this task. I will update this comment with:
- Summary
- Verification
- Outcome
```

Completed comment:

```markdown
<!-- openagent:progress-comment owner/repo#123 -->
## Status
- completed

## Summary
- ...

## Verification
- ...

## Outcome
- PR / review / blocker / follow-up
```

Blocked comment:

```markdown
<!-- openagent:progress-comment owner/repo#123 -->
## Status
- blocked

## Summary
- ...

## Verification
- ...

## Outcome
- Blocked by ...
```

## Notes

- For pull requests, use an issue comment on the PR thread for the progress comment.
- When the task prompt says GitHub comments are the primary user communication channel, use this skill as the default vehicle for progress, completion, and blocker updates unless a formal PR review is explicitly required.
- If you also need to submit a formal PR review, this skill only manages the progress comment; submit the formal review separately.
- If the task is resumed later, find the existing marked comment first and continue updating it rather than creating a fresh one.
