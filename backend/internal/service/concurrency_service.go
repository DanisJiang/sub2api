package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"time"
)

// ConcurrencyCache 定义并发控制的缓存接口
// 使用有序集合存储槽位，按时间戳清理过期条目
type ConcurrencyCache interface {
	// 账号槽位管理
	// 键格式: concurrency:account:{accountID}（有序集合，成员为 requestID）
	AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error)
	ReleaseAccountSlot(ctx context.Context, accountID int64, requestID string) error
	GetAccountConcurrency(ctx context.Context, accountID int64) (int, error)

	// 按指定槽位编号获取/释放槽位（用于 session 绑定）
	// 槽位编号格式: slot_{index}，确保同一个用户 session 始终映射到同一个槽位
	AcquireAccountSlotByIndex(ctx context.Context, accountID int64, slotIndex int) (bool, error)
	ReleaseAccountSlotByIndex(ctx context.Context, accountID int64, slotIndex int) error

	// 优先获取目标槽位，失败则获取其他可用槽位（降级机制）
	// maxConcurrency: 最大并发数（同时占用的槽位数上限）
	// totalSlots: 总槽位数（>= maxConcurrency，用于 session 分布）
	// 返回实际获取到的槽位编号，-1 表示并发已满
	AcquireSlotWithFallback(ctx context.Context, accountID int64, targetSlot int, maxConcurrency int, totalSlots int) (int, error)

	// 在指定范围内获取槽位（硬隔离，不跨范围 fallback）
	// 用于模型槽位池隔离：Opus 池和 Sonnet 池各自独立
	// maxConcurrency: 最大并发数（同时占用的槽位数上限）
	// rangeStart: 范围起始（包含），rangeEnd: 范围结束（不包含）
	// 返回实际获取到的槽位编号，-1 表示并发已满或范围内全部满了
	AcquireSlotInRange(ctx context.Context, accountID int64, targetSlot int, maxConcurrency int, rangeStart int, rangeEnd int) (int, error)

	// Session 互斥锁：防止同一 session 并发请求
	AcquireSessionMutex(ctx context.Context, accountID int64, sessionHash string, requestID string) (bool, error)
	ReleaseSessionMutex(ctx context.Context, accountID int64, sessionHash string, requestID string) error

	// Session-aware 槽位管理
	// 同一 session 可以共享槽位，不同 session 不能共享
	// maxParallel: 同一 session 同一模型类别的最大并行数（Haiku 无限制传大数，Opus/Sonnet 传 1）
	// modelCategory: 模型类别（opus/sonnet/haiku），用于独立计数
	AcquireSlotWithSession(ctx context.Context, accountID int64, slotIndex int, sessionHash string, maxParallel int, requestID string, modelCategory string) (bool, error)
	ReleaseSlotWithSession(ctx context.Context, accountID int64, slotIndex int, sessionHash string, modelCategory string) error

	// 账号等待队列（账号级）
	IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error)
	DecrementAccountWaitCount(ctx context.Context, accountID int64) error
	GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error)

	// 用户槽位管理
	// 键格式: concurrency:user:{userID}（有序集合，成员为 requestID）
	AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error)
	ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error
	GetUserConcurrency(ctx context.Context, userID int64) (int, error)

	// 等待队列计数（只在首次创建时设置 TTL）
	IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error)
	DecrementWaitCount(ctx context.Context, userID int64) error

	// 批量负载查询（只读）
	GetAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error)

	// 清理过期槽位（后台任务）
	CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error

	// 清理所有过期槽位（使用 SCAN 遍历所有 concurrency:* 键）
	CleanupAllExpiredSlots(ctx context.Context) (int, error)

	// 清空所有槽位（服务启动时调用，清理因重启导致的遗留槽位）
	ClearAllSlots(ctx context.Context) (int, error)

	// Slot 响应结束时间管理（用于用户输入节奏控制）
	// 键格式: slot_response_end:{accountID}:{slotIndex}
	SetSlotResponseEndTime(ctx context.Context, accountID int64, slotIndex int, timestamp int64) error
	GetSlotResponseEndTime(ctx context.Context, accountID int64, slotIndex int) (int64, error)

	// 账号 RPM 限制（滑动窗口）
	// 键格式: rpm_limit:{accountID}
	RecordAccountRequest(ctx context.Context, accountID int64) error
	GetAccountRPM(ctx context.Context, accountID int64) (int, error)
	GetAccountOldestRequestTime(ctx context.Context, accountID int64) (int64, error)

	// 账号 30 分钟总量限制
	// 键格式: rate_30m:{accountID}
	RecordAccountRequest30m(ctx context.Context, accountID int64) error
	GetAccountRequestCount30m(ctx context.Context, accountID int64) (int, error)

	// 账号暂停调度标记
	// 键格式: account_paused:{accountID}
	SetAccountPaused(ctx context.Context, accountID int64, duration time.Duration) error
	IsAccountPaused(ctx context.Context, accountID int64) (bool, error)

	// Session Slot 绑定：记录 session 绑定的槽位，确保同一 session 的所有请求使用同一槽位
	// 键格式: session_slot:{accountID}:{sessionHash}
	GetSessionSlot(ctx context.Context, accountID int64, sessionHash string) (int, error)
	SetSessionSlot(ctx context.Context, accountID int64, sessionHash string, slotIndex int) error
	RefreshSessionSlotTTL(ctx context.Context, accountID int64, sessionHash string) error

	// 负载均衡请求计数
	// 键格式: lb:req:{accountID}:{minuteBucket}
	IncrLoadBalanceRequestCount(ctx context.Context, accountID int64) error
	GetLoadBalanceRequestCounts(ctx context.Context, accountIDs []int64, windowMinutes int) (map[int64]int64, error)
}

