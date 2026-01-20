# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build Commands

```bash
# Build everything (backend + frontend)
make build

# Backend only (outputs to backend/bin/server)
cd backend && make build

# Frontend only (outputs to backend/internal/web/dist/)
cd frontend && pnpm install && pnpm run build

# Build backend with embedded frontend (production binary)
cd backend && go build -tags embed -o sub2api ./cmd/server
```

## Testing Commands

```bash
# All backend tests + linting
cd backend && make test

# Unit tests only
cd backend && make test-unit

# Integration tests only (uses testcontainers for PostgreSQL/Redis)
cd backend && make test-integration

# E2E tests
cd backend && make test-e2e

# Run a single test
cd backend && go test -v -run TestFunctionName ./path/to/package

# Frontend linting and type checking
cd frontend && pnpm run lint:check
cd frontend && pnpm run typecheck
```

## Development

```bash
# Backend with hot reload
cd backend && go run ./cmd/server

# Frontend dev server
cd frontend && pnpm run dev
```

## Code Generation

When editing `backend/ent/schema/*.go`, regenerate Ent and Wire:

```bash
cd backend
go generate ./ent
go generate ./cmd/server
```

## Architecture Overview

Sub2API is an AI API Gateway that distributes API quotas from AI subscriptions (Claude, Gemini, OpenAI) to users via platform-generated API keys.

### Backend Structure (`backend/`)

**Layered Architecture with Wire DI:**
- `cmd/server/` - Entry point and Wire dependency injection
- `internal/config/` - YAML + env configuration loading
- `internal/repository/` - Data access layer (Ent ORM, Redis caching)
- `internal/service/` - Business logic (40+ services)
- `internal/handler/` - HTTP handlers (Gin)
- `internal/server/` - HTTP server setup and routing
- `ent/schema/` - Database entity definitions

**Key Services:**
- `GatewayService` - Core request routing, account selection, streaming
- `BillingService` - Token counting, cost calculation, balance deduction
- `ConcurrencyService` - Per-user/per-account request slot management
- `AccountService` - Account CRUD and scheduling state management
- Platform-specific: `OpenAIGatewayService`, `GeminiMessagesCompatService`, `AntigravityGatewayService`

**Request Flow:**
1. Request → Handler extracts/validates API key
2. Check billing balance and subscription status
3. Reserve concurrency slot
4. Select account via scheduling algorithm (supports sticky sessions via `metadata.user_id`)
5. Forward to upstream, stream response back
6. Log usage, deduct balance, release slot

**Database Entities (Ent ORM):**
- `User` - Platform users with roles, balance, concurrency limits
- `Account` - Upstream AI accounts with credentials and scheduling state
- `Group` - Account groups with subscription types and rate limits
- `APIKey` - User API keys mapped to groups
- `UserSubscription` - User subscriptions tracking quota and costs
- `UsageLog` - Per-request token usage records

### Frontend Structure (`frontend/`)

Vue 3 + Vite + TailwindCSS + Pinia:
- `src/api/` - API client modules
- `src/stores/` - Pinia state management (auth, app, subscriptions)
- `src/views/` - Page components (auth, admin, user dashboards)
- `src/composables/` - Vue composition utilities
- `src/router/` - Route definitions with lazy loading

### Configuration

Config sources: `config.yaml` file or environment variables (e.g., `SERVER_PORT`, `DATABASE_URL`, `REDIS_ADDR`).

**Run Modes:**
- `standard` - Full billing and quota enforcement
- `simple` - Billing disabled (set `RUN_MODE=simple`)

### Account Scheduling

Accounts track availability via time-based flags:
- `schedulable` - Account included/excluded from selection
- `rate_limited_at` / `rate_limit_reset_at` - 429 handling
- `overload_until` - 529 temporary disable
- `expires_at` - Account expiration

### Testing Infrastructure

- Unit tests tagged with `unit`
- Integration tests tagged with `integration` (use testcontainers-go for real PostgreSQL/Redis)
- E2E tests tagged with `e2e`

## 合并上游代码注意事项

**重要：合并 upstream/main 时必须特别注意以下文件的冲突处理：**

### ⚠️ 核心原则（必须严格遵守）

1. **本地二开功能最重要** - 必须保留所有本地二开的功能
2. **合并前必须先列出本地二开提交** - 用 `git log --oneline upstream/main..HEAD` 查看
3. **每次修改后必须运行 `cd backend && make test`** - 不是 `go test`，要用 make test
4. **遇到冲突必须合并双方内容** - 绝不能简单选择一方覆盖另一方

