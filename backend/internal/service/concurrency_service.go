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
		return &AcquireResult{
			Acquired: true,
			ReleaseFunc: func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseAccountSlot(bgCtx, accountID, requestID); err != nil {
					log.Printf("Warning: failed to release account slot for %d (req=%s): %v", accountID, requestID, err)
				}
			},
		}, nil
	}

	return &AcquireResult{
		Acquired:    false,
		ReleaseFunc: nil,
	}, nil
}

// AcquireUserSlot attempts to acquire a concurrency slot for a user.
// If the user is at max concurrency, it waits until a slot is available or timeout.
// Returns a release function that MUST be called when the request completes.
func (s *ConcurrencyService) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int) (*AcquireResult, error) {
	// If maxConcurrency is 0 or negative, no limit
	if maxConcurrency <= 0 {
		return &AcquireResult{
			Acquired:    true,
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
		return &AcquireResult{
			Acquired: true,
			ReleaseFunc: func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseUserSlot(bgCtx, userID, requestID); err != nil {
					log.Printf("Warning: failed to release user slot for %d (req=%s): %v", userID, requestID, err)
				}
			},
		}, nil
	}

	return &AcquireResult{
		Acquired:    false,
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
		// On error, allow the request to proceed (fail open)
		log.Printf("Warning: increment wait count failed for user %d: %v", userID, err)
		return true, nil
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
		log.Printf("Warning: increment wait count failed for account %d: %v", accountID, err)
		return true, nil
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
func (s *ConcurrencyService) StartSlotCleanupWorker(interval time.Duration) {
	if s == nil || s.cache == nil || interval <= 0 {
		return
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