// generateRequestID generates a unique request ID for concurrency slot tracking
// Uses 8 random bytes (16 hex chars) for uniqueness
func generateRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to nanosecond timestamp (extremely rare case)
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

const (
	// Default extra wait slots beyond concurrency limit
	defaultExtraWaitSlots = 20
)

// ConcurrencyService manages concurrent request limiting for accounts and users
type ConcurrencyService struct {
	cache ConcurrencyCache
}

// NewConcurrencyService creates a new ConcurrencyService
func NewConcurrencyService(cache ConcurrencyCache) *ConcurrencyService {
	return &ConcurrencyService{cache: cache}
}

// AcquireResult represents the result of acquiring a concurrency slot
type AcquireResult struct {
	Acquired    bool
	SlotIndex   int    // 槽位编号（-1 表示未使用固定槽位）
	ReleaseFunc func() // Must be called when done (typically via defer)
}

type AccountWithConcurrency struct {
	ID             int64
	MaxConcurrency int
}

type AccountLoadInfo struct {
	AccountID          int64
	CurrentConcurrency int
	WaitingCount       int
	LoadRate           int // 0-100+ (percent)
}

// AcquireAccountSlot attempts to acquire a concurrency slot for an account.
// If the account is at max concurrency, it waits until a slot is available or timeout.
// Returns a release function that MUST be called when the request completes.
func (s *ConcurrencyService) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (*AcquireResult, error) {
	// If maxConcurrency is 0 or negative, no limit
	if maxConcurrency <= 0 {
		return &AcquireResult{
			Acquired:    true,
			SlotIndex:   -1,
			ReleaseFunc: func() {}, // no-op
		}, nil
	}

	// Generate unique request ID for this slot
	requestID := generateRequestID()

	acquired, err := s.cache.AcquireAccountSlot(ctx, accountID, maxConcurrency, requestID)
	if err != nil {
		return nil, err
	}

	if acquired {
		log.Printf("[slot-acquire] account=%d req=%.8s acquired (non-session)", accountID, requestID)
		return &AcquireResult{
			Acquired:  true,
			SlotIndex: -1,
			ReleaseFunc: func() {
				log.Printf("[slot-release] account=%d req=%.8s releasing", accountID, requestID)
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseAccountSlot(bgCtx, accountID, requestID); err != nil {
					log.Printf("[slot-release] account=%d req=%.8s FAILED: %v", accountID, requestID, err)
				} else {
					log.Printf("[slot-release] account=%d req=%.8s success", accountID, requestID)
				}
			},
		}, nil
	}

	return &AcquireResult{
		Acquired:    false,
		SlotIndex:   -1,
		ReleaseFunc: nil,
	}, nil
}

