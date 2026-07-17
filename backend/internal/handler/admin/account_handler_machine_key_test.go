package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func machineKeyDetailResponse(t *testing.T, authMethod string, account *service.Account) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	svc.getAccountResult = account
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth_method", authMethod)
		c.Next()
	})
	router.GET("/api/v1/admin/accounts/:id", handler.GetByID)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/42", nil)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return envelope.Data
}

func TestAccountDetailMachineAdminExposesOnlyRouteAPIKey(t *testing.T) {
	for _, accountType := range []string{service.AccountTypeAPIKey, service.AccountTypeUpstream} {
		t.Run(accountType, func(t *testing.T) {
			account := &service.Account{
				ID: 42, Name: "route", Platform: service.PlatformAnthropic,
				Type: accountType, Status: service.StatusActive,
				Credentials: map[string]any{
					"base_url":     "https://downstream.example.com",
					"api_key":      "route-secret",
					"access_token": "must-stay-hidden",
				},
			}
			data := machineKeyDetailResponse(t, service.AuditAuthMethodAdminAPIKey, account)
			credentials, ok := data["credentials"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "https://downstream.example.com", credentials["base_url"])
			require.Equal(t, "route-secret", credentials["api_key"])
			require.NotContains(t, credentials, "access_token")
			require.Equal(t, "must-stay-hidden", account.Credentials["access_token"], "response mapping must not mutate stored credentials")
		})
	}
}

func TestAccountDetailJWTKeepsRouteAPIKeyRedacted(t *testing.T) {
	data := machineKeyDetailResponse(t, service.AuditAuthMethodJWT, &service.Account{
		ID: 42, Name: "route", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive,
		Credentials: map[string]any{
			"base_url": "https://downstream.example.com",
			"api_key":  "route-secret",
		},
	})
	credentials, ok := data["credentials"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, credentials, "api_key")
	require.NotContains(t, string(mustJSON(t, data)), "route-secret")
}

func TestAccountDetailMachineAdminKeepsOAuthCredentialsRedacted(t *testing.T) {
	data := machineKeyDetailResponse(t, service.AuditAuthMethodAdminAPIKey, &service.Account{
		ID: 42, Name: "oauth", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeOAuth, Status: service.StatusActive,
		Credentials: map[string]any{
			"api_key":      "must-stay-hidden",
			"access_token": "oauth-secret",
		},
	})
	credentials, ok := data["credentials"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, credentials, "api_key")
	require.NotContains(t, credentials, "access_token")
}

func TestAccountDetailMachineAdminRequiresAnthropicRoute(t *testing.T) {
	for _, account := range []*service.Account{
		{
			ID: 42, Name: "external", Platform: service.PlatformAnthropic,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive,
			Credentials: map[string]any{"api_key": "external-secret"},
		},
		{
			ID: 43, Name: "openai-route", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive,
			Credentials: map[string]any{
				"base_url": "https://downstream.example.com",
				"api_key":  "openai-secret",
			},
		},
	} {
		data := machineKeyDetailResponse(t, service.AuditAuthMethodAdminAPIKey, account)
		credentials, ok := data["credentials"].(map[string]any)
		require.True(t, ok)
		require.NotContains(t, credentials, "api_key")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return raw
}
