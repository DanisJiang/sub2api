package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// 并发控制缓存常量定义
//
// 性能优化说明：
// 原实现使用 SCAN 命令遍历独立的槽位键（concurrency:account:{id}:{requestID}），
// 在高并发场景下 SCAN 需要多次往返，且遍历大量键时性能下降明显。
//
// 新实现改用 Redis 有序集合（Sorted Set）：
// 1. 每个账号/用户只有一个键，成员为 requestID，分数为时间戳
// 2. 使用 ZCARD 原子获取并发数，时间复杂度 O(1)
// 3. 使用 ZREMRANGEBYSCORE 清理过期槽位，避免手动管理 TTL
// 4. 单次 Redis 调用完成计数，减少网络往返
const (
	// 并发槽位键前缀（有序集合）
	// 格式: concurrency:account:{accountID}
	accountSlotKeyPrefix = "concurrency:account:"
	// 格式: concurrency:user:{userID}
	userSlotKeyPrefix = "concurrency:user:"
	// 等待队列计数器格式: concurrency:wait:{userID}
	waitQueueKeyPrefix = "concurrency:wait:"
	// 账号级等待队列计数器格式: wait:account:{accountID}
	accountWaitKeyPrefix = "wait:account:"
	// Session 互斥锁前缀：防止同一 session 并发请求
	// 格式: session_mutex:{accountID}:{sessionHash}
	sessionMutexKeyPrefix = "session_mutex:"

	// 默认槽位过期时间（分钟），可通过配置覆盖
	defaultSlotTTLMinutes = 15
	// Session 互斥锁过期时间（秒），设置为较短时间避免死锁
	sessionMutexTTLSeconds = 300 // 5 分钟

	// Slot Owner 键前缀：记录每个槽位的当前占用者
	// 格式: slot_owner:{accountID}:{slotIndex}
	slotOwnerKeyPrefix = "slot_owner:"

	// Slot 响应结束时间键前缀：记录每个槽位的响应结束时间戳
	// 格式: slot_response_end:{accountID}:{slotIndex}
	slotResponseEndKeyPrefix = "slot_response_end:"

	// Slot 响应结束时间 TTL（秒），设置为 1 小时足够覆盖正常使用场景
	slotResponseEndTTLSeconds = 3600

	// Haiku 模型同一 session 最大并行数
	haikuMaxParallel = 3

	// 账号 RPM 限制相关常量
	// 键格式: rpm_limit:{accountID}
	rpmLimitKeyPrefix = "rpm_limit:"
	rpmWindowSeconds  = 60 // 1 分钟滑动窗口
	rpmLimitTTL       = 120 // TTL 设置为 2 分钟，确保数据不会过早过期

	// 账号 30 分钟总量限制相关常量
	// 键格式: rate_30m:{accountID}
	rate30mKeyPrefix    = "rate_30m:"
	rate30mWindowSeconds = 1800 // 30 分钟滑动窗口
	rate30mLimitTTL     = 3600 // TTL 设置为 1 小时

	// 账号暂停调度标记
	// 键格式: account_paused:{accountID}
	accountPausedKeyPrefix = "account_paused:"
)