// AcquireAccountSlotByIndex attempts to acquire a specific slot for an account by index.
// The slot index is determined by hashing the user's session, ensuring the same user session
// always maps to the same slot. If the target slot is occupied, it falls back to other available slots.
// Returns a release function that MUST be called when the request completes.
func (s *ConcurrencyService) AcquireAccountSlotByIndex(ctx context.Context, accountID int64, maxConcurrency int, sessionHash string) (*AcquireResult, error) {
	// If maxConcurrency is 0 or negative, no limit
	if maxConcurrency <= 0 {
		return &AcquireResult{
			Acquired:    true,
			SlotIndex:   -1,
			ReleaseFunc: func() {}, // no-op
		}, nil
	}

	// Calculate total slots (more slots than concurrency for realistic session distribution)
	totalSlots := CalculateTotalSlots(maxConcurrency)

	// Calculate target slot index from session hash
	// This ensures the same session always maps to the same slot
	targetSlot := hashToSlotIndex(sessionHash, totalSlots)

	// 使用降级机制：优先目标槽位，失败则尝试其他槽位
	acquiredSlot, err := s.cache.AcquireSlotWithFallback(ctx, accountID, targetSlot, maxConcurrency, totalSlots)
	if err != nil {
		return nil, err
	}

	if acquiredSlot >= 0 {
		// 记录 session-slot 绑定：target vs acquired 不同说明发生了 fallback
		log.Printf("[session-slot] account=%d session=%.16s target=%d acquired=%d (totalSlots=%d)",
			accountID, sessionHash, targetSlot, acquiredSlot, totalSlots)
		return &AcquireResult{
			Acquired:  true,
			SlotIndex: acquiredSlot,
			ReleaseFunc: func() {
				log.Printf("[slot-release] account=%d slot=%d releasing", accountID, acquiredSlot)
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseAccountSlotByIndex(bgCtx, accountID, acquiredSlot); err != nil {
					log.Printf("[slot-release] account=%d slot=%d FAILED: %v", accountID, acquiredSlot, err)
				} else {
					log.Printf("[slot-release] account=%d slot=%d success", accountID, acquiredSlot)
				}
			},
		}, nil
	}

	// 全部槽位都满了
	return &AcquireResult{
		Acquired:    false,
		SlotIndex:   targetSlot, // 返回目标槽位，供排队等待使用
		ReleaseFunc: nil,
	}, nil
}

// hashToSlotIndex converts a session hash string to a slot index (0 to maxConcurrency-1)
func hashToSlotIndex(sessionHash string, maxConcurrency int) int {
	if maxConcurrency <= 0 {
		return 0
	}
	// Simple hash: sum of bytes mod maxConcurrency
	var sum int
	for _, b := range []byte(sessionHash) {
		sum += int(b)
	}
	return sum % maxConcurrency
}

// AcquireUserSlot attempts to acquire a concurrency slot for a user.
// If the user is at max concurrency, it waits until a slot is available or timeout.
// Returns a release function that MUST be called when the request completes.
func (s *ConcurrencyService) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int) (*AcquireResult, error) {
	// If maxConcurrency is 0 or negative, no limit
	if maxConcurrency <= 0 {
		return &AcquireResult{
			Acquired:    true,
			SlotIndex:   -1,
			ReleaseFunc: func() {}, // no-op
		}, nil
	}

	// Generate unique request ID for this slot
	requestID := generateRequestID()

	acquired, err := s.cache.AcquireUserSlot(ctx, userID, maxConcurrency, requestID)
	if err != nil {
		return nil, err
	}

	if acquired {
		log.Printf("[user-slot] user=%d req=%.8s acquired", userID, requestID)
		return &AcquireResult{
			Acquired:  true,
			SlotIndex: -1,
			ReleaseFunc: func() {
				log.Printf("[user-slot-release] user=%d req=%.8s releasing", userID, requestID)
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseUserSlot(bgCtx, userID, requestID); err != nil {
					log.Printf("[user-slot-release] user=%d req=%.8s FAILED: %v", userID, requestID, err)
				} else {
					log.Printf("[user-slot-release] user=%d req=%.8s success", userID, requestID)
				}
			},
		}, nil
	}

	return &AcquireResult{
		Acquired:    false,
		SlotIndex:   -1,
		ReleaseFunc: nil,
	}, nil
}

// ============================================
// Wait Queue Count Methods
// ============================================

// IncrementWaitCount attempts to increment the wait queue counter for a user.
// Returns true if successful, false if the wait queue is full.
// maxWait should be user.Concurrency + defaultExtraWaitSlots
func (s *ConcurrencyService) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	if s.cache == nil {
		// Redis not available, allow request
		return true, nil
	}

	result, err := s.cache.IncrementWaitCount(ctx, userID, maxWait)
	if err != nil {
		return false, fmt.Errorf("increment wait count failed for user %d: %w", userID, err)
	}
	return result, nil
}

