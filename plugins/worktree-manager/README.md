# Worktree Manager Service

这是当前 `openagent-github-bridge` 推荐使用的 agent 侧 companion service。

它只负责一件事：在 OpenCode 所在机器上创建或复用一个可绑定的 git worktree 目录，并把结果通过 HTTP 返回给 Bridge。

## 目录

```text
plugins/worktree-manager/
├── main.go
└── README.md
```

## 设计边界

- 不创建 OpenCode session
- 不依赖模型或 prompt
- 不关心 GitHub webhook
- 不负责 PR 创建或 review

Bridge 的职责是：

1. 调这个服务创建或复用 worktree
2. 读取返回的 `worktreePath`
3. 再调用 OpenCode `POST /session` 把正式 session 绑定到该目录
4. 最后 `prompt_async`

## HTTP 接口

### `GET /health`

用于健康检查。

返回示例：

```json
{
  "status": "ok",
  "repoRoot": "/Users/example/repos/openagent/github-bridge",
  "worktreeRoot": "/Users/example/.opencode/worktrees"
}
```

### `POST /worktrees/create-or-reuse`

请求体：

```json
{
  "owner": "openagent",
  "repo": "github-bridge",
  "kind": "issue",
  "number": 42,
  "branch": "issue-42",
  "baseRef": "main",
  "headSHA": "",
  "force": false
}
```

返回体：

```json
{
  "key": "openagent/github-bridge/issue/42",
  "kind": "issue",
  "branch": "issue-42",
  "baseRef": "main",
  "worktreePath": "/Users/example/.opencode/worktrees/openagent/github-bridge/issue-42",
  "reused": false
}
```

## 环境变量

### 必填

- `WORKTREE_MANAGER_REPO_ROOT`
  - 目标仓库根目录

### 可选

- `WORKTREE_MANAGER_ADDR`
  - 默认 `:4081`
- `WORKTREE_MANAGER_ROOT`
  - worktree 托管根目录
  - 默认 `~/.opencode/worktrees`
- `WORKTREE_MANAGER_BASE_REMOTE`
  - issue worktree 基线 remote
  - 默认 `origin`
- `WORKTREE_MANAGER_USERNAME`
  - 默认 `worktree-manager`
- `WORKTREE_MANAGER_PASSWORD`
  - 留空表示不启用 Basic Auth

## 启动方式

在 agent 机器上启动，且 `WORKTREE_MANAGER_REPO_ROOT` 必须指向当前 OpenCode 实例对应的仓库根目录。

```bash
cd /path/to/openagent-github-bridge

export WORKTREE_MANAGER_REPO_ROOT="/Users/example/repos/openagent/github-bridge"
export WORKTREE_MANAGER_ADDR="127.0.0.1:4081"
export WORKTREE_MANAGER_BASE_REMOTE="origin"
export WORKTREE_MANAGER_PASSWORD=""

go run ./plugins/worktree-manager
```

也可以直接编译：

```bash
go build -o ./bin/worktree-manager ./plugins/worktree-manager

WORKTREE_MANAGER_REPO_ROOT="/Users/example/repos/openagent/github-bridge" \
WORKTREE_MANAGER_ADDR="127.0.0.1:4081" \
WORKTREE_MANAGER_BASE_REMOTE="origin" \
./bin/worktree-manager
```

## 与 Bridge 的配置关系

Bridge 需要知道 companion service 地址：

```yaml
opencode:
  host: "http://127.0.0.1:4096"
  worktree_manager_host: "http://127.0.0.1:4081"
```

多仓库模式下：

```yaml
repositories:
  "owner1/repo1":
    opencode_host: "http://127.0.0.1:4096"
    worktree_manager_host: "http://127.0.0.1:4081"
  "owner2/repo2":
    opencode_host: "http://127.0.0.1:4097"
    worktree_manager_host: "http://127.0.0.1:4082"
```

## 语义规则

- issue 使用默认分支作为 `baseRef`
  - 创建时优先直接使用 `{baseRemote}/{baseRef}`，默认即 `origin/main`
  - 如果远程 ref 不能直接用于 `git worktree add`，会先同步到本地分支再创建
- PR review 使用 PR head ref 作为 `baseRef`
- 如果提供 `headSHA`，优先按该 commit 创建 worktree
- worktree 路径固定为：

```text
~/.opencode/worktrees/{owner}/{repo}/issue-{number}
~/.opencode/worktrees/{owner}/{repo}/pr-{number}
```

- 同一个 `{owner}/{repo}/{kind}/{number}` 固定复用同一 worktree
- `force=true` 时会强制重建该路径
