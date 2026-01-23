package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	pkgerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// GatewayHandler handles API gateway requests
type GatewayHandler struct {
	gatewayService            *service.GatewayService
	geminiCompatService       *service.GeminiMessagesCompatService
	antigravityGatewayService *service.AntigravityGatewayService
	userService               *service.UserService
	billingCacheService       *service.BillingCacheService
	concurrencyHelper         *ConcurrencyHelper
	maxAccountSwitches        int
	maxAccountSwitchesGemini  int
}

// NewGatewayHandler creates a new GatewayHandler
func NewGatewayHandler(
	gatewayService *service.GatewayService,
	geminiCompatService *service.GeminiMessagesCompatService,
	antigravityGatewayService *service.AntigravityGatewayService,
	userService *service.UserService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	cfg *config.Config,
) *GatewayHandler {
	pingInterval := time.Duration(0)
	maxAccountSwitches := 10
	maxAccountSwitchesGemini := 3
	if cfg != nil {
		pingInterval = time.Duration(cfg.Concurrency.PingInterval) * time.Second
		if cfg.Gateway.MaxAccountSwitches > 0 {
			maxAccountSwitches = cfg.Gateway.MaxAccountSwitches
		}
		if cfg.Gateway.MaxAccountSwitchesGemini > 0 {
			maxAccountSwitchesGemini = cfg.Gateway.MaxAccountSwitchesGemini
		}
	}
	return &GatewayHandler{
		gatewayService:            gatewayService,
		geminiCompatService:       geminiCompatService,
		antigravityGatewayService: antigravityGatewayService,
		userService:               userService,
		billingCacheService:       billingCacheService,
		concurrencyHelper:         NewConcurrencyHelper(concurrencyService, SSEPingFormatClaude, pingInterval),
		maxAccountSwitches:        maxAccountSwitches,
		maxAccountSwitchesGemini:  maxAccountSwitchesGemini,
	}
}

