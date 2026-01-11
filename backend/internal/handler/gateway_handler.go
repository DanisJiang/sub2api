package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	pkgerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// GatewayHandler handles API gateway requests
type GatewayHandler struct {
	gatewayService            *service.GatewayService
	geminiCompatService       *service.GeminiMessagesCompatService
	antigravityGatewayService *service.AntigravityGatewayService
	userService               *service.UserService
	billingCacheService       *service.BillingCacheService
	concurrencyHelper         *ConcurrencyHelper
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
	if cfg != nil {
		pingInterval = time.Duration(cfg.Concurrency.PingInterval) * time.Second
	}
	return &GatewayHandler{
		gatewayService:            gatewayService,
		geminiCompatService:       geminiCompatService,
		antigravityGatewayService: antigravityGatewayService,
		userService:               userService,
		billingCacheService:       billingCacheService,
		concurrencyHelper:         NewConcurrencyHelper(concurrencyService, SSEPingFormatClaude, pingInterval),
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

	parsedReq, err := service.ParseGatewayRequest(body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	reqModel := parsedReq.Model
	reqStream := parsedReq.Stream

	// 设置 Claude Code 客户端标识到 context（用于分组限制检查）
	SetClaudeCodeClientContext(c, body)

	// 【全局设置优先】检查是否要求仅允许 Claude Code 客户端
	// 跳过强制平台（如 /antigravity/v1/*）的检查
	if !middleware2.HasForcePlatform(c) && !service.IsClaudeCodeClient(c.Request.Context()) && h.gatewayService.IsGlobalClaudeCodeRequired(c.Request.Context()) {
		log.Printf("Rejected non-Claude-Code request (global setting): user_id=%d, ua=%s", apiKey.UserID, c.GetHeader("User-Agent"))
		h.errorResponse(c, http.StatusForbidden, "access_denied", "Only Claude Code clients are allowed. Please use the official Claude Code CLI.")
		return
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

	// 获取 User-Agent
	userAgent := c.Request.UserAgent()

	// 0. 检查wait队列是否已满
	maxWait := service.CalculateMaxWait(subject.Concurrency)
	canWait, err := h.concurrencyHelper.IncrementWaitCount(c.Request.Context(), subject.UserID, maxWait)
	if err != nil {
		log.Printf("Increment wait count failed: %v", err)
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Internal error checking wait queue")
		return
	}
	if !canWait {
		h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later")
		return
	}
	// 确保在函数退出时减少wait计数
	defer h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), subject.UserID)

	// 1. 首先获取用户并发槽位
	userReleaseFunc, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
	if err != nil {
		log.Printf("User concurrency acquire failed: %v", err)
		h.handleConcurrencyError(c, err, "user", streamStarted)
		return
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
		const maxAccountSwitches = 3
		switchCount := 0
		failedAccountIDs := make(map[int64]struct{})
		lastFailoverStatus := 0

		for {
			selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, sessionKey, reqModel, failedAccountIDs)
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

			// 存储槽位编号供后续 RewriteUserID 使用
			c.Set("slot_index", selection.SlotIndex)

			// 检查预热请求拦截（在账号选择后、转发前检查）
			if account.IsInterceptWarmupEnabled() && isWarmupRequest(body) {
				if selection.Acquired && selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				if reqStream {
					sendMockWarmupStream(c, reqModel)
				} else {
					sendMockWarmupResponse(c, reqModel)
				}
				return
			}

			// 3. 获取账号并发槽位
			accountReleaseFunc := selection.ReleaseFunc
			var accountWaitRelease func()
			if !selection.Acquired {
				if selection.WaitPlan == nil {
					h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts", streamStarted)
					return
				}
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
				// Only set release function if increment succeeded
				accountWaitRelease = func() {
					h.concurrencyHelper.DecrementAccountWaitCount(c.Request.Context(), account.ID)
				}

				accountReleaseFunc, err = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
					c,
					account.ID,
					selection.WaitPlan.MaxConcurrency,
					selection.WaitPlan.Timeout,
					reqStream,
					&streamStarted,
				)
				if err != nil {
					accountWaitRelease()
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
			if accountWaitRelease != nil {
				accountWaitRelease()
			}
			if err != nil {
				var failoverErr *service.UpstreamFailoverError
				if errors.As(err, &failoverErr) {
					failedAccountIDs[account.ID] = struct{}{}
					if switchCount >= maxAccountSwitches {
						lastFailoverStatus = failoverErr.StatusCode
						h.handleFailoverExhausted(c, lastFailoverStatus, streamStarted)
						return
					}
					lastFailoverStatus = failoverErr.StatusCode
					switchCount++
					log.Printf("Account %d: upstream error %d, switching account %d/%d", account.ID, failoverErr.StatusCode, switchCount, maxAccountSwitches)
					continue
				}
				// 错误响应已在Forward中处理，这里只记录日志
				log.Printf("Forward request failed: %v", err)
				return
			}

			// 异步记录使用量（subscription已在函数开头获取）
			go func(result *service.ForwardResult, usedAccount *service.Account, ua string) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
					Result:       result,
					APIKey:       apiKey,
					User:         apiKey.User,
					Account:      usedAccount,
					Subscription: subscription,
					UserAgent:    ua,
				}); err != nil {
					log.Printf("Record usage failed: %v", err)
				}
			}(result, account, userAgent)
			return
		}
	}

	const maxAccountSwitches = 10
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	lastFailoverStatus := 0

	for {
		// 选择支持该模型的账号
		selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, sessionKey, reqModel, failedAccountIDs)
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

		// 存储槽位编号供后续 RewriteUserID 使用
		c.Set("slot_index", selection.SlotIndex)

		// 判断是否为 Haiku 模型（Haiku 模型支持同一 session 并行请求）
		isHaikuRequest := IsHaikuModel(reqModel)

		// 【重要】Haiku 请求槽位升级：
		// SelectAccountWithLoadAwareness 使用普通槽位机制（互斥），不支持同 session 并行
		// 对于 Haiku，需要将普通槽位"升级"为 Haiku 槽位（支持同 session 共享）
		// 这样后续的并行 Haiku 请求就能加入同一个槽位
		if isHaikuRequest && account.IsOAuth() && sessionKey != "" && selection.Acquired && selection.ReleaseFunc != nil {
			// 释放普通槽位
			selection.ReleaseFunc()

			// 使用 Haiku 机制重新获取同一个槽位
			haikuResult, err := h.concurrencyHelper.concurrencyService.AcquireSlotForHaikuByIndex(
				c.Request.Context(), account.ID, selection.SlotIndex, sessionKey)
			if err != nil {
				log.Printf("Haiku slot upgrade failed: %v", err)
				h.handleStreamingAwareError(c, http.StatusInternalServerError, "api_error", "Internal error upgrading to Haiku slot", streamStarted)
				return
			}

			if haikuResult.Acquired {
				// 升级成功，使用 Haiku 释放函数
				selection.ReleaseFunc = haikuResult.ReleaseFunc
				log.Printf("[haiku-upgrade] account=%d session=%.16s slot=%d upgraded", account.ID, sessionKey, selection.SlotIndex)
			} else {
				// 槽位被其他 session 占用（竞态条件，刚释放就被抢占）
				// 回退到等待队列逻辑
				selection.Acquired = false
				selection.ReleaseFunc = nil
				// 创建 WaitPlan（因为原来 Acquired=true 时 WaitPlan 为 nil）
				selection.WaitPlan = &service.AccountWaitPlan{
					AccountID:      account.ID,
					MaxConcurrency: account.Concurrency,
					Timeout:        30 * time.Second, // 默认等待超时
					MaxWaiting:     100,              // 默认最大等待数
				}
				log.Printf("[haiku-upgrade] account=%d session=%.16s slot=%d upgrade failed, falling back to wait", account.ID, sessionKey, selection.SlotIndex)
			}
		}

		// OAuth 账号：检查 session 互斥锁，防止同一 session 并发请求
		// 注意：Haiku 模型跳过 session mutex，因为 Claude Code 发送并行请求
		var sessionMutexRelease func()
		if account.IsOAuth() && sessionKey != "" && !isHaikuRequest {
			releaseFunc, acquired, err := h.concurrencyHelper.AcquireSessionMutex(c.Request.Context(), account.ID, sessionKey)
			if err != nil {
				if selection.Acquired && selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				log.Printf("Session mutex acquire error: %v", err)
				h.handleStreamingAwareError(c, http.StatusInternalServerError, "api_error", "Internal error acquiring session lock", streamStarted)
				return
			}
			if !acquired {
				// 同一 session 有并发请求，拒绝
				if selection.Acquired && selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				log.Printf("Session mutex blocked: account=%d, session=%s (concurrent request detected)", account.ID, sessionKey)
				h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error", "Concurrent request detected for this session, please wait and retry", streamStarted)
				return
			}
			sessionMutexRelease = releaseFunc
		}

		// 检查预热请求拦截（在账号选择后、转发前检查）
		if account.IsInterceptWarmupEnabled() && isWarmupRequest(body) {
			if selection.Acquired && selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			if sessionMutexRelease != nil {
				sessionMutexRelease()
			}
			if reqStream {
				sendMockWarmupStream(c, reqModel)
			} else {
				sendMockWarmupResponse(c, reqModel)
			}
			return
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

			// Use Haiku-specific slot acquisition for Haiku model requests on OAuth accounts
			// Haiku allows same session to share a slot (max 3 parallel requests)
			if account.IsOAuth() && sessionKey != "" && isHaikuRequest {
				accountReleaseFunc, err = h.concurrencyHelper.AcquireHaikuSlotWithWait(
					c,
					account.ID,
					selection.WaitPlan.MaxConcurrency,
					sessionKey,
					selection.WaitPlan.Timeout,
					reqStream,
					&streamStarted,
				)
			} else if account.IsOAuth() && sessionKey != "" {
				// Check model category for Opus/Sonnet pool isolation
				modelCategory := GetModelCategory(reqModel)
				if modelCategory == ModelCategoryOpus || modelCategory == ModelCategorySonnet {
					// Use model-pool-aware slot acquisition (hard isolation)
					accountReleaseFunc, err = h.concurrencyHelper.AcquireModelPoolSlotWithWait(
						c,
						account.ID,
						selection.WaitPlan.MaxConcurrency,
						sessionKey,
						string(modelCategory),
						selection.WaitPlan.Timeout,
						reqStream,
						&streamStarted,
					)
				} else {
					// Unrecognized model: use session-aware slot acquisition
					accountReleaseFunc, err = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeoutBySession(
						c,
						account.ID,
						selection.WaitPlan.MaxConcurrency,
						sessionKey,
						selection.WaitPlan.Timeout,
						reqStream,
						&streamStarted,
					)
				}
			} else {
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

		// 用户输入节奏控制：对 OAuth 账号的用户主动输入请求，确保和上次响应之间有足够间隔
		// 目的：模拟真实用户行为，用户不可能在 Claude 输出完成后立即发送下一条消息
		currentSlotIndex := selection.SlotIndex
		if account.IsOAuth() && !parsedReq.IsToolResult && currentSlotIndex >= 0 {
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

		// 转发请求 - 根据账号平台分流
		var result *service.ForwardResult
		if account.Platform == service.PlatformAntigravity {
			result, err = h.antigravityGatewayService.Forward(c.Request.Context(), c, account, body)
		} else {
			result, err = h.gatewayService.Forward(c.Request.Context(), c, account, parsedReq)
		}

		// 记录响应结束时间（用于用户输入节奏控制）
		if account.IsOAuth() && currentSlotIndex >= 0 {
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
				if switchCount >= maxAccountSwitches {
					lastFailoverStatus = failoverErr.StatusCode
					h.handleFailoverExhausted(c, lastFailoverStatus, streamStarted)
					return
				}
				lastFailoverStatus = failoverErr.StatusCode
				switchCount++
				log.Printf("Account %d: upstream error %d, switching account %d/%d", account.ID, failoverErr.StatusCode, switchCount, maxAccountSwitches)
				continue
			}
			// 错误响应已在Forward中处理，这里只记录日志
			log.Printf("Account %d: Forward request failed: %v", account.ID, err)
			return
		}

		// 异步记录使用量（subscription已在函数开头获取）
		go func(result *service.ForwardResult, usedAccount *service.Account, ua string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
				Result:       result,
				APIKey:       apiKey,
				User:         apiKey.User,
				Account:      usedAccount,
				Subscription: subscription,
				UserAgent:    ua,
			}); err != nil {
				log.Printf("Record usage failed: %v", err)
			}
		}(result, account, userAgent)
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

	parsedReq, err := service.ParseGatewayRequest(body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	// 设置 Claude Code 客户端标识到 context（用于分组限制检查）
	SetClaudeCodeClientContext(c, body)

	// 验证 model 必填
	if parsedReq.Model == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

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

	// 转发请求（不记录使用量）
	if err := h.gatewayService.ForwardCountTokens(c.Request.Context(), c, account, parsedReq); err != nil {
		log.Printf("Forward count_tokens request failed: %v", err)
		// 错误响应已在 ForwardCountTokens 中处理
		return
	}
}