var (
	// acquireScript 使用有序集合计数并在未达上限时添加槽位
	// 使用 Redis TIME 命令获取服务器时间，避免多实例时钟不同步问题
	// KEYS[1] = 有序集合键 (concurrency:account:{id} / concurrency:user:{id})
	// ARGV[1] = maxConcurrency
	// ARGV[2] = TTL（秒）
	// ARGV[3] = requestID
	acquireScript = redis.NewScript(`
		local key = KEYS[1]
		local maxConcurrency = tonumber(ARGV[1])
		local ttl = tonumber(ARGV[2])
		local requestID = ARGV[3]

		-- 使用 Redis 服务器时间，确保多实例时钟一致
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - ttl

		-- 清理过期槽位
		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)

		-- 检查是否已存在（支持重试场景刷新时间戳）
		local exists = redis.call('ZSCORE', key, requestID)
		if exists ~= false then
			redis.call('ZADD', key, now, requestID)
			redis.call('EXPIRE', key, ttl)
			return 1
		end

		-- 检查是否达到并发上限
		local count = redis.call('ZCARD', key)
		if count < maxConcurrency then
			redis.call('ZADD', key, now, requestID)
			redis.call('EXPIRE', key, ttl)
			return 1
		end

		return 0
	`)

	// acquireSlotByIndexScript 尝试获取指定编号的槽位
	// 槽位编号格式: slot_{index}，确保同一个用户 session 始终映射到同一个槽位
	// KEYS[1] = 有序集合键 (concurrency:account:{id})
	// ARGV[1] = TTL（秒）
	// ARGV[2] = slotIndex (槽位编号，如 0, 1, 2...)
	// 返回: 1=成功获取, 0=槽位被占用
	acquireSlotByIndexScript = redis.NewScript(`
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local slotIndex = ARGV[2]
		local slotID = 'slot_' .. slotIndex

		-- 使用 Redis 服务器时间，确保多实例时钟一致
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - ttl

		-- 清理过期槽位
		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)

		-- 检查该槽位是否已存在（支持重试场景刷新时间戳）
		local exists = redis.call('ZSCORE', key, slotID)
		if exists ~= false then
			-- 槽位已被占用，返回失败
			return 0
		end

		-- 槽位空闲，获取它
		redis.call('ZADD', key, now, slotID)
		redis.call('EXPIRE', key, ttl)
		return 1
	`)

	// getCountScript 统计有序集合中的槽位数量并清理过期条目
	// 使用 Redis TIME 命令获取服务器时间
	// KEYS[1] = 有序集合键
	// ARGV[1] = TTL（秒）
	getCountScript = redis.NewScript(`
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])

		-- 使用 Redis 服务器时间
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - ttl

		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)
		return redis.call('ZCARD', key)
	`)

	// incrementWaitScript - only sets TTL on first creation to avoid refreshing
	// KEYS[1] = wait queue key
	// ARGV[1] = maxWait
	// ARGV[2] = TTL in seconds
	incrementWaitScript = redis.NewScript(`
		local current = redis.call('GET', KEYS[1])
		if current == false then
			current = 0
		else
			current = tonumber(current)
		end

		if current >= tonumber(ARGV[1]) then
			return 0
		end

		local newVal = redis.call('INCR', KEYS[1])

		-- Only set TTL on first creation to avoid refreshing zombie data
		if newVal == 1 then
			redis.call('EXPIRE', KEYS[1], ARGV[2])
		end

			return 1
		`)

	// incrementAccountWaitScript - account-level wait queue count
	incrementAccountWaitScript = redis.NewScript(`
			local current = redis.call('GET', KEYS[1])
			if current == false then
				current = 0
			else
				current = tonumber(current)
			end

			if current >= tonumber(ARGV[1]) then
				return 0
			end

			local newVal = redis.call('INCR', KEYS[1])

			-- Only set TTL on first creation to avoid refreshing zombie data
			if newVal == 1 then
				redis.call('EXPIRE', KEYS[1], ARGV[2])
			end

			return 1
		`)

	// decrementWaitScript - same as before
	decrementWaitScript = redis.NewScript(`
			local current = redis.call('GET', KEYS[1])
			if current ~= false and tonumber(current) > 0 then
				redis.call('DECR', KEYS[1])
			end
			return 1
		`)

	// getAccountsLoadBatchScript - batch load query with expired slot cleanup
	// ARGV[1] = slot TTL (seconds)
	// ARGV[2..n] = accountID1, maxConcurrency1, accountID2, maxConcurrency2, ...
	getAccountsLoadBatchScript = redis.NewScript(`
			local result = {}
			local slotTTL = tonumber(ARGV[1])

			-- Get current server time
			local timeResult = redis.call('TIME')
			local nowSeconds = tonumber(timeResult[1])
			local cutoffTime = nowSeconds - slotTTL

			local i = 2
			while i <= #ARGV do
				local accountID = ARGV[i]
				local maxConcurrency = tonumber(ARGV[i + 1])

				local slotKey = 'concurrency:account:' .. accountID

				-- Clean up expired slots before counting
				redis.call('ZREMRANGEBYSCORE', slotKey, '-inf', cutoffTime)
				local currentConcurrency = redis.call('ZCARD', slotKey)

				local waitKey = 'wait:account:' .. accountID
				local waitingCount = redis.call('GET', waitKey)
				if waitingCount == false then
					waitingCount = 0
				else
					waitingCount = tonumber(waitingCount)
				end

				local loadRate = 0
				if maxConcurrency > 0 then
					loadRate = math.floor((currentConcurrency + waitingCount) * 100 / maxConcurrency)
				end

				table.insert(result, accountID)
				table.insert(result, currentConcurrency)
				table.insert(result, waitingCount)
				table.insert(result, loadRate)

				i = i + 2
			end

			return result
		`)

	// cleanupExpiredSlotsScript - remove expired slots
	// KEYS[1] = concurrency:account:{accountID}
	// ARGV[1] = TTL (seconds)
	cleanupExpiredSlotsScript = redis.NewScript(`
			local key = KEYS[1]
			local ttl = tonumber(ARGV[1])
			local timeResult = redis.call('TIME')
			local now = tonumber(timeResult[1])
			local expireBefore = now - ttl
			return redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)
		`)

	// acquireSlotWithFallbackScript - 优先获取目标槽位，失败则获取其他可用槽位
	// KEYS[1] = 有序集合键 (concurrency:account:{id})
	// ARGV[1] = TTL（秒）
	// ARGV[2] = targetSlotIndex (目标槽位编号)
	// ARGV[3] = maxConcurrency (最大并发数)
	// 返回: 获取到的槽位编号（0-based），-1 表示全部满了
	acquireSlotWithFallbackScript = redis.NewScript(`
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local targetSlot = tonumber(ARGV[2])
		local maxConcurrency = tonumber(ARGV[3])

		-- 使用 Redis 服务器时间
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - ttl

		-- 清理过期槽位
		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)

		-- 1. 先尝试获取目标槽位
		local targetSlotID = 'slot_' .. targetSlot
		local exists = redis.call('ZSCORE', key, targetSlotID)
		if exists == false then
			redis.call('ZADD', key, now, targetSlotID)
			redis.call('EXPIRE', key, ttl)
			return targetSlot
		end

		-- 2. 目标槽位被占，尝试其他槽位
		for i = 0, maxConcurrency - 1 do
			if i ~= targetSlot then
				local slotID = 'slot_' .. i
				local slotExists = redis.call('ZSCORE', key, slotID)
				if slotExists == false then
					redis.call('ZADD', key, now, slotID)
					redis.call('EXPIRE', key, ttl)
					return i
				end
			end
		end

		-- 3. 全部满了
		return -1
	`)

	// acquireSlotInRangeScript - 在指定范围内获取槽位（硬隔离，不跨范围 fallback）
	// 用于模型槽位池隔离：Opus 池和 Sonnet 池各自独立
	// KEYS[1] = 有序集合键 (concurrency:account:{id})
	// ARGV[1] = TTL（秒）
	// ARGV[2] = targetSlotIndex (目标槽位编号)
	// ARGV[3] = rangeStart (范围起始，包含)
	// ARGV[4] = rangeEnd (范围结束，不包含)
	// 返回: 获取到的槽位编号，-1 表示范围内全部满了
	acquireSlotInRangeScript = redis.NewScript(`
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local targetSlot = tonumber(ARGV[2])
		local rangeStart = tonumber(ARGV[3])
		local rangeEnd = tonumber(ARGV[4])

		-- 使用 Redis 服务器时间
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - ttl

		-- 清理过期槽位
		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)

		-- 1. 先尝试获取目标槽位（如果在范围内）
		if targetSlot >= rangeStart and targetSlot < rangeEnd then
			local targetSlotID = 'slot_' .. targetSlot
			local exists = redis.call('ZSCORE', key, targetSlotID)
			if exists == false then
				redis.call('ZADD', key, now, targetSlotID)
				redis.call('EXPIRE', key, ttl)
				return targetSlot
			end
		end

		-- 2. 目标槽位被占或不在范围内，在范围内尝试其他槽位
		for i = rangeStart, rangeEnd - 1 do
			if i ~= targetSlot then
				local slotID = 'slot_' .. i
				local slotExists = redis.call('ZSCORE', key, slotID)
				if slotExists == false then
					redis.call('ZADD', key, now, slotID)
					redis.call('EXPIRE', key, ttl)
					return i
				end
			end
		end

		-- 3. 范围内全部满了（硬隔离，不跨范围 fallback）
		return -1
	`)

	// acquireSessionMutexScript - 获取 session 互斥锁（防止同一 session 并发）
	// KEYS[1] = session_mutex:{accountID}:{sessionHash}
	// ARGV[1] = TTL（秒）
	// ARGV[2] = requestID（用于标识锁的持有者）
	// 返回: 1=成功获取, 0=已被占用
	acquireSessionMutexScript = redis.NewScript(`
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local requestID = ARGV[2]

		-- 尝试 SETNX
		local result = redis.call('SET', key, requestID, 'NX', 'EX', ttl)
		if result then
			return 1
		end
		return 0
	`)

	// releaseSessionMutexScript - 释放 session 互斥锁（只删除自己持有的锁）
	// KEYS[1] = session_mutex:{accountID}:{sessionHash}
	// ARGV[1] = requestID（必须与获取时的 requestID 一致）
	// 返回: 1=成功释放, 0=锁不存在或不是自己持有的
	releaseSessionMutexScript = redis.NewScript(`
		local key = KEYS[1]
		local requestID = ARGV[1]

		-- 检查锁是否是自己持有的
		local currentOwner = redis.call('GET', key)
		if currentOwner == requestID then
			redis.call('DEL', key)
			return 1
		end
		return 0
	`)

	// acquireSlotWithSessionScript - 获取槽位（支持同一 session 并行，用于 Haiku）
	// 使用 Hash 存储每个槽位的 owner 和并发计数
	// KEYS[1] = slot_owner:{accountID}:{slotIndex}
	// KEYS[2] = concurrency:account:{accountID} (有序集合，用于槽位占用标记)
	// ARGV[1] = TTL（秒）
	// ARGV[2] = slotIndex
	// ARGV[3] = sessionHash（当前请求的 session）
	// ARGV[4] = maxParallel（同一 session 最大并行数）
	// ARGV[5] = requestID（用于标识本次请求）
	// 返回: 1=成功获取, 0=槽位被其他 session 占用或已达并行上限
	acquireSlotWithSessionScript = redis.NewScript(`
		local ownerKey = KEYS[1]
		local slotKey = KEYS[2]
		local ttl = tonumber(ARGV[1])
		local slotIndex = ARGV[2]
		local sessionHash = ARGV[3]
		local maxParallel = tonumber(ARGV[4])
		local requestID = ARGV[5]
		local slotID = 'slot_' .. slotIndex

		-- 使用 Redis 服务器时间
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])

		-- 获取当前 slot owner 信息
		local ownerData = redis.call('HGETALL', ownerKey)
		local currentOwner = nil
		local currentCount = 0
		local expireAt = 0

		for i = 1, #ownerData, 2 do
			if ownerData[i] == 'owner' then
				currentOwner = ownerData[i + 1]
			elseif ownerData[i] == 'count' then
				currentCount = tonumber(ownerData[i + 1])
			elseif ownerData[i] == 'expire' then
				expireAt = tonumber(ownerData[i + 1])
			end
		end

		-- 检查是否过期
		if expireAt > 0 and expireAt < now then
			-- 已过期，清理
			redis.call('DEL', ownerKey)
			redis.call('ZREM', slotKey, slotID)
			currentOwner = nil
			currentCount = 0
		end

		-- 情况1：槽位空闲（Hash 没有 owner）
		if currentOwner == nil or currentCount == 0 then
			-- 检查 ZSET 中是否已被其他请求占用（防止升级过程中的竞态条件）
			-- 在槽位升级流程中，先释放普通槽位再获取 Haiku 槽位，这个窗口期可能被其他请求抢占
			local zsetScore = redis.call('ZSCORE', slotKey, slotID)
			if zsetScore ~= false then
				-- ZSET 中已有占用，说明被其他请求抢占，返回失败
				return 0
			end

			redis.call('HSET', ownerKey, 'owner', sessionHash, 'count', 1, 'expire', now + ttl)
			redis.call('EXPIRE', ownerKey, ttl)
			-- 同时在有序集合中标记槽位被占用
			redis.call('ZADD', slotKey, now, slotID)
			redis.call('EXPIRE', slotKey, ttl)
			return 1
		end

		-- 情况2：同一 session 占用
		if currentOwner == sessionHash then
			if currentCount < maxParallel then
				redis.call('HINCRBY', ownerKey, 'count', 1)
				redis.call('HSET', ownerKey, 'expire', now + ttl)
				redis.call('EXPIRE', ownerKey, ttl)
				-- 刷新有序集合中的时间戳
				redis.call('ZADD', slotKey, now, slotID)
				redis.call('EXPIRE', slotKey, ttl)
				return 1
			else
				-- 已达并行上限
				return 0
			end
		end

		-- 情况3：其他 session 占用
		return 0
	`)

	// releaseSlotWithSessionScript - 释放槽位（支持同一 session 并行）
	// KEYS[1] = slot_owner:{accountID}:{slotIndex}
	// KEYS[2] = concurrency:account:{accountID}
	// ARGV[1] = slotIndex
	// ARGV[2] = sessionHash
	// 返回: 1=成功释放, 0=不是自己持有的
	releaseSlotWithSessionScript = redis.NewScript(`
		local ownerKey = KEYS[1]
		local slotKey = KEYS[2]
		local slotIndex = ARGV[1]
		local sessionHash = ARGV[2]
		local slotID = 'slot_' .. slotIndex

		-- 获取当前 owner
		local currentOwner = redis.call('HGET', ownerKey, 'owner')
		if currentOwner ~= sessionHash then
			return 0
		end

		-- 减少计数
		local newCount = redis.call('HINCRBY', ownerKey, 'count', -1)
		if newCount <= 0 then
			-- 计数归零，删除 owner 记录和槽位标记
			redis.call('DEL', ownerKey)
			redis.call('ZREM', slotKey, slotID)
		end

		return 1
	`)
)