// Messages handles Claude API compatible messages endpoint
// POST /v1/messages
func (h *GatewayHandler) Messages(c *gin.Context) {
	// 从context获取apiKey和user（ApiKeyAuth中间件已设置）
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	// 读取请求体
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}
	// 检查是否为 Claude Code 客户端，设置到 context 中
	SetClaudeCodeClientContext(c, body)
	setOpsRequestContext(c, "", false, body)
	parsedReq, err := service.ParseGatewayRequest(body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	reqModel := parsedReq.Model
	reqStream := parsedReq.Stream
	setOpsRequestContext(c, reqModel, reqStream, body)
	// 【全局设置优先】检查是否要求仅允许 Claude Code 客户端
	// 跳过强制平台（如 /antigravity/v1/*）的检查
	// 增强验证：除了基础的 Claude Code 客户端检测，还验证 headers
	if !middleware2.HasForcePlatform(c) && h.gatewayService.IsGlobalClaudeCodeRequired(c.Request.Context()) {
		isClaudeCode := service.IsClaudeCodeClient(c.Request.Context())
		headersValid := ValidateClaudeCodeHeaders(c)
		if !isClaudeCode || !headersValid {
			var reason string
			if !isClaudeCode && !headersValid {
				reason = "validator+headers"
			} else if !isClaudeCode {
				reason = "validator"
			} else {
				reason = "headers"
			}
			log.Printf("Rejected non-Claude-Code request (global setting): user_id=%d, ua=%s, x-app=%s, version=%s, beta=%s, reason=%s",
				apiKey.UserID, c.GetHeader("User-Agent"), c.GetHeader("X-App"), c.GetHeader("anthropic-version"), c.GetHeader("anthropic-beta"), reason)
			h.errorResponse(c, http.StatusForbidden, "access_denied", "Only Claude Code clients are allowed. Please use the official Claude Code CLI.")
			return
		}
	}
	// 验证 model 必填
	if reqModel == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	// 【新增】检查分组模型白名单
	if apiKey.Group != nil && !apiKey.Group.IsModelAllowed(reqModel) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("model '%s' is not allowed in this group", reqModel))
		return
	}
	// 【新增】应用分组模型映射
	if apiKey.Group != nil {
		mappedModel := apiKey.Group.MapModel(reqModel)
		if mappedModel != reqModel {
			log.Printf("Model mapping applied: %s -> %s (group_id=%d)", reqModel, mappedModel, apiKey.Group.ID)
			parsedReq.Model = mappedModel
			reqModel = mappedModel
		}
	}
	// Track if we've started streaming (for error handling)
	streamStarted := false
	// 获取订阅信息（可能为nil）- 提前获取用于后续检查
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	// 0. 检查wait队列是否已满
	maxWait := service.CalculateMaxWait(subject.Concurrency)
	canWait, err := h.concurrencyHelper.IncrementWaitCount(c.Request.Context(), subject.UserID, maxWait)
	waitCounted := false
	if err != nil {
		log.Printf("Increment wait count failed: %v", err)
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Internal error checking wait queue")
		return
	}
	if !canWait {
		h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later")
		return
	}
	if err == nil && canWait {
		waitCounted = true
	}
	// Ensure we decrement if we exit before acquiring the user slot.
	defer func() {
		if waitCounted {
			h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), subject.UserID)
		}
	}()
	// 1. 首先获取用户并发槽位
	userReleaseFunc, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
	if err != nil {
		log.Printf("User concurrency acquire failed: %v", err)
		h.handleConcurrencyError(c, err, "user", streamStarted)
		return
	}
	// User slot acquired: no longer waiting in the queue.
	if waitCounted {
		h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), subject.UserID)
		waitCounted = false
	}
	// 在请求结束或 Context 取消时确保释放槽位，避免客户端断开造成泄漏
	userReleaseFunc = wrapReleaseOnDone(c.Request.Context(), userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}
	// 2. 【新增】Wait后二次检查余额/订阅
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription); err != nil {
		log.Printf("Billing eligibility check failed after wait: %v", err)
		status, code, message := billingErrorDetails(err)
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}
	// 计算粘性会话hash
	sessionHash := h.gatewayService.GenerateSessionHash(parsedReq)
	// 获取平台：优先使用强制平台（/antigravity 路由，中间件已设置 request.Context），否则使用分组平台
	platform := ""
	if forcePlatform, ok := middleware2.GetForcePlatformFromContext(c); ok {
		platform = forcePlatform
	} else if apiKey.Group != nil {
		platform = apiKey.Group.Platform
	}
	sessionKey := sessionHash
	if platform == service.PlatformGemini && sessionHash != "" {
		sessionKey = "gemini:" + sessionHash
	}
	if platform == service.PlatformGemini {
		maxAccountSwitches := h.maxAccountSwitchesGemini
		switchCount := 0
		failedAccountIDs := make(map[int64]struct{})
		lastFailoverStatus := 0
		for {
			selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, sessionKey, reqModel, failedAccountIDs, "") // Gemini 不使用会话限制
			if err != nil {
				// 检查是否为分组级别的 Claude Code 限制错误
				if errors.Is(err, service.ErrClaudeCodeOnly) {
					log.Printf("Rejected non-Claude-Code request (group restriction): user_id=%d, ua=%s", apiKey.UserID, c.GetHeader("User-Agent"))
					h.handleStreamingAwareError(c, http.StatusForbidden, "access_denied", "This group only allows Claude Code clients. Please use the official Claude Code CLI.", streamStarted)
					return
				}
				if len(failedAccountIDs) == 0 {
					h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts: "+err.Error(), streamStarted)
					return
				}
				h.handleFailoverExhausted(c, lastFailoverStatus, streamStarted)
				return
			}
			account := selection.Account
			setOpsSelectedAccount(c, account.ID)
			// 存储槽位编号供后续 RewriteUserID 使用
			c.Set("slot_index", selection.SlotIndex)

			// 检查请求拦截（预热请求、SUGGESTION MODE等）
			if account.IsInterceptWarmupEnabled() {
				interceptType := detectInterceptType(body)
				if interceptType != InterceptTypeNone {
					if selection.Acquired && selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
					if reqStream {
						sendMockInterceptStream(c, reqModel, interceptType)
					} else {
						sendMockInterceptResponse(c, reqModel, interceptType)
					}
					return
				}
			}
			// 3. 获取账号并发槽位
			accountReleaseFunc := selection.ReleaseFunc
			if !selection.Acquired {
				if selection.WaitPlan == nil {
					h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts", streamStarted)
					return
				}
				accountWaitCounted := false
				canWait, err := h.concurrencyHelper.IncrementAccountWaitCount(c.Request.Context(), account.ID, selection.WaitPlan.MaxWaiting)
				if err != nil {
					log.Printf("Increment account wait count failed: %v", err)
					h.handleStreamingAwareError(c, http.StatusInternalServerError, "api_error", "Internal error checking account wait queue", streamStarted)
					return
				}
				if !canWait {
					log.Printf("Account wait queue full: account=%d", account.ID)
					h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later", streamStarted)
					return
				}
				if err == nil && canWait {
					accountWaitCounted = true
				}
				// Ensure the wait counter is decremented if we exit before acquiring the slot.
				defer func() {
					if accountWaitCounted {
						h.concurrencyHelper.DecrementAccountWaitCount(c.Request.Context(), account.ID)
					}
				}()
				accountReleaseFunc, err = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
					c,
					account.ID,
					selection.WaitPlan.MaxConcurrency,
					selection.WaitPlan.Timeout,
					reqStream,
					&streamStarted,
				)
				if err != nil {
					log.Printf("Account concurrency acquire failed: %v", err)
					h.handleConcurrencyError(c, err, "account", streamStarted)
					return
				}
				// Slot acquired: no longer waiting in queue.
				if accountWaitCounted {
					h.concurrencyHelper.DecrementAccountWaitCount(c.Request.Context(), account.ID)
					accountWaitCounted = false
				}
				if err := h.gatewayService.BindStickySession(c.Request.Context(), apiKey.GroupID, sessionKey, account.ID); err != nil {
					log.Printf("Bind sticky session failed: %v", err)
				}
			}
			// 账号槽位/等待计数需要在超时或断开时安全回收
			accountReleaseFunc = wrapReleaseOnDone(c.Request.Context(), accountReleaseFunc)
			// 转发请求 - 根据账号平台分流
			var result *service.ForwardResult
			if account.Platform == service.PlatformAntigravity {
				result, err = h.antigravityGatewayService.ForwardGemini(c.Request.Context(), c, account, reqModel, "generateContent", reqStream, body)
			} else {
				result, err = h.geminiCompatService.Forward(c.Request.Context(), c, account, body)
			}
			if accountReleaseFunc != nil {
				accountReleaseFunc()
			}
			if err != nil {
				var failoverErr *service.UpstreamFailoverError
				if errors.As(err, &failoverErr) {
					failedAccountIDs[account.ID] = struct{}{}
					lastFailoverStatus = failoverErr.StatusCode
					if switchCount >= maxAccountSwitches {
						h.handleFailoverExhausted(c, lastFailoverStatus, streamStarted)
						return
					}
					switchCount++
					log.Printf("Account %d: upstream error %d, switching account %d/%d", account.ID, failoverErr.StatusCode, switchCount, maxAccountSwitches)
					continue
				}
				// 错误响应已在Forward中处理，这里只记录日志
				log.Printf("Forward request failed: %v", err)
				return
			}
			// 捕获请求信息（用于异步记录，避免在 goroutine 中访问 gin.Context）
			userAgent := c.GetHeader("User-Agent")
			clientIP := ip.GetClientIP(c)
			// 异步记录使用量（subscription已在函数开头获取）
			go func(result *service.ForwardResult, usedAccount *service.Account, ua, clientIP string) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
					Result:       result,
					APIKey:       apiKey,
					User:         apiKey.User,
					Account:      usedAccount,
					Subscription: subscription,
					UserAgent:    ua,
					IPAddress:    clientIP,
				}); err != nil {
					log.Printf("Record usage failed: %v", err)
				}
			}(result, account, userAgent, clientIP)
			return
		}
	}

	maxAccountSwitches := h.maxAccountSwitches
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	lastFailoverStatus := 0
	for {
		// 选择支持该模型的账号
		selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, sessionKey, reqModel, failedAccountIDs, parsedReq.MetadataUserID)
		if err != nil {
			// 检查是否为分组级别的 Claude Code 限制错误
			if errors.Is(err, service.ErrClaudeCodeOnly) {
				log.Printf("Rejected non-Claude-Code request (group restriction): user_id=%d, ua=%s", apiKey.UserID, c.GetHeader("User-Agent"))
				h.handleStreamingAwareError(c, http.StatusForbidden, "access_denied", "This group only allows Claude Code clients. Please use the official Claude Code CLI.", streamStarted)
				return
			}
			if len(failedAccountIDs) == 0 {
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts: "+err.Error(), streamStarted)
				return
			}
			h.handleFailoverExhausted(c, lastFailoverStatus, streamStarted)
			return
		}
		account := selection.Account
		setOpsSelectedAccount(c, account.ID)
		// 注意：30 分钟限制使用 SetTempUnschedulable 暂停账号，
		// 账号选择阶段通过 IsSchedulable() 已经过滤掉被暂停的账号，
		// 所以这里不需要额外检查
		// 存储槽位编号供后续 RewriteUserID 使用
		c.Set("slot_index", selection.SlotIndex)
		// 判断是否为 Haiku 模型（Haiku 模型支持同一 session 并行请求）
		isHaikuRequest := IsHaikuModel(reqModel)
		// Anthropic 账号：获取 session 互斥锁，防止同一 session 并发请求
		// 注意：Haiku 模型跳过 session mutex，因为 Claude Code 发送并行请求
		// 使用带等待的版本，而不是直接返回 429
		var sessionMutexRelease func()
		if account.IsAnthropic() && sessionKey != "" && !isHaikuRequest {
			releaseFunc, err := h.concurrencyHelper.AcquireSessionMutexWithWait(c, account.ID, sessionKey, 2*time.Minute, reqStream, &streamStarted)
			if err != nil {
				if selection.Acquired && selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				log.Printf("Session mutex acquire error: %v", err)
				h.handleConcurrencyError(c, err, "session_mutex", streamStarted)
				return
			}
			sessionMutexRelease = releaseFunc
		}

		// 检查请求拦截（预热请求、SUGGESTION MODE等）
		if account.IsInterceptWarmupEnabled() {
			interceptType := detectInterceptType(body)
			if interceptType != InterceptTypeNone {
				if selection.Acquired && selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				if sessionMutexRelease != nil {
					sessionMutexRelease()
				}
				if reqStream {
					sendMockInterceptStream(c, reqModel, interceptType)
				} else {
					sendMockInterceptResponse(c, reqModel, interceptType)
				}
				return
			}
		}
		// 3. 获取账号并发槽位
		accountReleaseFunc := selection.ReleaseFunc
		var accountWaitRelease func()
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				if sessionMutexRelease != nil {
					sessionMutexRelease()
				}
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts", streamStarted)
				return
			}
			canWait, err := h.concurrencyHelper.IncrementAccountWaitCount(c.Request.Context(), account.ID, selection.WaitPlan.MaxWaiting)
			if err != nil {
				if sessionMutexRelease != nil {
					sessionMutexRelease()
				}
				log.Printf("Increment account wait count failed: %v", err)
				h.handleStreamingAwareError(c, http.StatusInternalServerError, "api_error", "Internal error checking account wait queue", streamStarted)
				return
			}
			if !canWait {
				if sessionMutexRelease != nil {
					sessionMutexRelease()
				}
				log.Printf("Account wait queue full: account=%d", account.ID)
				h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later", streamStarted)
				return
			}
			// Only set release function if increment succeeded
			accountWaitRelease = func() {
				h.concurrencyHelper.DecrementAccountWaitCount(c.Request.Context(), account.ID)
			}
			// Anthropic 账号使用统一的 session→slot 绑定逻辑
			if account.IsAnthropic() && sessionKey != "" {
				modelCategory := string(GetModelCategory(reqModel))
				accountReleaseFunc, err = h.concurrencyHelper.AcquireSessionSlotWithWait(
					c,
					account.ID,
					selection.WaitPlan.MaxConcurrency,
					sessionKey,
					modelCategory,
					selection.WaitPlan.Timeout,
					reqStream,
					&streamStarted,
				)
			} else {
				// 非 Anthropic 账号或无 session：使用普通逻辑
				accountReleaseFunc, err = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
					c,
					account.ID,
					selection.WaitPlan.MaxConcurrency,
					selection.WaitPlan.Timeout,
					reqStream,
					&streamStarted,
				)
			}
			if err != nil {
				accountWaitRelease()
				if sessionMutexRelease != nil {
					sessionMutexRelease()
				}
				log.Printf("Account concurrency acquire failed: %v", err)
				h.handleConcurrencyError(c, err, "account", streamStarted)
				return
			}
			if err := h.gatewayService.BindStickySession(c.Request.Context(), apiKey.GroupID, sessionKey, account.ID); err != nil {
				log.Printf("Bind sticky session failed: %v", err)
			}
		}
		// 账号槽位/等待计数需要在超时或断开时安全回收
		accountReleaseFunc = wrapReleaseOnDone(c.Request.Context(), accountReleaseFunc)
		accountWaitRelease = wrapReleaseOnDone(c.Request.Context(), accountWaitRelease)
		sessionMutexRelease = wrapReleaseOnDone(c.Request.Context(), sessionMutexRelease)
		// 用户输入节奏控制：对 Anthropic 账号的用户主动输入请求，确保和上次响应之间有足够间隔
		// 目的：模拟真实用户行为，用户不可能在 Claude 输出完成后立即发送下一条消息
		// 注意：只对 Anthropic 平台生效，Antigravity 等其他平台的消息格式不同，无法判断 user input
		currentSlotIndex := selection.SlotIndex
		if account.IsAnthropic() && !parsedReq.IsToolResult && currentSlotIndex >= 0 {
			if waitErr := h.waitForUserInputPacing(c, account.ID, currentSlotIndex); waitErr != nil {
				// 等待被中断（客户端断开），释放资源并返回
				if sessionMutexRelease != nil {
					sessionMutexRelease()
				}
				if accountWaitRelease != nil {
					accountWaitRelease()
				}
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
				log.Printf("[user-input-pacing] account=%d slot=%d wait interrupted: %v", account.ID, currentSlotIndex, waitErr)
				return
			}
		}
		// 【RPM 限制】等待直到账号 RPM 低于上限
		// 如果 RPM >= 上限，等待最早的请求过期后再继续
		if account.IsAnthropic() {
			if waitErr := h.concurrencyHelper.WaitForRPMSlot(c, account.ID, account.MaxRPM, reqStream, &streamStarted); waitErr != nil {
				// 等待被中断（客户端断开），释放资源并返回
				if sessionMutexRelease != nil {
					sessionMutexRelease()
				}
				if accountWaitRelease != nil {
					accountWaitRelease()
				}
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
				log.Printf("[rpm-limit] account=%d wait interrupted: %v", account.ID, waitErr)
				return
			}
		}
		// 转发请求 - 根据账号平台分流
		var result *service.ForwardResult
		if account.Platform == service.PlatformAntigravity {
			result, err = h.antigravityGatewayService.Forward(c.Request.Context(), c, account, body)
		} else {
			result, err = h.gatewayService.Forward(c.Request.Context(), c, account, parsedReq)
		}
		// 记录响应结束时间（用于用户输入节奏控制，仅 Anthropic）
		if account.IsAnthropic() && currentSlotIndex >= 0 {
			if setErr := h.concurrencyHelper.concurrencyService.SetSlotResponseEndTime(
				context.Background(), account.ID, currentSlotIndex); setErr != nil {
				log.Printf("[user-input-pacing] failed to record response end time: account=%d slot=%d err=%v",
					account.ID, currentSlotIndex, setErr)
			}
		}
		// 立即释放资源
		if sessionMutexRelease != nil {
			sessionMutexRelease()
		}
		if accountWaitRelease != nil {
			accountWaitRelease()
		}
		if accountReleaseFunc != nil {
			accountReleaseFunc()
		}
		if err != nil {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverStatus = failoverErr.StatusCode
				if switchCount >= maxAccountSwitches {
					h.handleFailoverExhausted(c, lastFailoverStatus, streamStarted)
					return
				}
				switchCount++
				log.Printf("Account %d: upstream error %d, switching account %d/%d", account.ID, failoverErr.StatusCode, switchCount, maxAccountSwitches)
				continue
			}
			// 错误响应已在Forward中处理，这里只记录日志
			log.Printf("Account %d: Forward request failed: %v", account.ID, err)
			return
		}
		// 【RPM/30m 限制】记录请求并检查 30 分钟总量
		// 如果达到账号配置的 30 分钟限制，暂停该账号（使用账号配置的冷却时间）
		if account.IsAnthropic() {
			recordResult := h.concurrencyHelper.RecordAccountRequest(context.Background(), account.ID, account.Max30mRequests)
			if recordResult.ShouldPause {
				// 使用现有的 SetTempUnschedulable 接口暂停账号
				pauseCtx, pauseCancel := context.WithTimeout(context.Background(), 5*time.Second)
				cooldownMinutes := account.RateLimitCooldownMinutes
				cooldownDuration := time.Duration(cooldownMinutes) * time.Minute
				if err := h.gatewayService.PauseAccountFor30mLimit(pauseCtx, account.ID, cooldownDuration, recordResult.RequestCount); err != nil {
					log.Printf("[30m-limit] failed to pause account: account=%d err=%v", account.ID, err)
				} else if cooldownMinutes > 0 {
					log.Printf("[30m-limit] account=%d paused for %d minutes (count=%d)", account.ID, cooldownMinutes, recordResult.RequestCount)
				} else {
					log.Printf("[30m-limit] account=%d reached limit but cooldown=0 (count=%d)", account.ID, recordResult.RequestCount)
				}
				pauseCancel()
			}
		}
		// 捕获请求信息（用于异步记录，避免在 goroutine 中访问 gin.Context）
		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		// 异步记录使用量（subscription已在函数开头获取）
		go func(result *service.ForwardResult, usedAccount *service.Account, ua, clientIP string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
				Result:       result,
				APIKey:       apiKey,
				User:         apiKey.User,
				Account:      usedAccount,
				Subscription: subscription,
				UserAgent:    ua,
				IPAddress:    clientIP,
			}); err != nil {
				log.Printf("Record usage failed: %v", err)
			}
		}(result, account, userAgent, clientIP)
		return
	}
}

