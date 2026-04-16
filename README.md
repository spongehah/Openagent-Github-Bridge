# OpenAgent GitHub Bridge

GitHub Webhook 到 AI Agent 的桥接服务，支持自动修复 Issue 和 PR Review。

## 功能特性

- **AI-Fix**: Issue 被打上 `ai-fix` 标签时，自动触发 AI 分析并创建修复 PR
- **PR Review**: PR 创建或被打上 `ai-review` 标签时，自动进行代码审查
- **Session 复用**: 同一 Issue/PR 的多次交互复用同一 Agent Session，保持上下文
- **Git Worktree 隔离**: 每个 Issue/PR 在独立的 git worktree 中工作，互不干扰
- **Fire-and-Forget**: Bridge 只负责下发任务，Agent 独立完成工作

## 架构概览

```
GitHub Webhook --> Bridge --> OpenCode Agent --> GitHub PR/Comment
                    |              ^
                    |              |
                    |              +-- worktree-manager (agent-side HTTP service)
                    |                    +-- 创建/复用 git worktree
                    |                    +-- 返回可绑定目录
                    +-- Session 管理 (复用 Agent Session)
                    +-- 任务队列 (异步处理)
```

## 准备工作

### 1. OpenCode 侧准备

**必须在 OpenCode 服务器上完成以下准备：**

#### 1.1 预先 Clone 目标仓库

OpenCode 需要在本地预先 clone 所有需要处理的仓库：

```bash
# 创建仓库目录（推荐结构）
mkdir -p ~/repos/{owner}/{repo}

# Clone 仓库
cd ~/repos/{owner}
git clone https://github.com/{owner}/{repo}.git

# 示例
mkdir -p ~/repos/myorg
cd ~/repos/myorg
git clone https://github.com/myorg/myapp.git
```

#### 1.2 配置 GitHub 鉴权

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

#### 1.3 启动 OpenCode Server

**关键：OpenCode 必须在仓库目录中启动**

OpenCode Server 的工作目录就是它能操作的仓库范围。

**单仓库模式：**
```bash
# 进入仓库目录
cd ~/repos/myorg/myapp

# 设置鉴权密码（可选但推荐）
export OPENCODE_SERVER_PASSWORD="your-secure-password"

# 启动 server 模式
opencode server --port 4096
```

**多仓库模式：为每个仓库启动独立的 OpenCode 实例**
```bash
# 终端 1: 启动 repo1 的 OpenCode
cd ~/repos/owner1/repo1
opencode server --port 4096

# 终端 2: 启动 repo2 的 OpenCode
cd ~/repos/owner2/repo2
opencode server --port 4097

# 可使用 systemd 或 supervisor 管理多个实例
```

#### 1.4 启动必需的 worktree-manager companion service

**这是必备步骤。**

如果没有启动 agent 侧 `worktree-manager` companion service，Bridge 无法让每个 Issue/PR 在独立的 git worktree 中工作，worktree 隔离链路就不会成立。

服务源码在当前仓库：

```text
plugins/worktree-manager/
```

推荐在与 OpenCode 相同的机器上启动，且 `WORKTREE_MANAGER_REPO_ROOT` 必须指向当前 OpenCode 实例对应的仓库根目录。

示例：

```bash
cd /path/to/openagent-github-bridge

export WORKTREE_MANAGER_REPO_ROOT="/Users/example/repos/openagent/github-bridge"
export WORKTREE_MANAGER_ADDR="127.0.0.1:4081"
export WORKTREE_MANAGER_PASSWORD=""

go run ./plugins/worktree-manager
```

编译运行：

```bash
go build -o ./bin/worktree-manager ./plugins/worktree-manager

WORKTREE_MANAGER_REPO_ROOT="/Users/example/repos/openagent/github-bridge" \
WORKTREE_MANAGER_ADDR="127.0.0.1:4081" \
./bin/worktree-manager
```

启动后确认服务可用：

```bash
curl http://127.0.0.1:4081/health
```

服务的详细参数和行为说明见：