// DecrementWaitCount decrements the wait queue counter for a user.
// Should be called when a request completes or exits the wait queue.
func (s *ConcurrencyService) DecrementWaitCount(ctx context.Context, userID int64) {
	if s.cache == nil {
		return
	}

	// Use background context to ensure decrement even if original context is cancelled
	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.cache.DecrementWaitCount(bgCtx, userID); err != nil {
		log.Printf("Warning: decrement wait count failed for user %d: %v", userID, err)
	}
}

// IncrementAccountWaitCount increments the wait queue counter for an account.
func (s *ConcurrencyService) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	if s.cache == nil {
		return true, nil
	}

	result, err := s.cache.IncrementAccountWaitCount(ctx, accountID, maxWait)
	if err != nil {
		return false, fmt.Errorf("increment wait count failed for account %d: %w", accountID, err)
	}
	return result, nil
}

// DecrementAccountWaitCount decrements the wait queue counter for an account.
func (s *ConcurrencyService) DecrementAccountWaitCount(ctx context.Context, accountID int64) {
	if s.cache == nil {
		return
	}

	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.cache.DecrementAccountWaitCount(bgCtx, accountID); err != nil {
		log.Printf("Warning: decrement wait count failed for account %d: %v", accountID, err)
	}
}

// GetAccountWaitingCount gets current wait queue count for an account.
func (s *ConcurrencyService) GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error) {
	if s.cache == nil {
		return 0, nil
	}
	return s.cache.GetAccountWaitingCount(ctx, accountID)
}

// CalculateMaxWait calculates the maximum wait queue size for a user
// maxWait = userConcurrency + defaultExtraWaitSlots
func CalculateMaxWait(userConcurrency int) int {
	if userConcurrency <= 0 {
		userConcurrency = 1
	}
	return userConcurrency + defaultExtraWaitSlots
}

// GetAccountsLoadBatch returns load info for multiple accounts.
func (s *ConcurrencyService) GetAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	if s.cache == nil {
		return map[int64]*AccountLoadInfo{}, nil
	}
	return s.cache.GetAccountsLoadBatch(ctx, accounts)
}

// CleanupExpiredAccountSlots removes expired slots for one account (background task).
func (s *ConcurrencyService) CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.CleanupExpiredAccountSlots(ctx, accountID)
}

// StartSlotCleanupWorker starts a background cleanup worker for expired slots.
// 使用 Redis SCAN 遍历所有 concurrency:* 键，清理过期槽位。
// 这样可以清理所有类型的槽位（账号、用户），不依赖数据库查询。
// 启动时会先清空所有槽位，因为服务重启意味着所有请求都已中断。
func (s *ConcurrencyService) StartSlotCleanupWorker(interval time.Duration) {
	if s == nil || s.cache == nil || interval <= 0 {
		return
	}

	// 启动时清空所有槽位（服务重启后，之前的请求都已断开，槽位应该被清空）
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cleared, err := s.cache.ClearAllSlots(ctx)
	cancel()
	if err != nil {
		log.Printf("[ConcurrencyStartup] Warning: failed to clear slots on startup: %v", err)
	} else if cleared > 0 {
		log.Printf("[ConcurrencyStartup] Cleared %d stale slots on startup", cleared)
	} else {
		log.Printf("[ConcurrencyStartup] No stale slots to clear")
	}

	runCleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		cleaned, err := s.cache.CleanupAllExpiredSlots(ctx)
		if err != nil {
			log.Printf("Warning: cleanup expired slots failed: %v", err)
		} else if cleaned > 0 {
			log.Printf("[ConcurrencyCleanup] Cleaned %d expired slots", cleaned)
		}
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		runCleanup()
		for range ticker.C {
			runCleanup()
		}
	}()
}

// GetAccountConcurrencyBatch gets current concurrency counts for multiple accounts
// Returns a map of accountID -> current concurrency count
func (s *ConcurrencyService) GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int)

	for _, accountID := range accountIDs {
		count, err := s.cache.GetAccountConcurrency(ctx, accountID)
		if err != nil {
			// If key doesn't exist in Redis, count is 0
			count = 0
		}
		result[accountID] = count
	}

	return result, nil
}

// ============================================
// Session Mutex Methods (防止同一 session 并发)
// ============================================

// SessionMutexResult represents the result of acquiring a session mutex
type SessionMutexResult struct {
	Acquired    bool
	RequestID   string // 用于释放锁
	ReleaseFunc func() // 释放锁的函数
}

