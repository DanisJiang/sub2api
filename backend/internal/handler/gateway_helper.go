package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// claudeCodeValidator is a singleton validator for Claude Code client detection
var claudeCodeValidator = service.NewClaudeCodeValidator()

// SetClaudeCodeClientContext 检查请求是否来自 Claude Code 客户端，并设置到 context 中
// 返回更新后的 context
func SetClaudeCodeClientContext(c *gin.Context, body []byte) {
	// 解析请求体为 map
	var bodyMap map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &bodyMap)
	}

	// 验证是否为 Claude Code 客户端
	isClaudeCode := claudeCodeValidator.Validate(c.Request, bodyMap)

	// 更新 request context
	ctx := service.SetClaudeCodeClient(c.Request.Context(), isClaudeCode)
	c.Request = c.Request.WithContext(ctx)
}

// ValidateClaudeCodeHeaders 验证请求 headers 是否符合 Claude Code 客户端特征
// 用于全局 Claude Code 限制的增强验证
// 基于 Claude Code npm 包逆向分析得出的验证规则
// 返回 true 表示 headers 有效
func ValidateClaudeCodeHeaders(c *gin.Context) bool {
	// 1. X-App 必须是 "cli"（Claude Code 硬编码值）
	xApp := c.GetHeader("X-App")
	if xApp != "cli" {
		return false
	}

	// 2. anthropic-version 必须是 "2023-06-01"（Claude Code 硬编码值）
	anthropicVersion := c.GetHeader("anthropic-version")
	if anthropicVersion != "2023-06-01" {
		return false
	}

	// 3. anthropic-beta 必须包含 "claude-code-" 或 "oauth-" 或 "interleaved-thinking"
	anthropicBeta := c.GetHeader("anthropic-beta")
	if !strings.Contains(anthropicBeta, "claude-code-") && !strings.Contains(anthropicBeta, "oauth-") && !strings.Contains(anthropicBeta, "interleaved-thinking") {
		return false
	}

	return true
}

// 并发槽位等待相关常量
//
// 性能优化说明：
// 原实现使用固定间隔（100ms）轮询并发槽位，存在以下问题：
// 1. 高并发时频繁轮询增加 Redis 压力
// 2. 固定间隔可能导致多个请求同时重试（惊群效应）
//
// 新实现使用指数退避 + 抖动算法：
// 1. 初始退避 100ms，每次乘以 1.5，最大 2s
// 2. 添加 ±20% 的随机抖动，分散重试时间点
// 3. 减少 Redis 压力，避免惊群效应
const (
	// maxConcurrencyWait 等待并发槽位的最大时间
	maxConcurrencyWait = 30 * time.Second
	// defaultPingInterval 流式响应等待时发送 ping 的默认间隔
	defaultPingInterval = 10 * time.Second
	// initialBackoff 初始退避时间
	initialBackoff = 100 * time.Millisecond
	// backoffMultiplier 退避时间乘数（指数退避）
	backoffMultiplier = 1.5
	// maxBackoff 最大退避时间
	maxBackoff = 2 * time.Second
)

// SSEPingFormat defines the format of SSE ping events for different platforms
type SSEPingFormat string

const (
	// SSEPingFormatClaude is the Claude/Anthropic SSE ping format
	SSEPingFormatClaude SSEPingFormat = "data: {\"type\": \"ping\"}\n\n"
	// SSEPingFormatNone indicates no ping should be sent (e.g., OpenAI has no ping spec)
	SSEPingFormatNone SSEPingFormat = ""
	// SSEPingFormatComment is an SSE comment ping for OpenAI/Codex CLI clients
	SSEPingFormatComment SSEPingFormat = ":\n\n"
)

// ConcurrencyError represents a concurrency limit error with context
type ConcurrencyError struct {
	SlotType  string
	IsTimeout bool
}

func (e *ConcurrencyError) Error() string {
	if e.IsTimeout {
		return fmt.Sprintf("timeout waiting for %s concurrency slot", e.SlotType)
	}
	return fmt.Sprintf("%s concurrency limit reached", e.SlotType)
}