// Models handles listing available models
// GET /v1/models
// Returns models based on account configurations (model_mapping whitelist)
// Falls back to default models if no whitelist is configured
func (h *GatewayHandler) Models(c *gin.Context) {
	apiKey, _ := middleware2.GetAPIKeyFromContext(c)
	var groupID *int64
	var platform string
	if apiKey != nil && apiKey.Group != nil {
		groupID = &apiKey.Group.ID
		platform = apiKey.Group.Platform
	}
	// Get available models from account configurations (without platform filter)
	availableModels := h.gatewayService.GetAvailableModels(c.Request.Context(), groupID, "")
	if len(availableModels) > 0 {
		// Build model list from whitelist
		models := make([]claude.Model, 0, len(availableModels))
		for _, modelID := range availableModels {
			models = append(models, claude.Model{
				ID:          modelID,
				Type:        "model",
				DisplayName: modelID,
				CreatedAt:   "2024-01-01T00:00:00Z",
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"object": "list",
			"data":   models,
		})
		return
	}
	// Fallback to default models
	if platform == "openai" {
		c.JSON(http.StatusOK, gin.H{
			"object": "list",
			"data":   openai.DefaultModels,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   claude.DefaultModels,
	})
}

// AntigravityModels 返回 Antigravity 支持的全部模型
// GET /antigravity/models
func (h *GatewayHandler) AntigravityModels(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   antigravity.DefaultModels(),
	})
}

