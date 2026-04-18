# OpenAgent GitHub Bridge

GitHub Webhook 到自管理 AI coding agent 的桥接服务，基于 OpenCode / OpenAgent 工作流，支持自动修复 Issue、输出实现方案、响应 `/go` 评论指令和执行 PR Review。

适合希望把 GitHub 事件自动分发给自托管 AI Agent，并保留模型选择权、工作目录控制权和人工接管能力的团队。

## 功能特性

- **Mention**: Comment 首行以 `@ogb-bot` 提及时，自动触发 AI 进行工作
- **AI-Fix**: Issue 被打上 `ai-fix` 标签时，自动触发 AI 分析并创建修复 PR
- **AI-Plan**: Issue 被打上 `ai-plan` 标签时，自动触发 AI 输出实现方案
- **Slash Coding**: Issue 评论以 `/go` 开头时，自动触发 AI 编码与创建 PR
- **PR Review**: PR 创建或被打上 `ai-review` 标签时，自动进行代码审查
- **Session 复用**: 同一 Issue/PR 的多次交互复用同一 Agent Session，保持上下文
- **Git Workspace 隔离**: 每个 Issue/PR 在独立的 git workspace 中工作，互不干扰
- **Fire-and-Forget**: Bridge 只负责下发任务，Agent 独立完成工作

## 相比 Codex / Claude Web 版和 GitHub App 的优势

- **`支持随时人工接管`**：任务由 Bridge 触发后，仍然可以直接进入 OpenCode Web GUI 接管同一会话和工作目录，继续推进工作，不局限于 GitHub 评论区交互。
- **`可通过 VSCode 远程编辑文件`**：因为代码就在你自己的机器或远端主机 workspace 里，可以直接用 VSCode Remote 等方式连接并编辑，不需要把修改局限在浏览器对话框或 App 沙箱内。(常见于AI无法很好完成工作需要人工介入更改时)
- **自管理 OpenCode 实例**：Agent 运行在你自己的机器或服务器上，仓库、凭据、运行环境、网络访问策略都由你控制，而不是依赖托管式 Web 工作区或第三方 GitHub App 执行环境。
- **可切换任意 AI Provider / Model**：底层通过 OpenCode 对接模型，不绑定单一厂商。可以按仓库、任务类型或成本策略自由选择 provider 和 model。
- **Agent 行为可深度个性化**：你可以自定义 OpenCode 配置、系统提示词、权限策略、技能、工作流和配套服务，而不是受限于固定产品形态。
- **同 issue/pr 聊天记录继承**：在同一个 issue/pr 中，Agent 的多轮交互会复用同一 Session，保持上下文连续性，而不是每次都从零开始。也可通过 -clear 参数手动重置上下文。
- **不只服务 GitHub 页面内流程**：虽然入口是 GitHub webhook，但 Agent 实际运行在真实仓库工作目录中，可继续做 GitHub 之外的本地调试、脚本执行、文档整理和环境排查。
- **更适合长期上下文和多轮协作**：同一 Issue / PR 复用 Agent Session，并结合独立 git workspace 隔离上下文，既保留连续性，也避免不同任务互相污染。

## 架构概览

```
GitHub Webhook --> Bridge --> OpenCode Agent --> GitHub PR/Comment
                    |              ^
                    |              |
                    |              +-- workspace-manager (agent-side HTTP service)
                    |                    +-- 创建/复用独立 git workspace
                    |                    +-- 返回可绑定目录
                    +-- Session 管理 (复用 Agent Session)
                    +-- 任务队列 (异步处理)
```

## 准备工作

### 1. OpenCode 侧准备

**必须在 OpenCode 服务器上完成以下准备：**

#### 1.1 配置 GitHub 鉴权

OpenCode 需要能够推送代码和创建 PR，配置方式：

**方式一：SSH Key（推荐）**
```bash
# 生成 SSH Key
ssh-keygen -t ed25519 -C "opencode@server"

# 将公钥添加到 GitHub
cat ~/.ssh/id_ed25519.pub
# 复制输出，添加到 GitHub Settings -> SSH Keys
```