// ConcurrencyHelper provides common concurrency slot management for gateway handlers
type ConcurrencyHelper struct {
	concurrencyService *service.ConcurrencyService
	pingFormat         SSEPingFormat
	pingInterval       time.Duration
}

// NewConcurrencyHelper creates a new ConcurrencyHelper
func NewConcurrencyHelper(concurrencyService *service.ConcurrencyService, pingFormat SSEPingFormat, pingInterval time.Duration) *ConcurrencyHelper {
	if pingInterval <= 0 {
		pingInterval = defaultPingInterval
	}
	return &ConcurrencyHelper{
		concurrencyService: concurrencyService,
		pingFormat:         pingFormat,
		pingInterval:       pingInterval,
	}
}

// wrapReleaseOnDone ensures release runs at most once and still triggers on context cancellation.
// 用于避免客户端断开或上游超时导致的并发槽位泄漏。
// 修复：添加 quit channel 确保 goroutine 及时退出，避免泄露
func wrapReleaseOnDone(ctx context.Context, releaseFunc func()) func() {
	if releaseFunc == nil {
		return nil
	}
	var once sync.Once
	quit := make(chan struct{})

	release := func() {
		once.Do(func() {
			releaseFunc()
			close(quit) // 通知监听 goroutine 退出
		})
	}

	go func() {
		select {
		case <-ctx.Done():
			// Context 取消时释放资源
			release()
		case <-quit:
			// 正常释放已完成，goroutine 退出
			return
		}
	}()

	return release
}

// IncrementWaitCount increments the wait count for a user
func (h *ConcurrencyHelper) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	return h.concurrencyService.IncrementWaitCount(ctx, userID, maxWait)
}

// DecrementWaitCount decrements the wait count for a user
func (h *ConcurrencyHelper) DecrementWaitCount(ctx context.Context, userID int64) {
	h.concurrencyService.DecrementWaitCount(ctx, userID)
}

// IncrementAccountWaitCount increments the wait count for an account
func (h *ConcurrencyHelper) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	return h.concurrencyService.IncrementAccountWaitCount(ctx, accountID, maxWait)
}

// DecrementAccountWaitCount decrements the wait count for an account
func (h *ConcurrencyHelper) DecrementAccountWaitCount(ctx context.Context, accountID int64) {
	h.concurrencyService.DecrementAccountWaitCount(ctx, accountID)
}

// AcquireSessionMutex acquires a mutex for a session to prevent concurrent requests.
// Returns a release function that must be called when the request completes.
// If the session already has an active request, returns (nil, false, nil).
func (h *ConcurrencyHelper) AcquireSessionMutex(ctx context.Context, accountID int64, sessionHash string) (func(), bool, error) {
	if h.concurrencyService == nil {
		return func() {}, true, nil
	}

	result, err := h.concurrencyService.AcquireSessionMutex(ctx, accountID, sessionHash)
	if err != nil {
		return nil, false, err
	}

	if !result.Acquired {
		return nil, false, nil
	}

	return result.ReleaseFunc, true, nil
}

// AcquireSessionMutexWithWait acquires a session mutex, waiting if it's currently held.
// For streaming requests, sends ping events during the wait.
// This is used to serialize Opus/Sonnet requests from the same session.
func (h *ConcurrencyHelper) AcquireSessionMutexWithWait(c *gin.Context, accountID int64, sessionHash string, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	if h.concurrencyService == nil {
		return func() {}, nil
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	// Try to acquire immediately
	result, err := h.concurrencyService.AcquireSessionMutex(ctx, accountID, sessionHash)
	if err != nil {
		return nil, err
	}
	if result.Acquired {
		return result.ReleaseFunc, nil
	}

	// Need to wait - set up ping if streaming
	needPing := isStream && h.pingFormat != ""

	var flusher http.Flusher
	if needPing {
		var ok bool
		flusher, ok = c.Writer.(http.Flusher)
		if !ok {
			needPing = false
		}
	}

	var pingCh <-chan time.Time
	if needPing {
		pingTicker := time.NewTicker(h.pingInterval)
		defer pingTicker.Stop()
		pingCh = pingTicker.C
	}

	backoff := initialBackoff
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		select {
		case <-ctx.Done():
			return nil, &ConcurrencyError{
				SlotType:  "session_mutex",
				IsTimeout: true,
			}

		case <-pingCh:
			if !*streamStarted {
				c.Header("Content-Type", "text/event-stream")
				c.Header("Cache-Control", "no-cache")
				c.Header("Connection", "keep-alive")
				c.Header("X-Accel-Buffering", "no")
				*streamStarted = true
			}
			if _, err := fmt.Fprint(c.Writer, string(h.pingFormat)); err != nil {
				return nil, err
			}
			flusher.Flush()

		case <-timer.C:
			result, err := h.concurrencyService.AcquireSessionMutex(ctx, accountID, sessionHash)
			if err != nil {
				return nil, err
			}
			if result.Acquired {
				return result.ReleaseFunc, nil
			}
			backoff = nextBackoff(backoff, rng)
			timer.Reset(backoff)
		}
	}
}