type concurrencyCache struct {
	rdb                 *redis.Client
	slotTTLSeconds      int // 槽位过期时间（秒）
	waitQueueTTLSeconds int // 等待队列过期时间（秒）
}

// NewConcurrencyCache 创建并发控制缓存
// slotTTLMinutes: 槽位过期时间（分钟），0 或负数使用默认值 15 分钟
// waitQueueTTLSeconds: 等待队列过期时间（秒），0 或负数使用 slot TTL
func NewConcurrencyCache(rdb *redis.Client, slotTTLMinutes int, waitQueueTTLSeconds int) service.ConcurrencyCache {
	if slotTTLMinutes <= 0 {
		slotTTLMinutes = defaultSlotTTLMinutes
	}
	if waitQueueTTLSeconds <= 0 {
		waitQueueTTLSeconds = slotTTLMinutes * 60
	}
	return &concurrencyCache{
		rdb:                 rdb,
		slotTTLSeconds:      slotTTLMinutes * 60,
		waitQueueTTLSeconds: waitQueueTTLSeconds,
	}
}

// Helper functions for key generation
func accountSlotKey(accountID int64) string {
	return fmt.Sprintf("%s%d", accountSlotKeyPrefix, accountID)
}

func userSlotKey(userID int64) string {
	return fmt.Sprintf("%s%d", userSlotKeyPrefix, userID)
}

