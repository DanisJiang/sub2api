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
	// 返回实际获取到的槽位编号，-1 表示全部满了
	AcquireSlotWithFallback(ctx context.Context, accountID int64, targetSlot int, maxConcurrency int) (int, error)

	// 在指定范围内获取槽位（硬隔离，不跨范围 fallback）
	// 用于模型槽位池隔离：Opus 池和 Sonnet 池各自独立
	// rangeStart: 范围起始（包含），rangeEnd: 范围结束（不包含）
	// 返回实际获取到的槽位编号，-1 表示范围内全部满了
	AcquireSlotInRange(ctx context.Context, accountID int64, targetSlot int, rangeStart int, rangeEnd int) (int, error)

	// Session 互斥锁：防止同一 session 并发请求
	AcquireSessionMutex(ctx context.Context, accountID int64, sessionHash string, requestID string) (bool, error)
	ReleaseSessionMutex(ctx context.Context, accountID int64, sessionHash string, requestID string) error

	// Session-aware 槽位管理（用于 Haiku 模型并行）
	// 同一 session 可以共享槽位（最多 3 个并行），不同 session 不能共享
	AcquireSlotWithSession(ctx context.Context, accountID int64, slotIndex int, sessionHash string, requestID string) (bool, error)
	ReleaseSlotWithSession(ctx context.Context, accountID int64, slotIndex int, sessionHash string) error

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

	// Calculate target slot index from session hash
	// This ensures the same session always maps to the same slot
	targetSlot := hashToSlotIndex(sessionHash, maxConcurrency)

	// 使用降级机制：优先目标槽位，失败则尝试其他槽位
	acquiredSlot, err := s.cache.AcquireSlotWithFallback(ctx, accountID, targetSlot, maxConcurrency)
	if err != nil {
		return nil, err
	}

	if acquiredSlot >= 0 {
		// 记录 session-slot 绑定：target vs acquired 不同说明发生了 fallback
		log.Printf("[session-slot] account=%d session=%.16s target=%d acquired=%d",
			accountID, sessionHash, targetSlot, acquiredSlot)
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

// HaikuSlotResult represents the result of acquiring a Haiku slot
type HaikuSlotResult struct {
	Acquired    bool
	SlotIndex   int
	ReleaseFunc func()
}

// AcquireSlotForHaiku attempts to acquire a slot for Haiku model requests.
// Unlike regular slot acquisition, this allows the same session to have multiple
// concurrent requests sharing the same slot (up to 3 parallel requests).
// Different sessions cannot share the same slot.
func (s *ConcurrencyService) AcquireSlotForHaiku(ctx context.Context, accountID int64, maxConcurrency int, sessionHash string) (*HaikuSlotResult, error) {
	if s.cache == nil {
		return &HaikuSlotResult{
			Acquired:    true,
			SlotIndex:   -1,
			ReleaseFunc: func() {},
		}, nil
	}

	if maxConcurrency <= 0 {
		return &HaikuSlotResult{
			Acquired:    true,
			SlotIndex:   -1,
			ReleaseFunc: func() {},
		}, nil
	}

	// Calculate target slot from session hash
	targetSlot := hashToSlotIndex(sessionHash, maxConcurrency)
	requestID := generateRequestID()

	// Try to acquire the target slot with session awareness
	acquired, err := s.cache.AcquireSlotWithSession(ctx, accountID, targetSlot, sessionHash, requestID)
	if err != nil {
		return nil, fmt.Errorf("acquire haiku slot failed: %w", err)
	}

	if acquired {
		log.Printf("[haiku-slot] account=%d session=%.16s slot=%d req=%.8s acquired",
			accountID, sessionHash, targetSlot, requestID)
		return &HaikuSlotResult{
			Acquired:  true,
			SlotIndex: targetSlot,
			ReleaseFunc: func() {
				log.Printf("[haiku-slot-release] account=%d session=%.16s slot=%d releasing",
					accountID, sessionHash, targetSlot)
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseSlotWithSession(bgCtx, accountID, targetSlot, sessionHash); err != nil {
					log.Printf("[haiku-slot-release] account=%d slot=%d FAILED: %v", accountID, targetSlot, err)
				} else {
					log.Printf("[haiku-slot-release] account=%d slot=%d success", accountID, targetSlot)
				}
			},
		}, nil
	}

	// Slot is occupied by another session, return not acquired
	// The caller should handle this like a normal slot acquisition failure (fallback or wait)
	log.Printf("[haiku-slot] account=%d session=%.16s slot=%d BLOCKED (other session)", accountID, sessionHash, targetSlot)
	return &HaikuSlotResult{
		Acquired:  false,
		SlotIndex: targetSlot,
	}, nil
}

// AcquireSlotForHaikuByIndex attempts to acquire a specific slot for Haiku model requests.
// Unlike AcquireSlotForHaiku which calculates slot from hash, this method uses the given slot index.
// This is used when we want to "upgrade" a regular slot to a Haiku slot.
func (s *ConcurrencyService) AcquireSlotForHaikuByIndex(ctx context.Context, accountID int64, slotIndex int, sessionHash string) (*HaikuSlotResult, error) {
	if s.cache == nil {
		return &HaikuSlotResult{
			Acquired:    true,
			SlotIndex:   slotIndex,
			ReleaseFunc: func() {},
		}, nil
	}

	requestID := generateRequestID()

	// Try to acquire the specified slot with session awareness
	acquired, err := s.cache.AcquireSlotWithSession(ctx, accountID, slotIndex, sessionHash, requestID)
	if err != nil {
		return nil, fmt.Errorf("acquire haiku slot by index failed: %w", err)
	}

	if acquired {
		log.Printf("[haiku-slot] account=%d session=%.16s slot=%d req=%.8s acquired (by index)",
			accountID, sessionHash, slotIndex, requestID)
		return &HaikuSlotResult{
			Acquired:  true,
			SlotIndex: slotIndex,
			ReleaseFunc: func() {
				log.Printf("[haiku-slot-release] account=%d session=%.16s slot=%d releasing",
					accountID, sessionHash, slotIndex)
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseSlotWithSession(bgCtx, accountID, slotIndex, sessionHash); err != nil {
					log.Printf("[haiku-slot-release] account=%d slot=%d FAILED: %v", accountID, slotIndex, err)
				} else {
					log.Printf("[haiku-slot-release] account=%d slot=%d success", accountID, slotIndex)
				}
			},
		}, nil
	}

	// Slot is occupied by another session
	log.Printf("[haiku-slot] account=%d session=%.16s slot=%d BLOCKED (other session, by index)", accountID, sessionHash, slotIndex)
	return &HaikuSlotResult{
		Acquired:  false,
		SlotIndex: slotIndex,
	}, nil
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
// Opus : Sonnet = 2 : 3
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

	// Opus 占 2/5，至少 1 个槽位
	opusSlots := (totalSlots * 2) / 5
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

// AcquireAccountSlotByModel attempts to acquire a slot for a specific model category.
// Opus and Sonnet have separate slot pools (hard isolation).
// Returns the acquired slot index, or -1 if the model's pool is full.
func (s *ConcurrencyService) AcquireAccountSlotByModel(ctx context.Context, accountID int64, maxConcurrency int, sessionHash string, modelCategory string) (*AcquireResult, error) {
	if maxConcurrency <= 0 {
		return &AcquireResult{
			Acquired:    true,
			SlotIndex:   -1,
			ReleaseFunc: func() {},
		}, nil
	}

	// Calculate model slot ranges
	opusRange, sonnetRange := CalculateModelSlotRange(maxConcurrency)

	// Determine which range to use based on model category
	var slotRange ModelSlotRange
	switch modelCategory {
	case "opus":
		slotRange = opusRange
	case "sonnet":
		slotRange = sonnetRange
	default:
		// Should not happen for Claude Code (only opus/sonnet/haiku)
		return nil, fmt.Errorf("unsupported model category for slot pool: %s", modelCategory)
	}

	// Calculate target slot within the range
	rangeSize := slotRange.End - slotRange.Start
	if rangeSize <= 0 {
		return &AcquireResult{
			Acquired:    false,
			SlotIndex:   -1,
			ReleaseFunc: nil,
		}, nil
	}
	targetSlotInRange := hashToSlotIndex(sessionHash, rangeSize)
	targetSlot := slotRange.Start + targetSlotInRange

	// Try to acquire slot within the model's range (hard isolation)
	acquiredSlot, err := s.cache.AcquireSlotInRange(ctx, accountID, targetSlot, slotRange.Start, slotRange.End)
	if err != nil {
		return nil, err
	}

	if acquiredSlot >= 0 {
		log.Printf("[model-slot] account=%d model=%s session=%.16s target=%d acquired=%d range=[%d,%d)",
			accountID, modelCategory, sessionHash, targetSlot, acquiredSlot, slotRange.Start, slotRange.End)
		return &AcquireResult{
			Acquired:  true,
			SlotIndex: acquiredSlot,
			ReleaseFunc: func() {
				log.Printf("[model-slot-release] account=%d slot=%d releasing", accountID, acquiredSlot)
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseAccountSlotByIndex(bgCtx, accountID, acquiredSlot); err != nil {
					log.Printf("[model-slot-release] account=%d slot=%d FAILED: %v", accountID, acquiredSlot, err)
				} else {
					log.Printf("[model-slot-release] account=%d slot=%d success", accountID, acquiredSlot)
				}
			},
		}, nil
	}

	// Model's pool is full (hard isolation - no cross-pool fallback)
	log.Printf("[model-slot] account=%d model=%s session=%.16s FULL range=[%d,%d)",
		accountID, modelCategory, sessionHash, slotRange.Start, slotRange.End)
	return &AcquireResult{
		Acquired:    false,
		SlotIndex:   targetSlot, // Return target slot for waiting
		ReleaseFunc: nil,
	}, nil
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