// isWarmupRequest 检测是否为预热请求（标题生成、Warmup等）
func isWarmupRequest(body []byte) bool {
	// 快速检查：如果body不包含关键字，直接返回false
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "title") && !strings.Contains(bodyStr, "Warmup") {
		return false
	}

	// 解析完整请求
	var req struct {
		Messages []struct {
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
		return false
	}

	// 检查 messages 中的标题提示模式
	for _, msg := range req.Messages {
		for _, content := range msg.Content {
			if content.Type == "text" {
				if strings.Contains(content.Text, "Please write a 5-10 word title for the following conversation:") ||
					content.Text == "Warmup" {
					return true
				}
			}
		}
	}

	// 检查 system 中的标题提取模式
	for _, system := range req.System {
		if strings.Contains(system.Text, "nalyze if this message indicates a new conversation topic. If it does, extract a 2-3 word title") {
			return true
		}
	}

	return false
}

// sendMockWarmupStream 发送流式 mock 响应（用于预热请求拦截）
func sendMockWarmupStream(c *gin.Context, model string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Build message_start event with proper JSON marshaling
	messageStart := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            "msg_mock_warmup",
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

	events := []string{
		`event: message_start` + "\n" + `data: ` + string(messageStartJSON),
		`event: content_block_start` + "\n" + `data: {"content_block":{"text":"","type":"text"},"index":0,"type":"content_block_start"}`,
		`event: content_block_delta` + "\n" + `data: {"delta":{"text":"New","type":"text_delta"},"index":0,"type":"content_block_delta"}`,
		`event: content_block_delta` + "\n" + `data: {"delta":{"text":" Conversation","type":"text_delta"},"index":0,"type":"content_block_delta"}`,
		`event: content_block_stop` + "\n" + `data: {"index":0,"type":"content_block_stop"}`,
		`event: message_delta` + "\n" + `data: {"delta":{"stop_reason":"end_turn","stop_sequence":null},"type":"message_delta","usage":{"input_tokens":10,"output_tokens":2}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}

	for _, event := range events {
		_, _ = c.Writer.WriteString(event + "\n\n")
		c.Writer.Flush()
		time.Sleep(20 * time.Millisecond)
	}
}

// sendMockWarmupResponse 发送非流式 mock 响应（用于预热请求拦截）
func sendMockWarmupResponse(c *gin.Context, model string) {
	c.JSON(http.StatusOK, gin.H{
		"id":          "msg_mock_warmup",
		"type":        "message",
		"role":        "assistant",
		"model":       model,
		"content":     []gin.H{{"type": "text", "text": "New Conversation"}},
		"stop_reason": "end_turn",
		"usage": gin.H{
			"input_tokens":  10,
			"output_tokens": 2,
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