// AcquireSessionMutex attempts to acquire a mutex for a session.
// This prevents the same session from having concurrent requests.
// Returns false if the session already has an active request.
func (s *ConcurrencyService) AcquireSessionMutex(ctx context.Context, accountID int64, sessionHash string) (*SessionMutexResult, error) {
	if s.cache == nil || sessionHash == "" {
		// 没有缓存或没有 session，跳过互斥检查
		return &SessionMutexResult{
			Acquired:    true,
			ReleaseFunc: func() {},
		}, nil
	}

	requestID := generateRequestID()
	acquired, err := s.cache.AcquireSessionMutex(ctx, accountID, sessionHash, requestID)
	if err != nil {
		return nil, fmt.Errorf("acquire session mutex failed: %w", err)
	}

	if !acquired {
		// 锁已被占用，同一 session 有并发请求
		log.Printf("[session-mutex] account=%d session=%.16s BLOCKED (concurrent request)", accountID, sessionHash)
		return &SessionMutexResult{
			Acquired:    false,
			ReleaseFunc: nil,
		}, nil
	}

	log.Printf("[session-mutex] account=%d session=%.16s req=%.8s acquired", accountID, sessionHash, requestID)
	return &SessionMutexResult{
		Acquired:  true,
		RequestID: requestID,
		ReleaseFunc: func() {
			log.Printf("[session-mutex-release] account=%d session=%.16s req=%.8s releasing", accountID, sessionHash, requestID)
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.cache.ReleaseSessionMutex(bgCtx, accountID, sessionHash, requestID); err != nil {
				log.Printf("[session-mutex-release] account=%d session=%.16s req=%.8s FAILED: %v", accountID, sessionHash, requestID, err)
			} else {
				log.Printf("[session-mutex-release] account=%d session=%.16s req=%.8s success", accountID, sessionHash, requestID)
			}
		},
	}, nil
}

// ============================================
// Slot Calculation (槽位数量计算)
// ============================================

// CalculateTotalSlots calculates the total number of slots based on concurrency.
// Formula: totalSlots = concurrency * 5 / 3 (rounded up)
// This creates more slot positions than concurrent connections for more realistic session distribution.
// Examples:
//   - concurrency 3 → slots 5
//   - concurrency 6 → slots 10
//   - concurrency 9 → slots 15
func CalculateTotalSlots(concurrency int) int {
	if concurrency <= 0 {
		return 0
	}
	// 使用向上取整：(concurrency * 4 + 2) / 3，约为 1.33 倍
	return (concurrency*4 + 2) / 3
}

// ============================================
// Model Slot Pool (模型槽位池隔离)
// ============================================

// ModelSlotRange represents the slot range for a model category
type ModelSlotRange struct {
	Start int // 起始槽位（包含）
	End   int // 结束槽位（不包含）
}

// CalculateModelSlotRange calculates the slot range for Opus and Sonnet models.
// Opus : Sonnet = 1 : 1
// Layout: [0, opusEnd) is Opus pool, [opusEnd, total) is Sonnet pool
// Haiku does not use model pool isolation, it follows session binding.
// Special case: when totalSlots < 2, both pools share all slots (no isolation possible).
func CalculateModelSlotRange(totalSlots int) (opus, sonnet ModelSlotRange) {
	if totalSlots <= 0 {
		return ModelSlotRange{0, 0}, ModelSlotRange{0, 0}
	}

	// Special case: with only 1 slot, both pools share it (no isolation possible)
	if totalSlots == 1 {
		return ModelSlotRange{0, 1}, ModelSlotRange{0, 1}
	}

	// Opus 占 1/2，至少 1 个槽位
	opusSlots := totalSlots / 2
	if opusSlots < 1 {
		opusSlots = 1
	}
	// 确保 Sonnet 至少有 1 个槽位
	if opusSlots >= totalSlots {
		opusSlots = totalSlots - 1
	}

	opus = ModelSlotRange{Start: 0, End: opusSlots}
	sonnet = ModelSlotRange{Start: opusSlots, End: totalSlots}
	return opus, sonnet
}

// SetSlotResponseEndTime 记录 slot 的响应结束时间
// 用于用户输入节奏控制：确保同一 slot 处理用户主动输入时有足够间隔
func (s *ConcurrencyService) SetSlotResponseEndTime(ctx context.Context, accountID int64, slotIndex int) error {
	timestamp := time.Now().Unix()
	return s.cache.SetSlotResponseEndTime(ctx, accountID, slotIndex, timestamp)
}