func waitQueueKey(userID int64) string {
	return fmt.Sprintf("%s%d", waitQueueKeyPrefix, userID)
}

func accountWaitKey(accountID int64) string {
	return fmt.Sprintf("%s%d", accountWaitKeyPrefix, accountID)
}

func sessionMutexKey(accountID int64, sessionHash string) string {
	return fmt.Sprintf("%s%d:%s", sessionMutexKeyPrefix, accountID, sessionHash)
}

func slotOwnerKey(accountID int64, slotIndex int) string {
	return fmt.Sprintf("%s%d:%d", slotOwnerKeyPrefix, accountID, slotIndex)
}

// Account slot operations

func (c *concurrencyCache) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
	key := accountSlotKey(accountID)
	// 时间戳在 Lua 脚本内使用 Redis TIME 命令获取，确保多实例时钟一致
	result, err := acquireScript.Run(ctx, c.rdb, []string{key}, maxConcurrency, c.slotTTLSeconds, requestID).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (c *concurrencyCache) ReleaseAccountSlot(ctx context.Context, accountID int64, requestID string) error {
	key := accountSlotKey(accountID)
	return c.rdb.ZRem(ctx, key, requestID).Err()
}

func (c *concurrencyCache) GetAccountConcurrency(ctx context.Context, accountID int64) (int, error) {
	key := accountSlotKey(accountID)
	// 时间戳在 Lua 脚本内使用 Redis TIME 命令获取
	result, err := getCountScript.Run(ctx, c.rdb, []string{key}, c.slotTTLSeconds).Int()
	if err != nil {
		return 0, err
	}
	return result, nil
}