// Usage handles getting account balance for CC Switch integration
// GET /v1/usage
func (h *GatewayHandler) Usage(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	// 订阅模式：返回订阅限额信息
	if apiKey.Group != nil && apiKey.Group.IsSubscriptionType() {
		subscription, ok := middleware2.GetSubscriptionFromContext(c)
		if !ok {
			h.errorResponse(c, http.StatusForbidden, "subscription_error", "No active subscription")
			return
		}
		remaining := h.calculateSubscriptionRemaining(apiKey.Group, subscription)
		c.JSON(http.StatusOK, gin.H{
			"isValid":   true,
			"planName":  apiKey.Group.Name,
			"remaining": remaining,
			"unit":      "USD",
		})
		return
	}
	// 余额模式：返回钱包余额
	latestUser, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to get user info")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"isValid":   true,
		"planName":  "钱包余额",
		"remaining": latestUser.Balance,
		"unit":      "USD",
	})
}

// calculateSubscriptionRemaining 计算订阅剩余可用额度
// 逻辑：
// 1. 如果日/周/月任一限额达到100%，返回0
// 2. 否则返回所有已配置周期中剩余额度的最小值
func (h *GatewayHandler) calculateSubscriptionRemaining(group *service.Group, sub *service.UserSubscription) float64 {
	var remainingValues []float64
	// 检查日限额
	if group.HasDailyLimit() {
		remaining := *group.DailyLimitUSD - sub.DailyUsageUSD
		if remaining <= 0 {
			return 0
		}
		remainingValues = append(remainingValues, remaining)
	}
	// 检查周限额
	if group.HasWeeklyLimit() {
		remaining := *group.WeeklyLimitUSD - sub.WeeklyUsageUSD
		if remaining <= 0 {
			return 0
		}
		remainingValues = append(remainingValues, remaining)
	}
	// 检查月限额
	if group.HasMonthlyLimit() {
		remaining := *group.MonthlyLimitUSD - sub.MonthlyUsageUSD
		if remaining <= 0 {
			return 0
		}
		remainingValues = append(remainingValues, remaining)
	}
	// 如果没有配置任何限额，返回-1表示无限制
	if len(remainingValues) == 0 {
		return -1
	}
	// 返回最小值
	min := remainingValues[0]
	for _, v := range remainingValues[1:] {
		if v < min {
			min = v
		}
	}
	return min
}