// AcquireUserSlotWithWait acquires a user concurrency slot, waiting if necessary.
// For streaming requests, sends ping events during the wait.
// streamStarted is updated if streaming response has begun.
func (h *ConcurrencyHelper) AcquireUserSlotWithWait(c *gin.Context, userID int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	ctx := c.Request.Context()

	// Try to acquire immediately
	result, err := h.concurrencyService.AcquireUserSlot(ctx, userID, maxConcurrency)
	if err != nil {
		return nil, err
	}

	if result.Acquired {
		return result.ReleaseFunc, nil
	}

	// Need to wait - handle streaming ping if needed
	return h.waitForSlotWithPing(c, "user", userID, maxConcurrency, isStream, streamStarted)
}

// AcquireAccountSlotWithWait acquires an account concurrency slot, waiting if necessary.
// For streaming requests, sends ping events during the wait.
// streamStarted is updated if streaming response has begun.
func (h *ConcurrencyHelper) AcquireAccountSlotWithWait(c *gin.Context, accountID int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	ctx := c.Request.Context()

	// Try to acquire immediately
	result, err := h.concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)
	if err != nil {
		return nil, err
	}

	if result.Acquired {
		return result.ReleaseFunc, nil
	}

	// Need to wait - handle streaming ping if needed
	return h.waitForSlotWithPing(c, "account", accountID, maxConcurrency, isStream, streamStarted)
}

// waitForSlotWithPing waits for a concurrency slot, sending ping events for streaming requests.
// streamStarted pointer is updated when streaming begins (for proper error handling by caller).
func (h *ConcurrencyHelper) waitForSlotWithPing(c *gin.Context, slotType string, id int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	return h.waitForSlotWithPingTimeout(c, slotType, id, maxConcurrency, "", maxConcurrencyWait, isStream, streamStarted)
}

// waitForSlotWithPingTimeout waits for a concurrency slot with a custom timeout.
// If sessionHash is provided for account slots, it will use slot-by-index to ensure session affinity.
func (h *ConcurrencyHelper) waitForSlotWithPingTimeout(c *gin.Context, slotType string, id int64, maxConcurrency int, sessionHash string, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	// Try immediate acquire first (avoid unnecessary wait)
	var result *service.AcquireResult
	var err error
	if slotType == "user" {
		result, err = h.concurrencyService.AcquireUserSlot(ctx, id, maxConcurrency)
	} else if sessionHash != "" {
		// Use slot-by-index for session affinity
		result, err = h.concurrencyService.AcquireAccountSlotByIndex(ctx, id, maxConcurrency, sessionHash)
	} else {
		result, err = h.concurrencyService.AcquireAccountSlot(ctx, id, maxConcurrency)
	}
	if err != nil {
		return nil, err
	}
	if result.Acquired {
		return result.ReleaseFunc, nil
	}

	// Determine if ping is needed (streaming + ping format defined)
	needPing := isStream && h.pingFormat != ""

	var flusher http.Flusher
	if needPing {
		var ok bool
		flusher, ok = c.Writer.(http.Flusher)
		if !ok {
			return nil, fmt.Errorf("streaming not supported")
		}
	}

	// Only create ping ticker if ping is needed
	var pingCh <-chan time.Time
	if needPing {
		pingTicker := time.NewTicker(h.pingInterval)
		defer pingTicker.Stop()
		pingCh = pingTicker.C
	}

	backoff := initialBackoff
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		select {
		case <-ctx.Done():
			return nil, &ConcurrencyError{
				SlotType:  slotType,
				IsTimeout: true,
			}

		case <-pingCh:
			// Send ping to keep connection alive
			if !*streamStarted {
				c.Header("Content-Type", "text/event-stream")
				c.Header("Cache-Control", "no-cache")
				c.Header("Connection", "keep-alive")
				c.Header("X-Accel-Buffering", "no")
				*streamStarted = true
			}
			if _, err := fmt.Fprint(c.Writer, string(h.pingFormat)); err != nil {
				return nil, err
			}
			flusher.Flush()

		case <-timer.C:
			// Try to acquire slot
			var result *service.AcquireResult
			var err error

			if slotType == "user" {
				result, err = h.concurrencyService.AcquireUserSlot(ctx, id, maxConcurrency)
			} else if sessionHash != "" {
				// Use slot-by-index for session affinity
				result, err = h.concurrencyService.AcquireAccountSlotByIndex(ctx, id, maxConcurrency, sessionHash)
			} else {
				result, err = h.concurrencyService.AcquireAccountSlot(ctx, id, maxConcurrency)
			}

			if err != nil {
				return nil, err
			}

			if result.Acquired {
				return result.ReleaseFunc, nil
			}
			backoff = nextBackoff(backoff, rng)
			timer.Reset(backoff)
		}
	}
}

