package service

import "time"

type Group struct {
	ID             int64
	Name           string
	Description    string
	Platform       string
	RateMultiplier float64
	IsExclusive    bool
	Status         string

	SubscriptionType    string
	DailyLimitUSD       *float64
	WeeklyLimitUSD      *float64
	MonthlyLimitUSD     *float64
	DefaultValidityDays int

	// 图片生成计费配置（antigravity 和 gemini 平台使用）
	ImagePrice1K *float64
	ImagePrice2K *float64
	ImagePrice4K *float64

	// Claude Code 客户端限制
	ClaudeCodeOnly  bool
	FallbackGroupID *int64

	// 模型白名单
	AllowedModels []string

	// 模型映射
	ModelMapping map[string]string

	CreatedAt time.Time
	UpdatedAt time.Time

	AccountGroups []AccountGroup
	AccountCount  int64
}

func (g *Group) IsActive() bool {
	return g.Status == StatusActive
}

func (g *Group) IsSubscriptionType() bool {
	return g.SubscriptionType == SubscriptionTypeSubscription
}

func (g *Group) IsFreeSubscription() bool {
	return g.IsSubscriptionType() && g.RateMultiplier == 0
}

func (g *Group) HasDailyLimit() bool {
	return g.DailyLimitUSD != nil && *g.DailyLimitUSD > 0
}

func (g *Group) HasWeeklyLimit() bool {
	return g.WeeklyLimitUSD != nil && *g.WeeklyLimitUSD > 0
}

func (g *Group) HasMonthlyLimit() bool {
	return g.MonthlyLimitUSD != nil && *g.MonthlyLimitUSD > 0
}

// GetImagePrice 根据 image_size 返回对应的图片生成价格
// 如果分组未配置价格，返回 nil（调用方应使用默认值）
func (g *Group) GetImagePrice(imageSize string) *float64 {
	switch imageSize {
	case "1K":
		return g.ImagePrice1K
	case "2K":
		return g.ImagePrice2K
	case "4K":
		return g.ImagePrice4K
	default:
		// 未知尺寸默认按 2K 计费
		return g.ImagePrice2K
	}
}

// IsModelAllowed 检查模型是否在白名单中
// 空白名单表示允许所有模型
func (g *Group) IsModelAllowed(model string) bool {
	if len(g.AllowedModels) == 0 {
		return true // 空白名单允许所有
	}
	for _, m := range g.AllowedModels {
		if m == model {
			return true
		}
	}
	return false
}

// MapModel 将请求的模型名映射为实际发送的模型名
// 如果没有映射规则，返回原模型名
func (g *Group) MapModel(model string) string {
	if len(g.ModelMapping) == 0 {
		return model
	}
	if mapped, ok := g.ModelMapping[model]; ok && mapped != "" {
		return mapped
	}
	return model
}