// handleConcurrencyError handles concurrency-related errors with proper 429 response
func (h *GatewayHandler) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error",
		fmt.Sprintf("Concurrency limit exceeded for %s, please retry later", slotType), streamStarted)
}
func (h *GatewayHandler) handleFailoverExhausted(c *gin.Context, statusCode int, streamStarted bool) {
	status, errType, errMsg := h.mapUpstreamError(statusCode)
	h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
}
func (h *GatewayHandler) mapUpstreamError(statusCode int) (int, string, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "upstream_error", "Upstream authentication failed, please contact administrator"
	case 403:
		return http.StatusBadGateway, "upstream_error", "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "rate_limit_error", "Upstream rate limit exceeded, please retry later"
	case 529:
		return http.StatusServiceUnavailable, "overloaded_error", "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "upstream_error", "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "upstream_error", "Upstream request failed"
	}
}

// handleStreamingAwareError handles errors that may occur after streaming has started
func (h *GatewayHandler) handleStreamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	if streamStarted {
		// Stream already started, send error as SSE event then close
		flusher, ok := c.Writer.(http.Flusher)
		if ok {
			// Send error event in SSE format with proper JSON marshaling
			errorData := map[string]any{
				"type": "error",
				"error": map[string]string{
					"type":    errType,
					"message": message,
				},
			}
			jsonBytes, err := json.Marshal(errorData)
			if err != nil {
				_ = c.Error(err)
				return
			}
			errorEvent := fmt.Sprintf("data: %s\n\n", string(jsonBytes))
			if _, err := fmt.Fprint(c.Writer, errorEvent); err != nil {
				_ = c.Error(err)
			}
			flusher.Flush()
		}
		return
	}
	// Normal case: return JSON response with proper status code
	h.errorResponse(c, status, errType, message)
}