### 🔴 明确区分：本地版本 vs 上游版本

**必须保留本地版本的功能（二开功能）：**
- 账号归档 (Archived) - `account.go`, `mappers.go`, `account_handler.go`
- 账号 RPM 限制 - `MaxRPM`, `Max30mRequests`, `RateLimitCooldownMinutes`
- 分组模型白名单 - `AllowedModels`, `ModelMapping`
- Claude Code 验证器字符串格式支持 - `claude_code_validator.go` 中的 `hasClaudeCodeSystemPrompt`
- 400 disabled organization 错误处理 - `ratelimit_service.go` 中使用 `upstreamMsg` 而非 `isAccountDisabledError`
- Session mutex 等待机制 - `gateway_handler.go`, `gateway_helper.go`
- 公告可点击链接 - `AnnouncementBanner.vue`
- gateway 翻译 - `zh.ts`, `en.ts` 中的 `admin.settings.gateway.*`

**使用上游版本的功能：**
- Antigravity 调度和错误处理 - `antigravity_gateway_service.go`（上游有更好的 bug 修复）
- 不要使用上游的 Session ID masking (`RewriteUserIDWithMasking`)，保留本地 session 实现

### 合并流程（必须严格遵循）

```bash
# 1. 合并前：列出所有本地二开提交
git log --oneline upstream/main..HEAD

# 2. 开始合并
git fetch upstream
git merge upstream/main --no-commit

# 3. 对每个冲突文件：
#    - 检查是否涉及本地二开功能
#    - 如果涉及，必须保留本地功能 + 上游新增功能
#    - 用 git diff HEAD -- <file> 确认改动正确

# 4. 每次解决冲突后立即测试（用 make test，不是 go test）
cd backend && make test

# 5. 重新生成 Ent 和 Wire（如果修改了 schema）
cd backend && go generate ./ent && go generate ./cmd/server

# 6. 最终验证
cd backend && make test
cd frontend && npm run typecheck
```

### 高风险文件详细清单

| 文件 | 本地二开功能 | 处理方式 |
|------|-------------|---------|
| `ratelimit_service.go` | 400 disabled organization 处理用 `upstreamMsg` | 保留本地版本 |
| `claude_code_validator.go` | 支持字符串格式 system 字段 | 保留本地版本 |
| `antigravity_gateway_service.go` | - | **使用上游版本** |
| `gateway_service.go` | 本地 session 实现，5参数 SelectAccountWithLoadAwareness | 保留本地版本 |
| `identity_service.go` | 本地 session 实现，无 RewriteUserIDWithMasking | 保留本地版本 |
| `dto/mappers.go` | Archived, MaxRPM 等字段 | 合并双方 |
| `account.go` (service) | Archived 字段和方法 | 合并双方 |
| `group.go` (service) | AllowedModels, ModelMapping | 合并双方 |
| `types/index.ts` | 前端类型定义 | 合并双方 |
| `zh.ts` / `en.ts` | gateway, archive 等翻译 | 合并双方 |

### 恢复文件到特定版本的正确命令

```bash
# 恢复到上游版本（从合并提交中提取）
git show <merge-commit>:backend/path/to/file > backend/path/to/file

# 恢复到本地版本（合并前的 HEAD）
git show HEAD^:backend/path/to/file > backend/path/to/file

# ⚠️ 错误示范：git checkout HEAD~2 -- file （可能恢复到错误的版本）
```

### 翻译文件 (i18n)

`frontend/src/i18n/locales/zh.ts` 和 `en.ts`：

- **问题**：本地二开功能的翻译会被上游覆盖
- **正确做法**：合并时保留双方的翻译内容
- **本地二开的翻译 key**：
  - `admin.settings.gateway.*` - 网关设置
  - `admin.accounts.archive*` / `admin.accounts.bulkArchived*` - 账号归档
  - `admin.accounts.bulkActions.archive` / `unarchive` - 批量归档按钮
  - `admin.announcements.*` - 公告管理

### 数据库迁移文件

- 本地迁移文件不要被上游覆盖
- 注意迁移文件的时间戳顺序

### 生产环境警告

- 服务器 IP: 144.34.206.47
- 服务通过 docker 运行
- **禁止**在生产环境添加调试日志、重启容器等高风险操作