// AcquireAccountSlotWithWaitTimeout acquires an account slot with a custom timeout (keeps SSE ping).
func (h *ConcurrencyHelper) AcquireAccountSlotWithWaitTimeout(c *gin.Context, accountID int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	return h.waitForSlotWithPingTimeout(c, "account", accountID, maxConcurrency, "", timeout, isStream, streamStarted)
}

// AcquireSessionSlotWithWait 统一的槽位获取方法（带等待）
// 使用 session→slot 绑定逻辑：
// - 同一 session 的请求使用同一 slot
// - 支持模型池隔离（Opus/Sonnet 各自的槽位池）
// - 支持同 session 并行（Haiku 最多 3 个）
func (h *ConcurrencyHelper) AcquireSessionSlotWithWait(c *gin.Context, accountID int64, maxConcurrency int, sessionHash string, modelCategory string, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	// Try to acquire immediately
	result, err := h.concurrencyService.AcquireSessionSlot(ctx, accountID, maxConcurrency, sessionHash, modelCategory)
	if err != nil {
		return nil, err
	}
	if result.Acquired {
		return result.ReleaseFunc, nil
	}

	// Need to wait - set up ping if streaming
	needPing := isStream && h.pingFormat != ""

	var flusher http.Flusher
	if needPing {
		var ok bool
		flusher, ok = c.Writer.(http.Flusher)
		if !ok {
			return nil, fmt.Errorf("streaming not supported")
		}
	}

	var pingCh <-chan time.Time
	if needPing {
		pingTicker := time.NewTicker(h.pingInterval)
		defer pingTicker.Stop()
		pingCh = pingTicker.C
	}

	backoff := initialBackoff
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	slotType := modelCategory
	if slotType == "" {
		slotType = "account"
	}

	for {
		select {
		case <-ctx.Done():
			return nil, &ConcurrencyError{
				SlotType:  slotType,
				IsTimeout: true,
			}

		case <-pingCh:
			if !*streamStarted {
				c.Header("Content-Type", "text/event-stream")
				c.Header("Cache-Control", "no-cache")
				c.Header("Connection", "keep-alive")
				c.Header("X-Accel-Buffering", "no")
				*streamStarted = true
			}
			if _, err := fmt.Fprint(c.Writer, string(h.pingFormat)); err != nil {
				return nil, err
			}
			flusher.Flush()

		case <-timer.C:
			result, err := h.concurrencyService.AcquireSessionSlot(ctx, accountID, maxConcurrency, sessionHash, modelCategory)
			if err != nil {
				return nil, err
			}
			if result.Acquired {
				return result.ReleaseFunc, nil
			}
			backoff = nextBackoff(backoff, rng)
			timer.Reset(backoff)
		}
	}
}