// errorResponse 返回Claude API格式的错误响应
func (h *GatewayHandler) errorResponse(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// CountTokens handles token counting endpoint
// POST /v1/messages/count_tokens
// 特点：校验订阅/余额，但不计算并发、不记录使用量
func (h *GatewayHandler) CountTokens(c *gin.Context) {
	// 从context获取apiKey和user（ApiKeyAuth中间件已设置）
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	_, ok = middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	// 读取请求体
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}
	setOpsRequestContext(c, "", false, body)
	parsedReq, err := service.ParseGatewayRequest(body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	// 验证 model 必填
	if parsedReq.Model == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	setOpsRequestContext(c, parsedReq.Model, parsedReq.Stream, body)
	// 获取订阅信息（可能为nil）
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	// 校验 billing eligibility（订阅/余额）
	// 【注意】不计算并发，但需要校验订阅/余额
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription); err != nil {
		status, code, message := billingErrorDetails(err)
		h.errorResponse(c, status, code, message)
		return
	}
	// 计算粘性会话 hash
	sessionHash := h.gatewayService.GenerateSessionHash(parsedReq)
	// 选择支持该模型的账号
	account, err := h.gatewayService.SelectAccountForModel(c.Request.Context(), apiKey.GroupID, sessionHash, parsedReq.Model)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available accounts: "+err.Error())
		return
	}
	setOpsSelectedAccount(c, account.ID)
	// 转发请求（不记录使用量）
	if err := h.gatewayService.ForwardCountTokens(c.Request.Context(), c, account, parsedReq); err != nil {
		log.Printf("Forward count_tokens request failed: %v", err)
		// 错误响应已在 ForwardCountTokens 中处理
		return
	}
}

