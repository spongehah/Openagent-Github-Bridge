# AGENTS.md - OpenAgent GitHub Bridge

> 本文件是 AI Agent 和开发者的协作指南，记录了项目的核心约定、架构决策和开发原则。

---

## 项目概述

**OpenAgent GitHub Bridge** 是一个 Go 后端服务，作为 GitHub Webhooks 和 AI Agent（如 OpenCode）之间的桥梁。它监听 GitHub 事件，根据配置触发 AI Agent 执行任务（如自动修复 Issue、Review PR）。

### 核心设计理念

```
GitHub Webhook → Bridge (验证/解析/下发) → AI Agent (执行/创建PR)
                      ↓
              Fire-and-Forget
              (下发即返回，不等待结果)
```

---

## 核心约定

### 1. Fire-and-Forget 模式

**Bridge 只负责下发任务，不等待 Agent 响应。**

- Webhook 收到请求 → 验证签名 → 解析事件 → 放入队列 → **立即返回 202**
- Worker 从队列取出 → 构建 Prompt → 调用 Agent API → **立即返回**（不等待执行结果）
- Agent 自己负责：执行任务、与 GitHub 交互、创建 PR、发评论

```go
// 正确 ✓ - Fire-and-forget
func (a *OpenCodeAdapter) DispatchTask(ctx context.Context, req *TaskRequest) (*TaskResponse, error) {
    // 调用 /session/:id/prompt_async，返回 204 No Content
    // 不等待 Agent 执行完成
}

// 错误 ✗ - 同步等待
func (a *OpenCodeAdapter) DispatchTask(ctx context.Context, req *TaskRequest) (*TaskResponse, error) {
    // 等待 Agent 返回完整响应
    // 这会阻塞 Worker
}
```

### 2. 配置开关原则

**只有实际触发动作的事件才需要 enable 配置。**

```yaml
# 正确 ✓ - 有实际动作的事件
features:
  ai_fix:
    enabled: true  # Issue 被打上标签 → 触发自动修复
  pr_review:
    enabled: false              # PR 创建 → 触发自动 review
    label_trigger_enabled: true # PR 被打标签 → 触发 review

# 错误 ✗ - 没有实际动作的事件不需要配置
features:
  issue_opened:
    enabled: true  # 如果没有对应动作，不要添加这个配置
```

**新增功能时的检查清单：**
- [ ] 这个事件监听后会触发什么动作？
- [ ] 如果有动作，添加对应的 `enabled` 配置
- [ ] 如果只是数据收集/日志，不需要配置

### 3. Git Worktree 隔离

**每个 Issue/PR 的工作 Session 应该在独立的 Git Worktree 中。**

- 下发任务前，Bridge 会指示 Agent 创建 worktree
- Worktree 分支命名：`issue-{number}` 或 `pr-{number}`
- 不同 Session 不会相互干扰

```go
// 创建 worktree 的流程
1. Bridge 调用 agent 侧 `worktree-manager` companion service
2. companion service 创建或复用 git worktree，并返回 `worktreePath`
3. Bridge 创建正式 OpenCode session，绑定到该目录
4. Bridge 下发任务到该 worktree session
```

### 4. Session 管理与复用

**以 `{owner}/{repo}/{type}/{number}` 为唯一 Key 管理 Session，同一 Issue/PR 复用 Agent Session。**

```go
// Session Key 格式
type SessionKey struct {
    Owner  string  // e.g., "openagent"
    Repo   string  // e.g., "github-bridge"
    Type   string  // "issue" or "pr"
    Number int     // e.g., 123
}

// 同一 Issue/PR 的所有交互复用同一 Session
key := SessionKey{Owner: "openagent", Repo: "bridge", Type: "issue", Number: 42}
// Key: "openagent/bridge/issue/42"

// Session 复用流程
1. 首次交互：创建新 Session，创建 Worktree，保存 Agent Session ID
2. 后续交互：复用已有 Agent Session ID，不重新创建 Worktree
3. Session 过期后（默认 24h）：重新创建
```

### 5. API 参考注释

**所有外部 API 调用必须在注释中标明参考来源。**

```go
// 正确 ✓
// VerifySignature validates the webhook payload signature using HMAC-SHA256.
// Reference: https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
func VerifySignature(payload []byte, signature, secret string) bool {

// IssueEvent represents an issue event payload.
// Reference: https://docs.github.com/en/webhooks/webhook-events-and-payloads#issues
type IssueEvent struct {

// DispatchTask sends a task to OpenCode server.
// Reference: https://open-code.ai/docs/en/server#post-session-id-prompt_async
func (a *OpenCodeAdapter) DispatchTask(ctx context.Context, req *TaskRequest) error {
```

**参考文档来源：**
- GitHub Webhooks: https://docs.github.com/en/webhooks
- GitHub API: https://docs.github.com/en/rest
- OpenCode Server: https://open-code.ai/docs/en/server
- OpenCode SDK: https://opencode.ai/docs/sdk

### 6. 多仓库支持

**OpenCode 必须在仓库目录中启动，每个仓库需要独立的 OpenCode 实例。**

```yaml
# 单仓库模式 - 只配置默认 OpenCode
opencode:
  host: "http://localhost:4096"

# 多仓库模式 - 为每个仓库配置独立的 OpenCode
repositories:
  "owner1/repo1":
    opencode_host: "http://localhost:4096"
    worktree_manager_host: "http://localhost:4081"
  "owner2/repo2":
    opencode_host: "http://localhost:4097"
    worktree_manager_host: "http://localhost:4082"
```