// GetSlotResponseEndTime 获取 slot 的上次响应结束时间
// 返回 Unix 时间戳，如果没有记录则返回 0
func (s *ConcurrencyService) GetSlotResponseEndTime(ctx context.Context, accountID int64, slotIndex int) (int64, error) {
	return s.cache.GetSlotResponseEndTime(ctx, accountID, slotIndex)
}

// ============================================
// 账号 RPM 和 30 分钟总量限制
// ============================================

// RecordAccountRequest 记录账号请求（用于 RPM 统计）
func (s *ConcurrencyService) RecordAccountRequest(ctx context.Context, accountID int64) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.RecordAccountRequest(ctx, accountID)
}

// GetAccountRPM 获取账号当前 RPM（过去 60 秒的请求数）
func (s *ConcurrencyService) GetAccountRPM(ctx context.Context, accountID int64) (int, error) {
	if s.cache == nil {
		return 0, nil
	}
	return s.cache.GetAccountRPM(ctx, accountID)
}

// GetAccountOldestRequestTime 获取账号最早的请求时间（毫秒时间戳）
// 用于计算需要等待多久才能有新配额
func (s *ConcurrencyService) GetAccountOldestRequestTime(ctx context.Context, accountID int64) (int64, error) {
	if s.cache == nil {
		return 0, nil
	}
	return s.cache.GetAccountOldestRequestTime(ctx, accountID)
}

// RecordAccountRequest30m 记录账号请求（用于 30 分钟总量统计）
func (s *ConcurrencyService) RecordAccountRequest30m(ctx context.Context, accountID int64) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.RecordAccountRequest30m(ctx, accountID)
}

// GetAccountRequestCount30m 获取账号过去 30 分钟的请求数
func (s *ConcurrencyService) GetAccountRequestCount30m(ctx context.Context, accountID int64) (int, error) {
	if s.cache == nil {
		return 0, nil
	}
	return s.cache.GetAccountRequestCount30m(ctx, accountID)
}

// SetAccountPaused 设置账号暂停调度标记
func (s *ConcurrencyService) SetAccountPaused(ctx context.Context, accountID int64, duration time.Duration) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.SetAccountPaused(ctx, accountID, duration)
}

// IsAccountPaused 检查账号是否被暂停调度
func (s *ConcurrencyService) IsAccountPaused(ctx context.Context, accountID int64) (bool, error) {
	if s.cache == nil {
		return false, nil
	}
	return s.cache.IsAccountPaused(ctx, accountID)
}

// ============================================
// Session Slot 绑定 - 确保同一 session 的所有请求使用同一槽位
// ============================================

// GetSessionSlot 获取 session 绑定的槽位
// 返回 -1 表示没有绑定
func (s *ConcurrencyService) GetSessionSlot(ctx context.Context, accountID int64, sessionHash string) (int, error) {
	if s.cache == nil {
		return -1, nil
	}
	return s.cache.GetSessionSlot(ctx, accountID, sessionHash)
}

// SetSessionSlot 设置 session 绑定的槽位
func (s *ConcurrencyService) SetSessionSlot(ctx context.Context, accountID int64, sessionHash string, slotIndex int) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.SetSessionSlot(ctx, accountID, sessionHash, slotIndex)
}

// RefreshSessionSlotTTL 刷新 session slot 绑定的 TTL
func (s *ConcurrencyService) RefreshSessionSlotTTL(ctx context.Context, accountID int64, sessionHash string) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.RefreshSessionSlotTTL(ctx, accountID, sessionHash)
}

// ============================================
// 统一的槽位获取逻辑
// ============================================

// SessionSlotResult 统一槽位获取结果
type SessionSlotResult struct {
	Acquired    bool   // 是否成功获取
	SlotIndex   int    // 获取到的槽位编号
	ReleaseFunc func() // 释放函数，必须在请求结束时调用
}