// InterceptType 表示请求拦截类型
type InterceptType int

const (
	InterceptTypeNone           InterceptType = iota
	InterceptTypeWarmup                       // 预热请求（返回 "New Conversation"）
	InterceptTypeSuggestionMode               // SUGGESTION MODE（返回空字符串）
)

// detectInterceptType 检测请求是否需要拦截，返回拦截类型
func detectInterceptType(body []byte) InterceptType {
	// 快速检查：如果不包含任何关键字，直接返回
	bodyStr := string(body)
	hasSuggestionMode := strings.Contains(bodyStr, "[SUGGESTION MODE:")
	hasWarmupKeyword := strings.Contains(bodyStr, "title") || strings.Contains(bodyStr, "Warmup")

	if !hasSuggestionMode && !hasWarmupKeyword {
		return InterceptTypeNone
	}

	// 解析请求（只解析一次）
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return InterceptTypeNone
	}

	// 检查 SUGGESTION MODE（最后一条 user 消息）
	if hasSuggestionMode && len(req.Messages) > 0 {
		lastMsg := req.Messages[len(req.Messages)-1]
		if lastMsg.Role == "user" && len(lastMsg.Content) > 0 &&
			lastMsg.Content[0].Type == "text" &&
			strings.HasPrefix(lastMsg.Content[0].Text, "[SUGGESTION MODE:") {
			return InterceptTypeSuggestionMode
		}
	}

	// 检查 Warmup 请求
	if hasWarmupKeyword {
		// 检查 messages 中的标题提示模式
		for _, msg := range req.Messages {
			for _, content := range msg.Content {
				if content.Type == "text" {
					if strings.Contains(content.Text, "Please write a 5-10 word title for the following conversation:") ||
						content.Text == "Warmup" {
						return InterceptTypeWarmup
					}
				}
			}
		}
		// 检查 system 中的标题提取模式
		for _, sys := range req.System {
			if strings.Contains(sys.Text, "nalyze if this message indicates a new conversation topic. If it does, extract a 2-3 word title") {
				return InterceptTypeWarmup
			}
		}
	}

	return InterceptTypeNone
}

// sendMockInterceptStream 发送流式 mock 响应（用于请求拦截）
func sendMockInterceptStream(c *gin.Context, model string, interceptType InterceptType) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// 根据拦截类型决定响应内容
	var msgID string
	var outputTokens int
	var textDeltas []string

	switch interceptType {
	case InterceptTypeSuggestionMode:
		msgID = "msg_mock_suggestion"
		outputTokens = 1
		textDeltas = []string{""} // 空内容
	default: // InterceptTypeWarmup
		msgID = "msg_mock_warmup"
		outputTokens = 2
		textDeltas = []string{"New", " Conversation"}
	}

	// Build message_start event with proper JSON marshaling
	messageStart := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]int{
				"input_tokens":  10,
				"output_tokens": 0,
			},
		},
	}
	messageStartJSON, _ := json.Marshal(messageStart)

	// Build events
	events := []string{
		`event: message_start` + "\n" + `data: ` + string(messageStartJSON),
		`event: content_block_start` + "\n" + `data: {"content_block":{"text":"","type":"text"},"index":0,"type":"content_block_start"}`,
	}

	// Add text deltas
	for _, text := range textDeltas {
		delta := map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]string{
				"type": "text_delta",
				"text": text,
			},
		}
		deltaJSON, _ := json.Marshal(delta)
		events = append(events, `event: content_block_delta`+"\n"+`data: `+string(deltaJSON))
	}

	// Add final events
	messageDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
		},
		"usage": map[string]int{
			"input_tokens":  10,
			"output_tokens": outputTokens,
		},
	}
	messageDeltaJSON, _ := json.Marshal(messageDelta)

	events = append(events,
		`event: content_block_stop`+"\n"+`data: {"index":0,"type":"content_block_stop"}`,
		`event: message_delta`+"\n"+`data: `+string(messageDeltaJSON),
		`event: message_stop`+"\n"+`data: {"type":"message_stop"}`,
	)

	for _, event := range events {
		_, _ = c.Writer.WriteString(event + "\n\n")
		c.Writer.Flush()
		time.Sleep(20 * time.Millisecond)
	}
}

