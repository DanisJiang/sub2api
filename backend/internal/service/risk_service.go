package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// RiskService 风控服务客户端
type RiskService interface {
	// Check 检查账号风险，需要传入当前请求的 inputTokens
	Check(ctx context.Context, accountID int64, inputTokens int) (*RiskCheckResponse, error)
	// Record 记录请求完成
	Record(ctx context.Context, accountID int64, inputTokens, outputTokens int) error
	// TestConnection 测试风控服务连接
	TestConnection(ctx context.Context) (*RiskServiceHealthResponse, error)
	// IsEnabled 检查风控服务是否启用
	IsEnabled(ctx context.Context) bool
}

// RiskCheckResponse 风控检查响应
type RiskCheckResponse struct {
	AccountID    string  `json:"account_id"`
	RiskScore    float64 `json:"risk_score"`
	Threshold    float64 `json:"threshold"`
	Status       string  `json:"status"` // safe, danger
	Message      string  `json:"message"`
	RequestCount int     `json:"request_count"`
	WindowHours  int     `json:"window_hours"`
}

// RiskServiceHealthResponse 风控服务健康检查响应
type RiskServiceHealthResponse struct {
	Status         string         `json:"status"`
	ModelLoaded    bool           `json:"model_loaded"`
	RedisConnected bool           `json:"redis_connected"`
	Config         map[string]any `json:"config"`
}

// RiskServiceURLProvider 提供风控服务 URL 的接口
type RiskServiceURLProvider interface {
	GetRiskServiceURL(ctx context.Context) string
}

type riskServiceImpl struct {
	httpClient  *http.Client
	urlProvider RiskServiceURLProvider
}

// NewRiskService 创建风控服务客户端
func NewRiskService(urlProvider RiskServiceURLProvider) RiskService {
	return &riskServiceImpl{
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		urlProvider: urlProvider,
	}
}

func (r *riskServiceImpl) getBaseURL(ctx context.Context) string {
	if r.urlProvider == nil {
		return ""
	}
	return r.urlProvider.GetRiskServiceURL(ctx)
}

func (r *riskServiceImpl) IsEnabled(ctx context.Context) bool {
	return r.getBaseURL(ctx) != ""
}

func (r *riskServiceImpl) Check(ctx context.Context, accountID int64, inputTokens int) (*RiskCheckResponse, error) {
	baseURL := r.getBaseURL(ctx)
	if baseURL == "" {
		return &RiskCheckResponse{
			Status: "safe",
		}, nil
	}

	reqBody := map[string]any{
		"account_id":   fmt.Sprintf("%d", accountID),
		"input_tokens": inputTokens,
	}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/check", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var result RiskCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	return &result, nil
}

func (r *riskServiceImpl) Record(ctx context.Context, accountID int64, inputTokens, outputTokens int) error {
	baseURL := r.getBaseURL(ctx)
	if baseURL == "" {
		return nil
	}

	reqBody := map[string]any{
		"account_id":    fmt.Sprintf("%d", accountID),
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
	}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/record", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	return nil
}

func (r *riskServiceImpl) TestConnection(ctx context.Context) (*RiskServiceHealthResponse, error) {
	baseURL := r.getBaseURL(ctx)
	if baseURL == "" {
		return nil, fmt.Errorf("risk service URL not configured")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/health", nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health check failed with status: %d", resp.StatusCode)
	}

	var result RiskServiceHealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	return &result, nil
}