**方式二：Personal Access Token**
```bash
# 配置 git credential
git config --global credential.helper store

# 首次 push 时输入 token
# Username: your-username
# Password: ghp_xxxx (你的 PAT)
```

#### 1.2 启动 OpenCode Server

**关键：OpenCode 必须在仓库目录中启动**

OpenCode Server 的工作目录就是它能操作的仓库范围。

**单仓库模式：**
```bash
# 在 $WORKSPACE_MANAGER_ROOT/owner/repo 目录启动
# 默认 $WORKSPACE_MANAGER_ROOT=~/.opencode/workspaces，可自己修改
mkdir $WORKSPACE_MANAGER_ROOT/owner/repo
cd $WORKSPACE_MANAGER_ROOT/owner/repo

# 设置鉴权密码（可选但推荐）
export OPENCODE_SERVER_PASSWORD="your-secure-password"

# 启动 server 模式
# 如果想要主动接管任务，启动 web 模式，含有 server 模式的全部功能
opencode serve --port 4096
```

**多仓库模式：为每个仓库启动独立的 OpenCode 实例**
```bash
# 终端 1: 启动 repo1 的 OpenCode
cd $WORKSPACE_MANAGER_ROOT/owner1/repo1
opencode serve --port 4096

# 终端 2: 启动 repo2 的 OpenCode
cd $WORKSPACE_MANAGER_ROOT/owner2/repo2
opencode serve --port 4097

# 可使用 systemd 或 supervisor 管理多个实例
```

#### 1.2.1 可选安装的 OpenCode Skills 与 OpenCode 配置

本仓库在 `skills/` 下提供了建议安装到 OpenCode 侧的 skill，例如：

Bridge 下发的 prompt 可能会显式要求 Agent 优先调用 `github-progress-comment`，然后按任务类型继续调用 `issue-to-pr`、`issue-plan` 或 `pr-review` 等其它 skill。

安装：
```bash
cd /path/to/openagent-github-bridge
ln -sf $(pwd)/skills/* ~/.agents/skills # 软链接

cp opencode.json /path/to/your/repo # 或将配置自己拷贝到用户配置 ~/.config/opencode/opencode.json 中
```

#### 1.3 启动必需的 workspace-manager companion service

**这是必备步骤。**

如果没有启动 agent 侧 `workspace-manager` companion service，Bridge 无法让每个 Issue/PR 在独立的 git workspace 中工作，隔离链路就不会成立。

服务源码在当前仓库：

```text
plugins/workspace-manager/
```

在与 OpenCode 相同的机器上启动。`workspace-manager` 会根据 Bridge 请求里的 `repoURL` 直接从远端 clone 仓库到本地。

示例：

```bash
cd /path/to/openagent-github-bridge

export WORKSPACE_MANAGER_ADDR="127.0.0.1:4081"
export WORKSPACE_MANAGER_ROOT="~/.opencode/workspaces"
export WORKSPACE_MANAGER_BASE_REMOTE="origin"
export WORKSPACE_MANAGER_PASSWORD=""

go run ./plugins/workspace-manager
```

启动后确认服务可用：

```bash
curl http://127.0.0.1:4081/health
```

服务的详细参数和行为说明见：

```text
plugins/workspace-manager/README.md
```

### 2. GitHub 侧准备

#### 2.1 创建 Webhook

在目标仓库或组织设置中创建 Webhook：

1. 进入 Settings -> Webhooks -> Add webhook
2. 配置：
   - **Payload URL**: `https://your-bridge-server:7777/webhook`
   - **Content type**: `application/json`
   - **Secret**: 设置一个安全的密钥（与 Bridge 配置一致）
   - **Events**: 选择以下事件：
     - Issues
     - Issue comments
     - Pull requests
     - Pull request reviews
     - Pull request review comments

#### 2.2 创建 Labels（可选）

在仓库中创建触发标签：

- `ai-fix` - 触发自动修复
- `ai-plan` - 触发方案设计
- `ai-review` - 触发 PR 审查

### 3. Bridge 侧准备

#### 3.1 配置文件

复制并编辑配置文件：

```bash
cp config/config.yaml config/config.local.yaml
```

关键配置项：