// ModelCategory represents the category of Claude model
type ModelCategory string

const (
	ModelCategoryOpus   ModelCategory = "opus"
	ModelCategorySonnet ModelCategory = "sonnet"
	ModelCategoryHaiku  ModelCategory = "haiku"
)

// GetModelCategory returns the category of a Claude model.
// Returns empty string if the model is not recognized (should be treated as error for Claude Code).
func GetModelCategory(model string) ModelCategory {
	if containsSubstring(model, "opus") {
		return ModelCategoryOpus
	}
	if containsSubstring(model, "sonnet") {
		return ModelCategorySonnet
	}
	if containsSubstring(model, "haiku") {
		return ModelCategoryHaiku
	}
	return "" // Unrecognized model
}

// IsHaikuModel checks if the model is a Haiku model (claude-3-5-haiku, claude-3-haiku, etc.)
func IsHaikuModel(model string) bool {
	return GetModelCategory(model) == ModelCategoryHaiku
}

// IsOpusModel checks if the model is an Opus model
func IsOpusModel(model string) bool {
	return GetModelCategory(model) == ModelCategoryOpus
}

// IsSonnetModel checks if the model is a Sonnet model
func IsSonnetModel(model string) bool {
	return GetModelCategory(model) == ModelCategorySonnet
}

// containsSubstring checks if string contains a substring
func containsSubstring(s, substr string) bool {
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ============================================
// 账号 RPM 和 30 分钟总量限制
// ============================================

const (
	// defaultAccountMaxRPM 单个账号的默认最大 RPM（账号未配置时使用）
	defaultAccountMaxRPM = 4
	// rpmWindowSeconds RPM 滑动窗口（秒）
	rpmWindowSeconds = 60
)

// 注意：IsAccountPaused 已移除
// 30 分钟限制使用 SetTempUnschedulable 暂停账号，
// 账号选择阶段通过 IsSchedulable() 自动过滤被暂停的账号

// WaitForRPMSlot waits until the account's RPM is below the limit.
// Returns error if context is cancelled or max wait time exceeded.
// If RPM is already below limit, returns immediately.
// maxRPM: 0 表示使用默认值，>0 表示使用账号配置的值
func (h *ConcurrencyHelper) WaitForRPMSlot(c *gin.Context, accountID int64, maxRPM int, isStream bool, streamStarted *bool) error {
	if h.concurrencyService == nil {
		return nil
	}

	// 使用账号配置的 RPM 限制，0 表示使用默认值
	effectiveMaxRPM := maxRPM
	if effectiveMaxRPM <= 0 {
		effectiveMaxRPM = defaultAccountMaxRPM
	}

	ctx := c.Request.Context()

	// Check current RPM
	currentRPM, err := h.concurrencyService.GetAccountRPM(ctx, accountID)
	if err != nil {
		// If error, log and continue (don't block the request)
		return nil
	}

	if currentRPM < effectiveMaxRPM {
		// RPM is below limit, no need to wait
		return nil
	}

	// Need to wait - calculate how long based on oldest request
	oldestTime, err := h.concurrencyService.GetAccountOldestRequestTime(ctx, accountID)
	if err != nil || oldestTime == 0 {
		// If error or no oldest time, wait a fixed amount
		return h.waitWithPing(c, 5*time.Second, isStream, streamStarted)
	}

	// Calculate wait time: oldest request will expire at oldestTime + 60s
	nowMs := time.Now().UnixMilli()
	expireMs := oldestTime + int64(rpmWindowSeconds*1000)
	waitMs := expireMs - nowMs

	if waitMs <= 0 {
		// Already expired, retry check
		return nil
	}

	// Cap wait time at 60 seconds (full window)
	if waitMs > rpmWindowSeconds*1000 {
		waitMs = rpmWindowSeconds * 1000
	}

	waitDuration := time.Duration(waitMs) * time.Millisecond

	// Log the wait
	fmt.Printf("[rpm-limit] account=%d rpm=%d maxRPM=%d waiting=%v\n", accountID, currentRPM, effectiveMaxRPM, waitDuration)

	return h.waitWithPing(c, waitDuration, isStream, streamStarted)
}

// waitWithPing waits for the specified duration, sending pings for streaming requests.
func (h *ConcurrencyHelper) waitWithPing(c *gin.Context, duration time.Duration, isStream bool, streamStarted *bool) error {
	ctx := c.Request.Context()

	// Determine if ping is needed
	needPing := isStream && h.pingFormat != ""

	var flusher http.Flusher
	if needPing {
		var ok bool
		flusher, ok = c.Writer.(http.Flusher)
		if !ok {
			needPing = false
		}
	}

	var pingCh <-chan time.Time
	if needPing {
		pingTicker := time.NewTicker(h.pingInterval)
		defer pingTicker.Stop()
		pingCh = pingTicker.C
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-pingCh:
			if !*streamStarted {
				c.Header("Content-Type", "text/event-stream")
				c.Header("Cache-Control", "no-cache")
				c.Header("Connection", "keep-alive")
				c.Header("X-Accel-Buffering", "no")
				*streamStarted = true
			}
			if _, err := fmt.Fprint(c.Writer, string(h.pingFormat)); err != nil {
				return err
			}
			flusher.Flush()

		case <-timer.C:
			return nil
		}
	}
}