// AcquireAccountSlotByIndex 尝试获取指定编号的槽位
// 用于确保同一个用户 session 始终映射到同一个槽位
func (c *concurrencyCache) AcquireAccountSlotByIndex(ctx context.Context, accountID int64, slotIndex int) (bool, error) {
	key := accountSlotKey(accountID)
	result, err := acquireSlotByIndexScript.Run(ctx, c.rdb, []string{key}, c.slotTTLSeconds, slotIndex).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// ReleaseAccountSlotByIndex 释放指定编号的槽位
func (c *concurrencyCache) ReleaseAccountSlotByIndex(ctx context.Context, accountID int64, slotIndex int) error {
	key := accountSlotKey(accountID)
	slotID := fmt.Sprintf("slot_%d", slotIndex)
	return c.rdb.ZRem(ctx, key, slotID).Err()
}

// AcquireSlotWithFallback 优先获取目标槽位，失败则获取其他可用槽位
// 返回实际获取到的槽位编号，-1 表示全部满了
func (c *concurrencyCache) AcquireSlotWithFallback(ctx context.Context, accountID int64, targetSlot int, maxConcurrency int) (int, error) {
	key := accountSlotKey(accountID)
	result, err := acquireSlotWithFallbackScript.Run(ctx, c.rdb, []string{key}, c.slotTTLSeconds, targetSlot, maxConcurrency).Int()
	if err != nil {
		return -1, err
	}
	return result, nil
}

// AcquireSlotInRange 在指定范围内获取槽位（硬隔离，不跨范围 fallback）
// 用于模型槽位池隔离：Opus 池和 Sonnet 池各自独立
// rangeStart: 范围起始（包含）
// rangeEnd: 范围结束（不包含）
// 返回实际获取到的槽位编号，-1 表示范围内全部满了
func (c *concurrencyCache) AcquireSlotInRange(ctx context.Context, accountID int64, targetSlot int, rangeStart int, rangeEnd int) (int, error) {
	key := accountSlotKey(accountID)
	result, err := acquireSlotInRangeScript.Run(ctx, c.rdb, []string{key}, c.slotTTLSeconds, targetSlot, rangeStart, rangeEnd).Int()
	if err != nil {
		return -1, err
	}
	return result, nil
}

// AcquireSessionMutex 获取 session 互斥锁，防止同一 session 并发请求
// 返回 true 表示成功获取锁，false 表示该 session 已有正在进行的请求
func (c *concurrencyCache) AcquireSessionMutex(ctx context.Context, accountID int64, sessionHash string, requestID string) (bool, error) {
	key := sessionMutexKey(accountID, sessionHash)
	result, err := acquireSessionMutexScript.Run(ctx, c.rdb, []string{key}, sessionMutexTTLSeconds, requestID).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// ReleaseSessionMutex 释放 session 互斥锁（只删除自己持有的锁）
func (c *concurrencyCache) ReleaseSessionMutex(ctx context.Context, accountID int64, sessionHash string, requestID string) error {
	key := sessionMutexKey(accountID, sessionHash)
	_, err := releaseSessionMutexScript.Run(ctx, c.rdb, []string{key}, requestID).Int()
	return err
}

// AcquireSlotWithSession 获取槽位（支持同一 session 并行，用于 Haiku 模型）
// 同一 session 最多可以有 haikuMaxParallel 个并行请求共享同一个槽位
// 不同 session 不能共享槽位（返回失败，调用者可以 fallback 或等待）
func (c *concurrencyCache) AcquireSlotWithSession(ctx context.Context, accountID int64, slotIndex int, sessionHash string, requestID string) (bool, error) {
	ownerKey := slotOwnerKey(accountID, slotIndex)
	slotKey := accountSlotKey(accountID)
	result, err := acquireSlotWithSessionScript.Run(ctx, c.rdb, []string{ownerKey, slotKey},
		c.slotTTLSeconds, slotIndex, sessionHash, haikuMaxParallel, requestID).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// ReleaseSlotWithSession 释放槽位（支持同一 session 并行）
// 减少并发计数，当计数归零时删除 owner 记录
func (c *concurrencyCache) ReleaseSlotWithSession(ctx context.Context, accountID int64, slotIndex int, sessionHash string) error {
	ownerKey := slotOwnerKey(accountID, slotIndex)
	slotKey := accountSlotKey(accountID)
	_, err := releaseSlotWithSessionScript.Run(ctx, c.rdb, []string{ownerKey, slotKey}, slotIndex, sessionHash).Int()
	return err
}

// User slot operations

func (c *concurrencyCache) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
	key := userSlotKey(userID)
	// 时间戳在 Lua 脚本内使用 Redis TIME 命令获取，确保多实例时钟一致
	result, err := acquireScript.Run(ctx, c.rdb, []string{key}, maxConcurrency, c.slotTTLSeconds, requestID).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (c *concurrencyCache) ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error {
	key := userSlotKey(userID)
	return c.rdb.ZRem(ctx, key, requestID).Err()
}

func (c *concurrencyCache) GetUserConcurrency(ctx context.Context, userID int64) (int, error) {
	key := userSlotKey(userID)
	// 时间戳在 Lua 脚本内使用 Redis TIME 命令获取
	result, err := getCountScript.Run(ctx, c.rdb, []string{key}, c.slotTTLSeconds).Int()
	if err != nil {
		return 0, err
	}
	return result, nil
}

// Wait queue operations

func (c *concurrencyCache) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	key := waitQueueKey(userID)
	result, err := incrementWaitScript.Run(ctx, c.rdb, []string{key}, maxWait, c.waitQueueTTLSeconds).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (c *concurrencyCache) DecrementWaitCount(ctx context.Context, userID int64) error {
	key := waitQueueKey(userID)
	_, err := decrementWaitScript.Run(ctx, c.rdb, []string{key}).Result()
	return err
}

// Account wait queue operations

func (c *concurrencyCache) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	key := accountWaitKey(accountID)
	result, err := incrementAccountWaitScript.Run(ctx, c.rdb, []string{key}, maxWait, c.waitQueueTTLSeconds).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (c *concurrencyCache) DecrementAccountWaitCount(ctx context.Context, accountID int64) error {
	key := accountWaitKey(accountID)
	_, err := decrementWaitScript.Run(ctx, c.rdb, []string{key}).Result()
	return err
}

func (c *concurrencyCache) GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error) {
	key := accountWaitKey(accountID)
	val, err := c.rdb.Get(ctx, key).Int()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return val, nil
}

func (c *concurrencyCache) GetAccountsLoadBatch(ctx context.Context, accounts []service.AccountWithConcurrency) (map[int64]*service.AccountLoadInfo, error) {
	if len(accounts) == 0 {
		return map[int64]*service.AccountLoadInfo{}, nil
	}

	args := []any{c.slotTTLSeconds}
	for _, acc := range accounts {
		args = append(args, acc.ID, acc.MaxConcurrency)
	}

	result, err := getAccountsLoadBatchScript.Run(ctx, c.rdb, []string{}, args...).Slice()
	if err != nil {
		return nil, err
	}

	loadMap := make(map[int64]*service.AccountLoadInfo)
	for i := 0; i < len(result); i += 4 {
		if i+3 >= len(result) {
			break
		}

		accountID, _ := strconv.ParseInt(fmt.Sprintf("%v", result[i]), 10, 64)
		currentConcurrency, _ := strconv.Atoi(fmt.Sprintf("%v", result[i+1]))
		waitingCount, _ := strconv.Atoi(fmt.Sprintf("%v", result[i+2]))
		loadRate, _ := strconv.Atoi(fmt.Sprintf("%v", result[i+3]))

		loadMap[accountID] = &service.AccountLoadInfo{
			AccountID:          accountID,
			CurrentConcurrency: currentConcurrency,
			WaitingCount:       waitingCount,
			LoadRate:           loadRate,
		}
	}

	return loadMap, nil
}

func (c *concurrencyCache) CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error {
	key := accountSlotKey(accountID)
	_, err := cleanupExpiredSlotsScript.Run(ctx, c.rdb, []string{key}, c.slotTTLSeconds).Result()
	return err
}

// CleanupAllExpiredSlots 使用 SCAN 遍历所有 concurrency:* 键并清理过期槽位
// 返回清理的槽位总数
func (c *concurrencyCache) CleanupAllExpiredSlots(ctx context.Context) (int, error) {
	var totalCleaned int
	var cursor uint64

	// 遍历所有 concurrency:account:* 和 concurrency:user:* 键
	for {
		keys, nextCursor, err := c.rdb.Scan(ctx, cursor, "concurrency:*", 100).Result()
		if err != nil {
			return totalCleaned, err
		}

		for _, key := range keys {
			// 跳过等待队列计数器（不是有序集合）
			if strings.HasPrefix(key, waitQueueKeyPrefix) {
				continue
			}

			// 对有序集合执行清理
			cleaned, err := cleanupExpiredSlotsScript.Run(ctx, c.rdb, []string{key}, c.slotTTLSeconds).Int()
			if err != nil {
				// 忽略单个键的错误，继续处理其他键
				continue
			}
			totalCleaned += cleaned
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return totalCleaned, nil
}

// ClearAllSlots 清空所有槽位（服务启动时调用）
// 遍历所有 concurrency:account:* 和 concurrency:user:* 键并删除
// 返回删除的键总数
func (c *concurrencyCache) ClearAllSlots(ctx context.Context) (int, error) {
	var totalCleared int
	var cursor uint64

	for {
		keys, nextCursor, err := c.rdb.Scan(ctx, cursor, "concurrency:*", 100).Result()
		if err != nil {
			return totalCleared, err
		}

		for _, key := range keys {
			// 跳过等待队列计数器
			if strings.HasPrefix(key, waitQueueKeyPrefix) {
				continue
			}

			// 删除整个键
			deleted, err := c.rdb.Del(ctx, key).Result()
			if err != nil {
				continue
			}
			totalCleared += int(deleted)
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	// 同时清理 session mutex 键
	cursor = 0
	for {
		keys, nextCursor, err := c.rdb.Scan(ctx, cursor, "session_mutex:*", 100).Result()
		if err != nil {
			return totalCleared, err
		}

		for _, key := range keys {
			deleted, err := c.rdb.Del(ctx, key).Result()
			if err != nil {
				continue
			}
			totalCleared += int(deleted)
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	// 清理 slot owner 键
	cursor = 0
	for {
		keys, nextCursor, err := c.rdb.Scan(ctx, cursor, "slot_owner:*", 100).Result()
		if err != nil {
			return totalCleared, err
		}

		for _, key := range keys {
			deleted, err := c.rdb.Del(ctx, key).Result()
			if err != nil {
				continue
			}
			totalCleared += int(deleted)
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return totalCleared, nil
}

// SetSlotResponseEndTime 记录 slot 的响应结束时间
// 用于用户输入节奏控制
func (c *concurrencyCache) SetSlotResponseEndTime(ctx context.Context, accountID int64, slotIndex int, timestamp int64) error {
	key := fmt.Sprintf("%s%d:%d", slotResponseEndKeyPrefix, accountID, slotIndex)
	return c.rdb.Set(ctx, key, timestamp, time.Duration(slotResponseEndTTLSeconds)*time.Second).Err()
}

// GetSlotResponseEndTime 获取 slot 的上次响应结束时间
// 返回 Unix 时间戳，如果没有记录则返回 0
func (c *concurrencyCache) GetSlotResponseEndTime(ctx context.Context, accountID int64, slotIndex int) (int64, error) {
	key := fmt.Sprintf("%s%d:%d", slotResponseEndKeyPrefix, accountID, slotIndex)
	result, err := c.rdb.Get(ctx, key).Int64()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, nil // 没有记录，返回 0
		}
		return 0, err
	}
	return result, nil
}

// ============================================
// 账号 RPM 限制（滑动窗口）
// ============================================

func rpmLimitKey(accountID int64) string {
	return fmt.Sprintf("%s%d", rpmLimitKeyPrefix, accountID)
}

// RecordAccountRequest 记录账号请求（用于 RPM 统计）
// 使用 ZSET 存储请求时间戳，自动清理过期记录
func (c *concurrencyCache) RecordAccountRequest(ctx context.Context, accountID int64) error {
	key := rpmLimitKey(accountID)
	now := time.Now().UnixMilli()
	cutoff := now - int64(rpmWindowSeconds*1000)

	pipe := c.rdb.Pipeline()
	// 清理过期记录
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", cutoff))
	// 添加当前请求
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: fmt.Sprintf("%d", now)})
	// 设置 TTL
	pipe.Expire(ctx, key, time.Duration(rpmLimitTTL)*time.Second)
	_, err := pipe.Exec(ctx)
	return err
}

// GetAccountRPM 获取账号当前 RPM（过去 60 秒的请求数）
func (c *concurrencyCache) GetAccountRPM(ctx context.Context, accountID int64) (int, error) {
	key := rpmLimitKey(accountID)
	now := time.Now().UnixMilli()
	cutoff := now - int64(rpmWindowSeconds*1000)

	// 先清理过期记录
	c.rdb.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", cutoff))
	// 统计当前数量
	count, err := c.rdb.ZCard(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// GetAccountOldestRequestTime 获取账号最早的请求时间（毫秒时间戳）
// 用于计算需要等待多久才能有新配额
func (c *concurrencyCache) GetAccountOldestRequestTime(ctx context.Context, accountID int64) (int64, error) {
	key := rpmLimitKey(accountID)
	now := time.Now().UnixMilli()
	cutoff := now - int64(rpmWindowSeconds*1000)

	// 先清理过期记录
	c.rdb.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", cutoff))
	// 获取最早的记录
	result, err := c.rdb.ZRangeWithScores(ctx, key, 0, 0).Result()
	if err != nil {
		return 0, err
	}
	if len(result) == 0 {
		return 0, nil
	}
	return int64(result[0].Score), nil
}

// ============================================
// 账号 30 分钟总量限制
// ============================================

func rate30mKey(accountID int64) string {
	return fmt.Sprintf("%s%d", rate30mKeyPrefix, accountID)
}

// RecordAccountRequest30m 记录账号请求（用于 30 分钟总量统计）
func (c *concurrencyCache) RecordAccountRequest30m(ctx context.Context, accountID int64) error {
	key := rate30mKey(accountID)
	now := time.Now().UnixMilli()
	cutoff := now - int64(rate30mWindowSeconds*1000)

	pipe := c.rdb.Pipeline()
	// 清理过期记录
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", cutoff))
	// 添加当前请求
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: fmt.Sprintf("%d", now)})
	// 设置 TTL
	pipe.Expire(ctx, key, time.Duration(rate30mLimitTTL)*time.Second)
	_, err := pipe.Exec(ctx)
	return err
}

// GetAccountRequestCount30m 获取账号过去 30 分钟的请求数
func (c *concurrencyCache) GetAccountRequestCount30m(ctx context.Context, accountID int64) (int, error) {
	key := rate30mKey(accountID)
	now := time.Now().UnixMilli()
	cutoff := now - int64(rate30mWindowSeconds*1000)

	// 先清理过期记录
	c.rdb.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", cutoff))
	// 统计当前数量
	count, err := c.rdb.ZCard(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// ============================================
// 账号暂停调度标记
// ============================================

func accountPausedKey(accountID int64) string {
	return fmt.Sprintf("%s%d", accountPausedKeyPrefix, accountID)
}

// SetAccountPaused 设置账号暂停调度标记
func (c *concurrencyCache) SetAccountPaused(ctx context.Context, accountID int64, duration time.Duration) error {
	key := accountPausedKey(accountID)
	return c.rdb.Set(ctx, key, "1", duration).Err()
}

// IsAccountPaused 检查账号是否被暂停调度
func (c *concurrencyCache) IsAccountPaused(ctx context.Context, accountID int64) (bool, error) {
	key := accountPausedKey(accountID)
	result, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}