```yaml
# 服务配置
server:
  port: 7777

# GitHub Webhook 配置
github:
  webhook_secret: "your-webhook-secret"  # 与 GitHub Webhook 设置一致

# OpenCode 配置
opencode:
  host: "http://localhost:4096"          # OpenCode server 地址
  password: "your-secure-password"        # 与 OpenCode server 一致（可选）
  clone_url: ""                           # 可选：默认仓库克隆地址覆盖，推荐为 SSH URL，否则可能造成克隆失败
  workspace_manager_host: "http://localhost:4081"  # agent 侧 companion service

# 功能开关
features:
  ai_fix:
    enabled: true
    labels:
      - "ai-fix"
    plan_label_trigger_enabled: true
    plan_labels:
      - "ai-plan"
    comment_trigger_enabled: true
    comment_commands:
      - "/go"
  pr_review:
    enabled: false                        # PR 创建时自动 review
    label_trigger_enabled: true           # 标签触发 review
    labels:
      - "ai-review"

# 多仓库模式（可选）
# 当需要支持多个仓库时配置
repositories:
  "owner1/repo1":
    opencode_host: "http://localhost:4096"
    clone_url: "git@github.com:owner1/repo1.git"
    workspace_manager_host: "http://localhost:4081"
  "owner2/repo2":
    opencode_host: "http://localhost:4097"
    clone_url: "git@github.com:owner2/repo2.git"
    workspace_manager_host: "http://localhost:4082"
```

**运行模式说明：**

| 模式 | 配置 | 说明 |
|------|------|------|
| 单仓库 | 只配置 `opencode.host` | 所有仓库使用同一个 OpenCode 实例 |
| 多仓库 | 配置 `repositories` 映射 | 每个仓库路由到对应的 OpenCode 实例 |

多仓库模式下，如果某个仓库没有在 `repositories` 中配置，会 fallback 到默认的 `opencode` 配置。

#### 3.2 环境变量

可通过环境变量覆盖配置：

```bash
export GITHUB_WEBHOOK_SECRET="your-webhook-secret"
export OPENCODE_HOST="http://localhost:4096"
export OPENCODE_SERVER_PASSWORD="your-secure-password"
export OPENCODE_CLONE_URL=""
export WORKSPACE_MANAGER_HOST="http://localhost:4081"
export WORKSPACE_MANAGER_PASSWORD=""
```

### 4. 启动服务

#### 开发模式

```bash
cd openagent-github-bridge
go run cmd/server/main.go
```

#### 生产模式

```bash
# 构建
make build

# 运行
./bin/bridge -config config/config.local.yaml
```

#### Docker 模式

```bash
# 构建镜像
docker build -t openagent-bridge .

# 运行
docker run -d \
  -p 7777:7777 \
  -e GITHUB_WEBHOOK_SECRET="xxx" \
  -e OPENCODE_HOST="http://host.docker.internal:4096" \
  -e WORKSPACE_MANAGER_HOST="http://host.docker.internal:4081" \
  openagent-bridge
```

#### 暴露服务
- 使用 ngrok
```bash
ngrok http 7777
```
- 部署到服务器

## 工作流程

### AI-Fix 流程

1. 用户在 Issue 上添加 `ai-fix` 标签
2. GitHub 发送 webhook 到 Bridge
3. Bridge 验证签名，解析事件
4. Bridge 创建/获取 Session（复用同一 Issue 的 Session）
5. Bridge 调用 agent 侧 workspace-manager：
   - 如果是新 Session：创建或复用独立 workspace（按 Bridge 解析后的仓库远端地址 clone，一般基于 `origin/main` 创建 `issue-{number}`）
   - 返回 `worktreePath`
6. Bridge 调用 OpenCode：
   - 创建正式 session，并绑定到返回的 `worktreePath`
   - session title 在原有 session key 后追加本地时间戳，形如 `20260417-163030`
   - 发送 prompt（包含 Issue 内容和仓库信息）
7. OpenCode 独立工作：
   - 分析 Issue
   - 在 workspace 中修改代码
   - 创建 PR（包含 `Fixes #{number}`）

### AI-Plan / Slash Coding 流程