// RecordAccountRequestResult 记录请求的结果
type RecordAccountRequestResult struct {
	ShouldPause  bool // 是否需要暂停账号
	RequestCount int  // 30分钟内的请求计数
}

// RecordAccountRequest records a request for an account and returns rate limit status.
// The caller is responsible for pausing the account using gatewayService.PauseAccountFor30mLimit
// if ShouldPause is true.
// max30mRequests: 0 表示不限制，>0 表示使用账号配置的值
func (h *ConcurrencyHelper) RecordAccountRequest(ctx context.Context, accountID int64, max30mRequests int) RecordAccountRequestResult {
	if h.concurrencyService == nil {
		return RecordAccountRequestResult{}
	}

	// Record for RPM tracking
	if err := h.concurrencyService.RecordAccountRequest(ctx, accountID); err != nil {
		fmt.Printf("[rpm-limit] failed to record request: account=%d err=%v\n", accountID, err)
	}

	// Record for 30m tracking
	if err := h.concurrencyService.RecordAccountRequest30m(ctx, accountID); err != nil {
		fmt.Printf("[30m-limit] failed to record request: account=%d err=%v\n", accountID, err)
	}

	// 如果 max30mRequests 为 0，表示不限制 30 分钟总量
	if max30mRequests <= 0 {
		return RecordAccountRequestResult{}
	}

	// Check 30m count
	count30m, err := h.concurrencyService.GetAccountRequestCount30m(ctx, accountID)
	if err != nil {
		fmt.Printf("[30m-limit] failed to get count: account=%d err=%v\n", accountID, err)
		return RecordAccountRequestResult{}
	}

	if count30m >= max30mRequests {
		fmt.Printf("[30m-limit] account=%d reached limit (count=%d, max=%d)\n", accountID, count30m, max30mRequests)
		return RecordAccountRequestResult{
			ShouldPause:  true,
			RequestCount: count30m,
		}
	}

	return RecordAccountRequestResult{RequestCount: count30m}
}

// nextBackoff 计算下一次退避时间
// 性能优化：使用指数退避 + 随机抖动，避免惊群效应
// current: 当前退避时间
// rng: 随机数生成器（可为 nil，此时不添加抖动）
// 返回值：下一次退避时间（100ms ~ 2s 之间）
func nextBackoff(current time.Duration, rng *rand.Rand) time.Duration {
	// 指数退避：当前时间 * 1.5
	next := time.Duration(float64(current) * backoffMultiplier)
	if next > maxBackoff {
		next = maxBackoff
	}
	if rng == nil {
		return next
	}
	// 添加 ±20% 的随机抖动（jitter 范围 0.8 ~ 1.2）
	// 抖动可以分散多个请求的重试时间点，避免同时冲击 Redis
	jitter := 0.8 + rng.Float64()*0.4
	jittered := time.Duration(float64(next) * jitter)
	if jittered < initialBackoff {
		return initialBackoff
	}
	if jittered > maxBackoff {
		return maxBackoff
	}
	return jittered
}