// sendMockInterceptResponse 发送非流式 mock 响应（用于请求拦截）
func sendMockInterceptResponse(c *gin.Context, model string, interceptType InterceptType) {
	var msgID, text string
	var outputTokens int

	switch interceptType {
	case InterceptTypeSuggestionMode:
		msgID = "msg_mock_suggestion"
		text = ""
		outputTokens = 1
	default: // InterceptTypeWarmup
		msgID = "msg_mock_warmup"
		text = "New Conversation"
		outputTokens = 2
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          msgID,
		"type":        "message",
		"role":        "assistant",
		"model":       model,
		"content":     []gin.H{{"type": "text", "text": text}},
		"stop_reason": "end_turn",
		"usage": gin.H{
			"input_tokens":  10,
			"output_tokens": outputTokens,
		},
	})
}
func billingErrorDetails(err error) (status int, code, message string) {
	if errors.Is(err, service.ErrBillingServiceUnavailable) {
		msg := pkgerrors.Message(err)
		if msg == "" {
			msg = "Billing service temporarily unavailable. Please retry later."
		}
		return http.StatusServiceUnavailable, "billing_service_error", msg
	}
	msg := pkgerrors.Message(err)
	if msg == "" {
		msg = err.Error()
	}
	return http.StatusForbidden, "billing_error", msg
}

// waitForUserInputPacing 等待用户输入节奏控制
// 对于 OAuth 账号的用户主动输入请求，确保和上次响应结束之间有足够间隔（5-15秒随机）
// 目的：模拟真实用户行为，用户不可能在 Claude 输出完成后立即发送下一条消息
// 返回 error 仅在 context 被取消时（客户端断开连接）
func (h *GatewayHandler) waitForUserInputPacing(c *gin.Context, accountID int64, slotIndex int) error {
	// 获取上次响应结束时间
	lastResponseEnd, err := h.concurrencyHelper.concurrencyService.GetSlotResponseEndTime(c.Request.Context(), accountID, slotIndex)
	if err != nil {
		// 获取失败不影响请求，仅记录日志
		log.Printf("[user-input-pacing] failed to get last response end time: account=%d slot=%d err=%v",
			accountID, slotIndex, err)
		return nil
	}
	// 没有上次响应记录（首次请求或记录已过期），无需等待
	if lastResponseEnd == 0 {
		return nil
	}
	// 计算随机等待时间：10-20秒
	minWaitSeconds := 10
	maxWaitSeconds := 20
	randomWaitSeconds := minWaitSeconds + rand.Intn(maxWaitSeconds-minWaitSeconds+1)
	randomWaitDuration := time.Duration(randomWaitSeconds) * time.Second
	// 计算已经过去的时间
	now := time.Now().Unix()
	elapsedSeconds := now - lastResponseEnd
	elapsedDuration := time.Duration(elapsedSeconds) * time.Second
	// 计算实际需要等待的时间
	actualWait := randomWaitDuration - elapsedDuration
	if actualWait <= 0 {
		// 已经等待足够长时间，无需额外等待
		log.Printf("[user-input-pacing] account=%d slot=%d elapsed=%ds >= random=%ds, no wait needed",
			accountID, slotIndex, elapsedSeconds, randomWaitSeconds)
		return nil
	}
	log.Printf("[user-input-pacing] account=%d slot=%d elapsed=%ds, random=%ds, waiting=%v",
		accountID, slotIndex, elapsedSeconds, randomWaitSeconds, actualWait)
	// 等待，同时监听 context 取消（客户端断开）
	timer := time.NewTimer(actualWait)
	defer timer.Stop()
	select {
	case <-timer.C:
		// 等待完成
		log.Printf("[user-input-pacing] account=%d slot=%d wait completed", accountID, slotIndex)
		return nil
	case <-c.Request.Context().Done():
		// 客户端断开连接
		return c.Request.Context().Err()
	}
}
