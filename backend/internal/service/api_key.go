package service

import "time"

type APIKey struct {
	ID          int64
	UserID      int64
	Key         string
	Name        string
	GroupID     *int64
	Status      string
	IPWhitelist []string
	IPBlacklist []string
	UsageLimit  *float64 // 用量限制（美元），nil = 无限制
	TotalUsage  float64  // 累计用量（美元）
	CreatedAt   time.Time
	UpdatedAt   time.Time
	User        *User
	Group       *Group
}

func (k *APIKey) IsActive() bool {
	return k.Status == StatusActive
}

// IsUsageLimitExceeded 检查是否超过用量限制
func (k *APIKey) IsUsageLimitExceeded() bool {
	if k.UsageLimit == nil {
		return false // 无限制
	}
	return k.TotalUsage >= *k.UsageLimit
}