**启动 OpenCode 的正确方式：**
```bash
# 必须在仓库目录中启动
cd ~/repos/owner1/repo1 && opencode server --port 4096
cd ~/repos/owner2/repo2 && opencode server --port 4097
```

**路由逻辑：**
```go
// MultiRepoOpenCodeAdapter 根据 repo 路由到对应的 OpenCode 实例
func (m *MultiRepoOpenCodeAdapter) DispatchTask(ctx context.Context, task TaskContext) {
    adapter := m.getOrCreateAdapter(task.RepoOwner, task.RepoName)
    return adapter.DispatchTask(ctx, task)
}
```

### 7. 鉴权配置

**支持可配置鉴权，未配置时作为无鉴权处理。**

```go
// OpenCode HTTP Basic Auth
type OpenCodeConfig struct {
    Host     string // Server URL
    Username string // Default: "opencode"
    Password string // Empty = no auth
}

// 鉴权逻辑
func (a *OpenCodeAdapter) setAuth(req *http.Request) {
    if a.config.Password != "" {
        req.SetBasicAuth(a.config.Username, a.config.Password)
    }
    // Password 为空时，不设置 Auth header
}
```

---

## 目录结构

```
openagent-github-bridge/
├── cmd/server/main.go           # 应用入口
├── internal/
│   ├── config/config.go         # 配置管理（含多仓库映射）
│   ├── agent/
│   │   ├── interface.go         # Agent 接口定义
│   │   ├── opencode.go          # OpenCode 适配器（单实例）
│   │   └── multi_repo.go        # 多仓库路由适配器
│   ├── github/
│   │   ├── client.go            # GitHub API 客户端
│   │   └── webhook.go           # Webhook 签名验证
│   ├── handler/webhook.go       # HTTP 处理器
│   ├── queue/task.go            # 异步任务队列
│   ├── session/manager.go       # Session 管理器
│   └── service/bridge.go        # 核心业务逻辑
├── config/config.yaml           # 配置文件
├── skills/                      # (Future) Agent 技能目录
└── AGENTS.md                    # 本文件
```

---

## 开发规范

### 添加新的事件监听

1. **在 `github/webhook.go` 中添加事件结构**
   ```go
   // NewEventType represents a new event payload.
   // Reference: https://docs.github.com/en/webhooks/webhook-events-and-payloads#new_event
   type NewEventType struct {
       Action string `json:"action"`
       // ...
   }
   ```

2. **在 `handler/webhook.go` 中处理事件**
   ```go
   case *ghwebhook.NewEventType:
       if event.Action != "target_action" {
           return nil, nil
       }
       return &queue.Task{
           Type: queue.TaskTypeNewEvent,
           // ...
       }, nil
   ```

3. **如果需要触发动作，在 `config/config.go` 中添加 enable 配置**
   ```go
   type NewFeatureConfig struct {
       Enabled bool `mapstructure:"enabled"`
   }
   ```

4. **在 `service/bridge.go` 中添加处理逻辑和 Prompt 构建**

### 添加新的 Agent 适配器

1. 实现 `agent.Agent` 接口
2. 支持 Fire-and-Forget 模式
3. 添加 API 参考注释
4. 支持可选鉴权

```go
// Agent defines the interface for AI coding agents.
type Agent interface {
    // DispatchTask sends a task to the agent (fire-and-forget).
    // Returns immediately after dispatching, does not wait for completion.
    DispatchTask(ctx context.Context, req *TaskRequest) (*TaskResponse, error)
    
    // Ping checks if the agent is available.
    Ping(ctx context.Context) error
}
```

---

## 配置参考

```yaml
# 功能开关 - 只有触发动作的事件需要 enable
features:
  ai_fix:
    enabled: true
    labels: ["ai-fix"]
  pr_review:
    enabled: false
    skip_draft_prs: true
    skip_bot_prs: true
    label_trigger_enabled: true
    labels: ["ai-review"]

# OpenCode 配置
opencode:
  host: "http://localhost:4096"
  username: "opencode"
  password: ""  # Empty = no auth

# 服务配置
server:
  port: 7777  # 默认端口

# 触发配置
trigger:
  prefix: "@ogb-bot"
  respond_all_issues: false
```

---

## 未来规划

### Skills 目录

```
skills/
├── fix-bug.md           # Bug 修复技能
├── add-feature.md       # 功能开发技能
├── code-review.md       # 代码审查技能
└── refactor.md          # 代码重构技能
```

Skills 是预定义的 Prompt 模板，用于提升 Agent 在特定任务上的能力。

---

## 调试

```go
// 使用 [v0] 前缀的 console.log 进行调试
log.Printf("[v0] Task dispatched: %s", task.ID)
log.Printf("[v0] Session key: %s", sess.Key.String())

// 调试完成后移除
```

---

## 变更日志

| 日期 | 变更 | 原因 |
|------|------|------|
| 2026-04-15 | 初始架构 | 项目创建 |
| 2026-04-15 | Fire-and-forget 模式 | Bridge 不等待 Agent 响应 |
| 2026-04-15 | 配置开关原则 | 只有实际动作的事件需要 enable |
| 2026-04-15 | Git Worktree 支持 | 每个 Issue/PR 独立工作区 |
| 2026-04-15 | PR Review 功能 | 支持 opened 和 labeled 触发 |
| 2026-04-15 | 多仓库支持 | 支持 repo -> OpenCode 实例映射 |
| 2026-04-15 | Session 复用 | 同一 Issue/PR 复用 Agent Session |