1. 用户在 Issue 上添加 `ai-plan` 标签，或在 Issue 评论中以 `/go` 开头发起指令
2. GitHub 发送 webhook 到 Bridge
3. Bridge 根据触发方式选择 skill：
   - `ai-plan` -> `issue-plan`
   - `/go` -> `issue-to-pr`
4. Agent 执行对应流程：
   - `ai-plan` 只产出方案，不编码
   - `/go` 读取 issue 与补充说明，编码并创建 PR
5. Bridge 根据触发方式选择 OpenCode agent：
   - `ai-plan` -> `plan`
   - `ai-fix` / `/go` -> `build`

### PR Review 流程

1. 用户创建 PR 或添加 `ai-review` 标签
2. GitHub 发送 webhook 到 Bridge
3. Bridge 调用 agent 侧 workspace-manager：
   - 创建或复用 workspace，并在每次下发前对齐到当前 PR 的最新 head SHA（分支名 `pr-{number}`）
   - 返回 `worktreePath`
4. Bridge 调用 OpenCode：
   - 新 Session：创建正式 session，并绑定到返回的 `worktreePath`
   - 已有 Session：复用原 session，但 workspace 已先刷新到最新 PR head
   - Agent 必须直接在该 workspace 中工作，不再执行 `git checkout` / `git switch` / `gh pr checkout`
   - 发送 review prompt
5. OpenCode 独立工作：
   - 直接使用已准备好的 PR workspace
   - 进行代码审查
   - 提交 review comments

## 配置参考

| 配置项 | 环境变量 | 默认值 | 说明 |
|--------|----------|--------|------|
| `server.port` | `SERVER_PORT` | `7777` | 服务端口 |
| `github.webhook_secret` | `GITHUB_WEBHOOK_SECRET` | - | Webhook 签名密钥 |
| `opencode.host` | `OPENCODE_HOST` | `http://localhost:4096` | OpenCode 地址 |
| `opencode.password` | `OPENCODE_SERVER_PASSWORD` | - | OpenCode 鉴权密码 |
| `opencode.clone_url` | `OPENCODE_CLONE_URL` | - | 默认仓库克隆地址覆盖 |
| `opencode.workspace_manager_host` | `WORKSPACE_MANAGER_HOST` | `http://localhost:4081` | agent 侧 workspace-manager 地址 |
| `opencode.workspace_manager_password` | `WORKSPACE_MANAGER_PASSWORD` | - | workspace-manager 鉴权密码 |
| `features.ai_fix.enabled` | - | `true` | 启用 AI-Fix |
| `features.pr_review.enabled` | - | `false` | 启用 PR 自动 Review |
| `features.pr_review.label_trigger_enabled` | - | `true` | 启用标签触发 Review |

## 故障排查

### Webhook 未触发

1. 检查 GitHub Webhook 配置是否正确
2. 检查 Webhook Secret 是否一致
3. 查看 GitHub Webhook 的 Recent Deliveries

### OpenCode 连接失败

1. 检查 OpenCode server 是否运行：`curl http://localhost:4096/global/health`
2. 检查鉴权配置是否一致
3. 检查网络连通性

### workspace-manager 连接失败

1. 检查 companion service 是否运行：`curl http://localhost:4081/health`
2. 检查 `workspace_manager_host` 配置是否与实际端口一致
3. 如果启用了 Basic Auth，检查 `workspace_manager_username/password` 是否一致
4. 检查 Bridge 最终传给 workspace-manager 的 `repoURL` 是否正确

### Workspace 创建失败

1. 确认 workspace-manager 对仓库和 `~/.opencode/workspaces` 有写权限
2. 检查目标 `repoURL` 是否可从 workspace-manager 所在机器直接 clone
3. 检查目标 ref 是否存在，以及是否存在残留同名 workspace 目录

### PR 创建失败

1. 检查 OpenCode 的 GitHub 鉴权配置
2. 确认 PAT 有 `repo` 权限
3. 查看 OpenCode 日志

## 开发指南

详见 [AGENTS.md](./AGENTS.md)

## License

MIT

## 友链

- [Linux.do](https://linux.do/)
