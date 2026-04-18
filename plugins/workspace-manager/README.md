# Workspace Manager Service

这是当前 `openagent-github-bridge` 推荐使用的 agent 侧 companion service。

它只负责一件事：在 OpenCode 所在机器上创建或复用一个可绑定的独立 git workspace 目录，并把结果通过 HTTP 返回给 Bridge。

## 目录

```text
plugins/workspace-manager/
├── main.go
└── README.md
```

## 设计边界

- 不创建 OpenCode session
- 不依赖模型或 prompt
- 不关心 GitHub webhook
- 不负责 PR 创建或 review

Bridge 的职责是：

1. 调这个服务创建或复用 workspace
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
  "workspaceRoot": "/Users/example/.opencode/workspaces"
}
```

### `POST /workspaces/create-or-reuse`

请求体：

```json
{
  "owner": "openagent",
  "repo": "github-bridge",
  "repoURL": "git@github.com:openagent/github-bridge.git",
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
  "worktreePath": "/Users/example/.opencode/workspaces/openagent/github-bridge/issue-42",
  "reused": false
}
```

## 环境变量

### 可选

- `WORKSPACE_MANAGER_ADDR`
  - 默认 `:4081`
- `WORKSPACE_MANAGER_ROOT`
  - workspace 托管根目录
  - 默认 `~/.opencode/workspaces`
- `WORKSPACE_MANAGER_BASE_REMOTE`
  - clone 后远端名称与 issue 基线 remote 名
  - 默认 `origin`
- `WORKSPACE_MANAGER_USERNAME`
  - 默认 `workspace-manager`
- `WORKSPACE_MANAGER_PASSWORD`
  - 留空表示不启用 Basic Auth

## 启动方式

在 agent 机器上启动即可；服务会根据 Bridge 请求中携带的 `repoURL` 直接从远端 clone。

```bash
cd /path/to/openagent-github-bridge

export WORKSPACE_MANAGER_ADDR="127.0.0.1:4081"
export WORKSPACE_MANAGER_BASE_REMOTE="origin"
export WORKSPACE_MANAGER_PASSWORD=""

go run ./plugins/workspace-manager
```

也可以直接编译：

```bash
go build -o ./bin/workspace-manager ./plugins/workspace-manager

WORKSPACE_MANAGER_ADDR="127.0.0.1:4081" \
WORKSPACE_MANAGER_BASE_REMOTE="origin" \
./bin/workspace-manager
```

## 与 Bridge 的配置关系

Bridge 需要知道 companion service 地址：

```yaml
opencode:
  host: "http://127.0.0.1:4096"
  clone_url: "git@github.com:owner/default-repo.git" # 可选默认覆盖
  workspace_manager_host: "http://127.0.0.1:4081"
```

多仓库模式下：

```yaml
repositories:
  "owner1/repo1":
    opencode_host: "http://127.0.0.1:4096"
    clone_url: "git@github.com:owner1/repo1.git"
    workspace_manager_host: "http://127.0.0.1:4081"
  "owner2/repo2":
    opencode_host: "http://127.0.0.1:4097"
    clone_url: "git@github.com:owner2/repo2.git"
    workspace_manager_host: "http://127.0.0.1:4082"
```

## 语义规则

- issue 使用默认分支作为 `baseRef`
  - 首次创建时会按请求里的 `repoURL` 在受管目录下 clone 一份独立仓库，再从 `{baseRemote}/{baseRef}` 建立 `issue-{number}` 分支
- PR review 使用 PR head ref 作为 `baseRef`
- 如果提供 `headSHA`，优先按该 commit 创建或刷新 `pr-{number}` 分支
- 如果 PR workspace 已存在且再次传入 `headSHA`，会对现有 workspace 执行 hard reset/clean 并切到最新 head，保证目录内容与 PR 一致
- 已存在的 workspace 每次请求都会校验 `{baseRemote}` 的 URL，必要时自动更新为当前请求里的 `repoURL`
- workspace 路径固定为：

```text
~/.opencode/workspaces/{owner}/{repo}/issue-{number}
~/.opencode/workspaces/{owner}/{repo}/pr-{number}
```

- 同一个 `{owner}/{repo}/{kind}/{number}` 固定复用同一 workspace
- `force=true` 时会强制删除并重建该路径
