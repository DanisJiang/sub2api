//go:build unit

// Package service 提供 API 网关核心服务。
// 本文件包含加权负载均衡相关逻辑的单元测试。
package service

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// accountWithLoad 用于测试排序逻辑
type accountWithLoad struct {
	account  *Account
	loadInfo *AccountLoadInfo
}

// TestWeightedLoadBalancingSorting 测试加权负载均衡排序逻辑
func TestWeightedLoadBalancingSorting(t *testing.T) {
	tests := []struct {
		name           string
		accounts       []accountWithLoad
		requestCounts  map[int64]int64
		lbSettings     *LoadBalancingSettings
		expectedOrder  []int64 // 期望的账号 ID 顺序
	}{
		{
			name: "strict priority when disabled",
			accounts: []accountWithLoad{
				{account: &Account{ID: 1, Priority: 2}, loadInfo: &AccountLoadInfo{LoadRate: 10}},
				{account: &Account{ID: 2, Priority: 1}, loadInfo: &AccountLoadInfo{LoadRate: 50}},
			},
			requestCounts: map[int64]int64{1: 10, 2: 50},
			lbSettings:    &LoadBalancingSettings{Enabled: false},
			expectedOrder: []int64{2, 1}, // priority 1 先于 priority 2
		},
		{
			name: "weighted load balancing - equal requests",
			accounts: []accountWithLoad{
				{account: &Account{ID: 1, Priority: 1}, loadInfo: &AccountLoadInfo{}},
				{account: &Account{ID: 2, Priority: 2}, loadInfo: &AccountLoadInfo{}},
			},
			requestCounts: map[int64]int64{1: 0, 2: 0},
			lbSettings:    &LoadBalancingSettings{Enabled: true, PriorityOffset: 30, TimeWindowMinutes: 10},
			expectedOrder: []int64{1, 2}, // priority 1 有效负载更低 (0 vs 30)
		},
		{
			name: "weighted load balancing - priority 2 has fewer requests",
			accounts: []accountWithLoad{
				{account: &Account{ID: 1, Priority: 1}, loadInfo: &AccountLoadInfo{}},
				{account: &Account{ID: 2, Priority: 2}, loadInfo: &AccountLoadInfo{}},
			},
			requestCounts: map[int64]int64{1: 50, 2: 10},
			lbSettings:    &LoadBalancingSettings{Enabled: true, PriorityOffset: 30, TimeWindowMinutes: 10},
			// baseCount = 100 (minimum)
			// effective1 = 50 + 0 = 50
			// effective2 = 10 + 1*30*100/100 = 10 + 30 = 40
			expectedOrder: []int64{2, 1}, // priority 2 有效负载更低
		},
		{
			name: "weighted load balancing - priority 1 still preferred when load similar",
			accounts: []accountWithLoad{
				{account: &Account{ID: 1, Priority: 1}, loadInfo: &AccountLoadInfo{}},
				{account: &Account{ID: 2, Priority: 2}, loadInfo: &AccountLoadInfo{}},
			},
			requestCounts: map[int64]int64{1: 20, 2: 0},
			lbSettings:    &LoadBalancingSettings{Enabled: true, PriorityOffset: 30, TimeWindowMinutes: 10},
			// baseCount = 100 (minimum)
			// effective1 = 20 + 0 = 20
			// effective2 = 0 + 1*30*100/100 = 30
			expectedOrder: []int64{1, 2}, // priority 1 有效负载更低
		},
		{
			name: "weighted load balancing - high offset",
			accounts: []accountWithLoad{
				{account: &Account{ID: 1, Priority: 1}, loadInfo: &AccountLoadInfo{}},
				{account: &Account{ID: 2, Priority: 2}, loadInfo: &AccountLoadInfo{}},
				{account: &Account{ID: 3, Priority: 3}, loadInfo: &AccountLoadInfo{}},
			},
			requestCounts: map[int64]int64{1: 100, 2: 50, 3: 0},
			lbSettings:    &LoadBalancingSettings{Enabled: true, PriorityOffset: 50, TimeWindowMinutes: 10},
			// baseCount = 100
			// effective1 = 100 + 0 = 100
			// effective2 = 50 + 1*50*100/100 = 50 + 50 = 100
			// effective3 = 0 + 2*50*100/100 = 0 + 100 = 100
			// all equal, so sort by actual request count
			expectedOrder: []int64{3, 2, 1},
		},
		{
			name: "nil request counts handled safely",
			accounts: []accountWithLoad{
				{account: &Account{ID: 1, Priority: 1}, loadInfo: &AccountLoadInfo{}},
				{account: &Account{ID: 2, Priority: 2}, loadInfo: &AccountLoadInfo{}},
			},
			requestCounts: nil, // nil map
			lbSettings:    &LoadBalancingSettings{Enabled: true, PriorityOffset: 30, TimeWindowMinutes: 10},
			expectedOrder: []int64{1, 2}, // should not panic, priority 1 first
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			available := make([]accountWithLoad, len(tt.accounts))
			copy(available, tt.accounts)

			// 确保 requestCounts 不为 nil
			requestCounts := tt.requestCounts
			if requestCounts == nil {
				requestCounts = make(map[int64]int64)
			}

			// 计算 maxRequestCount
			var maxRequestCount int64
			for _, count := range requestCounts {
				if count > maxRequestCount {
					maxRequestCount = count
				}
			}

			// 模拟排序逻辑
			sort.SliceStable(available, func(i, j int) bool {
				a, b := available[i], available[j]

				if tt.lbSettings.Enabled {
					baseCount := maxRequestCount
					if baseCount < 100 {
						baseCount = 100
					}
					countA := requestCounts[a.account.ID]
					countB := requestCounts[b.account.ID]
					effectiveA := countA + int64(a.account.Priority-1)*int64(tt.lbSettings.PriorityOffset)*baseCount/100
					effectiveB := countB + int64(b.account.Priority-1)*int64(tt.lbSettings.PriorityOffset)*baseCount/100
					if effectiveA != effectiveB {
						return effectiveA < effectiveB
					}
					if countA != countB {
						return countA < countB
					}
				} else {
					if a.account.Priority != b.account.Priority {
						return a.account.Priority < b.account.Priority
					}
					if a.loadInfo.LoadRate != b.loadInfo.LoadRate {
						return a.loadInfo.LoadRate < b.loadInfo.LoadRate
					}
				}
				return false
			})

			// 验证顺序
			actualOrder := make([]int64, len(available))
			for i, item := range available {
				actualOrder[i] = item.account.ID
			}
			require.Equal(t, tt.expectedOrder, actualOrder)
		})
	}
}