// AcquireSessionSlot 统一的槽位获取方法
// 逻辑：
// 1. 检查 session 是否已有绑定的 slot
// 2. 如果有绑定，尝试获取那个 slot
// 3. 如果没有绑定，计算 targetSlot，尝试获取
// 4. 如果 targetSlot 被其他 session 占用，fallback 到其他空闲 slot
// 5. 获取成功后，绑定/刷新 session → slot
//
// 参数:
// - accountID: 账号 ID
// - maxConcurrency: 最大并发数
// - sessionHash: session 标识
// - modelCategory: 模型类别 (opus/sonnet/haiku/"")，用于槽位池隔离
//
// 模型池隔离：
// - Opus 和 Sonnet 各有独立的槽位池（硬隔离，互不影响）
// - Haiku 和未识别模型使用全部槽位池
// - 不同模型类别的 session 绑定是独立的（同一 session 在 opus/sonnet 下有不同绑定）
func (s *ConcurrencyService) AcquireSessionSlot(ctx context.Context, accountID int64, maxConcurrency int, sessionHash string, modelCategory string) (*SessionSlotResult, error) {
	if s.cache == nil {
		return &SessionSlotResult{
			Acquired:    true,
			SlotIndex:   -1,
			ReleaseFunc: func() {},
		}, nil
	}

	if maxConcurrency <= 0 {
		return &SessionSlotResult{
			Acquired:    true,
			SlotIndex:   -1,
			ReleaseFunc: func() {},
		}, nil
	}

	totalSlots := CalculateTotalSlots(maxConcurrency)
	requestID := generateRequestID()

	// 计算槽位范围（模型池隔离）
	// Opus 和 Sonnet 各有独立的槽位池，Haiku 使用全部槽位
	var rangeStart, rangeEnd int
	switch modelCategory {
	case "opus":
		opusRange, _ := CalculateModelSlotRange(totalSlots)
		rangeStart, rangeEnd = opusRange.Start, opusRange.End
	case "sonnet":
		_, sonnetRange := CalculateModelSlotRange(totalSlots)
		rangeStart, rangeEnd = sonnetRange.Start, sonnetRange.End
	default:
		// Haiku 和未识别模型使用全部槽位
		rangeStart, rangeEnd = 0, totalSlots
	}

	rangeSize := rangeEnd - rangeStart
	if rangeSize <= 0 {
		return &SessionSlotResult{
			Acquired:    false,
			SlotIndex:   -1,
			ReleaseFunc: nil,
		}, nil
	}

	// 计算 maxParallel：同一 session 最大并行请求数
	// Haiku: 无限制（传大数）
	// Opus/Sonnet: 严格 1 个
	var maxParallel int
	if modelCategory == "haiku" || modelCategory == "" {
		maxParallel = 9999 // 无限制
	} else {
		maxParallel = 1 // Opus/Sonnet 严格串行
	}

	// 查询 session 是否已有绑定（统一使用 sessionHash，无前缀）
	boundSlot := -1
	slot, err := s.cache.GetSessionSlot(ctx, accountID, sessionHash)
	if err != nil {
		log.Printf("[session-slot] GetSessionSlot failed: account=%d session=%.16s err=%v", accountID, sessionHash, err)
	} else {
		boundSlot = slot
	}

	// 2. 如果有绑定，检查绑定的 slot 是否在当前模型池范围内
	if boundSlot >= 0 {
		if boundSlot >= rangeStart && boundSlot < rangeEnd {
			// 绑定的 slot 在当前模型池范围内，尝试获取
			acquired, err := s.cache.AcquireSlotWithSession(ctx, accountID, boundSlot, sessionHash, maxParallel, requestID, modelCategory)
			if err != nil {
				return nil, fmt.Errorf("acquire bound slot failed: %w", err)
			}
			if acquired {
				// 刷新 TTL（slot 没变，只刷新 TTL）
				_ = s.cache.RefreshSessionSlotTTL(ctx, accountID, sessionHash)
				log.Printf("[session-slot] account=%d session=%.16s model=%s bound_slot=%d acquired", accountID, sessionHash, modelCategory, boundSlot)
				// 捕获 modelCategory 到闭包中
				releaseMC := modelCategory
				return &SessionSlotResult{
					Acquired:  true,
					SlotIndex: boundSlot,
					ReleaseFunc: func() {
						bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if err := s.cache.ReleaseSlotWithSession(bgCtx, accountID, boundSlot, sessionHash, releaseMC); err != nil {
							log.Printf("[session-slot-release] account=%d slot=%d FAILED: %v", accountID, boundSlot, err)
						}
					},
				}, nil
			}
			// 绑定的槽位被其他 session 占用，继续往下 fallback 到其他 slot
			log.Printf("[session-slot] account=%d session=%.16s model=%s bound_slot=%d BLOCKED by other session, will fallback", accountID, sessionHash, modelCategory, boundSlot)
		} else {
			// 绑定的 slot 不在当前模型池范围内（模型切换导致），需要在新范围内选择
			log.Printf("[session-slot] account=%d session=%.16s model=%s bound_slot=%d OUT OF RANGE [%d,%d), will select new slot", accountID, sessionHash, modelCategory, boundSlot, rangeStart, rangeEnd)
		}
	}

	// 3. 没有有效绑定或绑定不在范围内，在当前模型池范围内选择新 slot
	targetSlotInRange := hashToSlotIndex(sessionHash, rangeSize)
	targetSlot := rangeStart + targetSlotInRange

	// 4. 尝试获取 targetSlot
	acquired, err := s.cache.AcquireSlotWithSession(ctx, accountID, targetSlot, sessionHash, maxParallel, requestID, modelCategory)
	if err != nil {
		return nil, fmt.Errorf("acquire target slot failed: %w", err)
	}
	if acquired {
		// 绑定/更新 session → slot
		if err := s.cache.SetSessionSlot(ctx, accountID, sessionHash, targetSlot); err != nil {
			log.Printf("[session-slot] SetSessionSlot failed: account=%d session=%.16s slot=%d err=%v", accountID, sessionHash, targetSlot, err)
		}
		log.Printf("[session-slot] account=%d session=%.16s model=%s target_slot=%d acquired (new binding)", accountID, sessionHash, modelCategory, targetSlot)
		// 捕获 modelCategory 到闭包中
		releaseMC := modelCategory
		return &SessionSlotResult{
			Acquired:  true,
			SlotIndex: targetSlot,
			ReleaseFunc: func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseSlotWithSession(bgCtx, accountID, targetSlot, sessionHash, releaseMC); err != nil {
					log.Printf("[session-slot-release] account=%d slot=%d FAILED: %v", accountID, targetSlot, err)
				}
			},
		}, nil
	}

	// 5. targetSlot 被占用，在范围内 fallback 到其他空闲 slot
	log.Printf("[session-slot] account=%d session=%.16s model=%s target_slot=%d occupied, trying fallback in range [%d,%d)", accountID, sessionHash, modelCategory, targetSlot, rangeStart, rangeEnd)
	for offset := 1; offset < rangeSize; offset++ {
		fallbackSlotInRange := (targetSlotInRange + offset) % rangeSize
		fallbackSlot := rangeStart + fallbackSlotInRange
		acquired, err := s.cache.AcquireSlotWithSession(ctx, accountID, fallbackSlot, sessionHash, maxParallel, requestID, modelCategory)
		if err != nil {
			log.Printf("[session-slot] fallback acquire error: account=%d slot=%d err=%v", accountID, fallbackSlot, err)
			continue
		}
		if acquired {
			// 绑定/更新 session → slot
			if err := s.cache.SetSessionSlot(ctx, accountID, sessionHash, fallbackSlot); err != nil {
				log.Printf("[session-slot] SetSessionSlot failed: account=%d session=%.16s slot=%d err=%v", accountID, sessionHash, fallbackSlot, err)
			}
			log.Printf("[session-slot] account=%d session=%.16s model=%s fallback_slot=%d acquired (new binding)", accountID, sessionHash, modelCategory, fallbackSlot)
			// 捕获 modelCategory 到闭包中
			releaseMC := modelCategory
			return &SessionSlotResult{
				Acquired:  true,
				SlotIndex: fallbackSlot,
				ReleaseFunc: func() {
					bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := s.cache.ReleaseSlotWithSession(bgCtx, accountID, fallbackSlot, sessionHash, releaseMC); err != nil {
						log.Printf("[session-slot-release] account=%d slot=%d FAILED: %v", accountID, fallbackSlot, err)
					}
				},
			}, nil
		}
	}

	// 6. 范围内所有槽位都满了
	log.Printf("[session-slot] account=%d session=%.16s model=%s ALL SLOTS FULL in range [%d,%d)", accountID, sessionHash, modelCategory, rangeStart, rangeEnd)
	return &SessionSlotResult{
		Acquired:    false,
		SlotIndex:   targetSlot,
		ReleaseFunc: nil,
	}, nil
}

// ============================================
// 负载均衡请求计数
// ============================================

// IncrLoadBalanceRequestCount 增加账号的负载均衡请求计数
func (s *ConcurrencyService) IncrLoadBalanceRequestCount(ctx context.Context, accountID int64) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.IncrLoadBalanceRequestCount(ctx, accountID)
}

// GetLoadBalanceRequestCounts 批量获取多个账号在时间窗口内的请求计数
func (s *ConcurrencyService) GetLoadBalanceRequestCounts(ctx context.Context, accountIDs []int64, windowMinutes int) (map[int64]int64, error) {
	if s.cache == nil {
		return make(map[int64]int64), nil
	}
	return s.cache.GetLoadBalanceRequestCounts(ctx, accountIDs, windowMinutes)
}