```text
plugins/worktree-manager/README.md
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
  worktree_manager_host: "http://localhost:4081"  # agent 侧 companion service

# 功能开关
features:
  ai_fix:
    enabled: true
    labels:
      - "ai-fix"
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
    worktree_manager_host: "http://localhost:4081"
  "owner2/repo2":
    opencode_host: "http://localhost:4097"
    worktree_manager_host: "http://localhost:4082"
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
export WORKTREE_MANAGER_HOST="http://localhost:4081"
export WORKTREE_MANAGER_PASSWORD=""
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
  -e WORKTREE_MANAGER_HOST="http://host.docker.internal:4081" \
  openagent-bridge
```

## 工作流程

### AI-Fix 流程

1. 用户在 Issue 上添加 `ai-fix` 标签
2. GitHub 发送 webhook 到 Bridge
3. Bridge 验证签名，解析事件
4. Bridge 创建/获取 Session（复用同一 Issue 的 Session）
5. Bridge 调用 agent 侧 worktree-manager：
   - 如果是新 Session：创建或复用 worktree（基于默认分支，分支名 `issue-{number}`）
   - 返回 `worktreePath`
6. Bridge 调用 OpenCode：
   - 创建正式 session，并绑定到返回的 `worktreePath`
   - 发送 prompt（包含 Issue 内容和仓库信息）
7. OpenCode 独立工作：
   - 分析 Issue
   - 在 worktree 中修改代码
   - 创建 PR（包含 `Fixes #{number}`）

### PR Review 流程

1. 用户创建 PR 或添加 `ai-review` 标签
2. GitHub 发送 webhook 到 Bridge
3. Bridge 调用 agent 侧 worktree-manager：
   - 如果是新 Session：创建或复用 worktree（基于 PR head ref / head SHA，分支名 `pr-{number}`）
   - 返回 `worktreePath`
4. Bridge 调用 OpenCode：
   - 创建正式 session，并绑定到返回的 `worktreePath`
   - 发送 review prompt
5. OpenCode 独立工作：
   - 检出 PR 代码
   - 进行代码审查
   - 提交 review comments

## 配置参考

| 配置项 | 环境变量 | 默认值 | 说明 |
|--------|----------|--------|------|
| `server.port` | `SERVER_PORT` | `7777` | 服务端口 |
| `github.webhook_secret` | `GITHUB_WEBHOOK_SECRET` | - | Webhook 签名密钥 |
| `opencode.host` | `OPENCODE_HOST` | `http://localhost:4096` | OpenCode 地址 |
| `opencode.password` | `OPENCODE_SERVER_PASSWORD` | - | OpenCode 鉴权密码 |
| `opencode.worktree_manager_host` | `WORKTREE_MANAGER_HOST` | `http://localhost:4081` | agent 侧 worktree-manager 地址 |
| `opencode.worktree_manager_password` | `WORKTREE_MANAGER_PASSWORD` | - | worktree-manager 鉴权密码 |
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

### worktree-manager 连接失败

1. 检查 companion service 是否运行：`curl http://localhost:4081/health`
2. 检查 `worktree_manager_host` 配置是否与实际端口一致
3. 检查 `WORKTREE_MANAGER_REPO_ROOT` 是否指向正确仓库根目录
4. 如果启用了 Basic Auth，检查 `worktree_manager_username/password` 是否一致

### Worktree 创建失败

1. 确认 worktree-manager 对仓库和 `~/.opencode/worktrees` 有写权限
2. 检查目标 ref 是否已经 fetch 到本地，以及是否存在残留同名 worktree
3. 检查 companion service 的 `WORKTREE_MANAGER_REPO_ROOT` 是否和 OpenCode 实例对应同一个仓库

### PR 创建失败

1. 检查 OpenCode 的 GitHub 鉴权配置
2. 确认 PAT 有 `repo` 权限
3. 查看 OpenCode 日志

## 开发指南

详见 [AGENTS.md](./AGENTS.md)

## License

MIT