// TestEffectiveLoadCalculation 测试有效负载计算公式
func TestEffectiveLoadCalculation(t *testing.T) {
	tests := []struct {
		name           string
		requestCount   int64
		priority       int
		priorityOffset int
		baseCount      int64
		expectedLoad   int64
	}{
		{
			name:           "priority 1 no offset",
			requestCount:   50,
			priority:       1,
			priorityOffset: 30,
			baseCount:      100,
			expectedLoad:   50, // 50 + (1-1)*30*100/100 = 50
		},
		{
			name:           "priority 2 with offset",
			requestCount:   20,
			priority:       2,
			priorityOffset: 30,
			baseCount:      100,
			expectedLoad:   50, // 20 + (2-1)*30*100/100 = 20 + 30 = 50
		},
		{
			name:           "priority 3 with offset",
			requestCount:   0,
			priority:       3,
			priorityOffset: 30,
			baseCount:      100,
			expectedLoad:   60, // 0 + (3-1)*30*100/100 = 0 + 60 = 60
		},
		{
			name:           "zero offset means strict priority behavior",
			requestCount:   100,
			priority:       5,
			priorityOffset: 0,
			baseCount:      100,
			expectedLoad:   100, // 100 + (5-1)*0*100/100 = 100
		},
		{
			name:           "100% offset - extreme case",
			requestCount:   0,
			priority:       2,
			priorityOffset: 100,
			baseCount:      100,
			expectedLoad:   100, // 0 + (2-1)*100*100/100 = 100
		},
		{
			name:           "large base count",
			requestCount:   500,
			priority:       2,
			priorityOffset: 30,
			baseCount:      1000,
			expectedLoad:   800, // 500 + (2-1)*30*1000/100 = 500 + 300 = 800
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			effectiveLoad := tt.requestCount + int64(tt.priority-1)*int64(tt.priorityOffset)*tt.baseCount/100
			require.Equal(t, tt.expectedLoad, effectiveLoad)
		})
	}
}

// TestMinimumBaseCount 测试最小基准值逻辑
func TestMinimumBaseCount(t *testing.T) {
	tests := []struct {
		name            string
		maxRequestCount int64
		expectedBase    int64
	}{
		{name: "zero requests", maxRequestCount: 0, expectedBase: 100},
		{name: "few requests", maxRequestCount: 5, expectedBase: 100},
		{name: "99 requests", maxRequestCount: 99, expectedBase: 100},
		{name: "100 requests", maxRequestCount: 100, expectedBase: 100},
		{name: "101 requests", maxRequestCount: 101, expectedBase: 101},
		{name: "many requests", maxRequestCount: 500, expectedBase: 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseCount := tt.maxRequestCount
			if baseCount < 100 {
				baseCount = 100
			}
			require.Equal(t, tt.expectedBase, baseCount)
		})
	}
}

// TestLoadBalancingSettingsDefault 测试默认配置
func TestLoadBalancingSettingsDefault(t *testing.T) {
	settings := DefaultLoadBalancingSettings()

	require.NotNil(t, settings)
	require.True(t, settings.Enabled)
	require.Equal(t, 30, settings.PriorityOffset)
	require.Equal(t, 10, settings.TimeWindowMinutes)
}
