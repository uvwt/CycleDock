package modelclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestResolveModelLimitsPriority(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)

	// 1. 用户手动覆盖优先于服务商与注册表
	userOverride := ModelLimits{
		ContextWindowTokens: 64000,
		MaxInputTokens:      56000,
		MaxOutputTokens:     8000,
		Source:              LimitsSourceUser,
	}
	providerMeta := &ModelLimits{
		ContextWindowTokens: 128000,
		MaxInputTokens:      120000,
		MaxOutputTokens:     16384,
		Source:              LimitsSourceProvider,
	}
	got := ResolveModelLimits(userOverride, providerMeta, "gpt-4o", "", now)
	if got.Source != LimitsSourceUser || got.ContextWindowTokens != 64000 || got.MaxInputTokens != 56000 || got.MaxOutputTokens != 8000 {
		t.Fatalf("expected user override to win, got %+v", got)
	}

	// 2. 服务商返回的元数据优先于注册表
	got = ResolveModelLimits(ModelLimits{}, providerMeta, "gpt-4o", "", now)
	if got.Source != LimitsSourceProvider || got.ContextWindowTokens != 128000 || got.MaxInputTokens != 120000 {
		t.Fatalf("expected provider limits to win over registry, got %+v", got)
	}

	// 3. 注册表对精确识别的官方模型生效（覆盖最新世代模型与命名空间前缀）
	got = ResolveModelLimits(ModelLimits{}, nil, "deepseek-chat", "", now)
	if got.Source != LimitsSourceRegistry || got.ContextWindowTokens != 163840 || got.MaxOutputTokens != 16384 {
		t.Fatalf("expected registry match for deepseek-chat, got %+v", got)
	}
	for _, tc := range []struct {
		modelID string
		wantCtx int
		wantIn  int
		wantOut int
	}{
		{modelID: "gpt-6.1-sol", wantCtx: 1050000, wantIn: 922000, wantOut: 128000},
		{modelID: "gpt-5.4", wantCtx: 1050000, wantIn: 922000, wantOut: 128000},
		{modelID: "claude-opus-5-5", wantCtx: 1000000, wantIn: 1000000, wantOut: 128000},
		{modelID: "anthropic/claude-sonnet-4.6", wantCtx: 1000000, wantIn: 1000000, wantOut: 128000},
		{modelID: "gemini-3.8-flash", wantCtx: 1048576, wantIn: 1048576, wantOut: 65536},
		{modelID: "deepseek-v4-pro", wantCtx: 1048576, wantIn: 1048576, wantOut: 393216},
		{modelID: "grok-4.7", wantCtx: 500000, wantIn: 500000, wantOut: 500000},
		{modelID: "qwen3.8-max", wantCtx: 1000000, wantIn: 1000000, wantOut: 131072},
		{modelID: "moonshotai/kimi-k3", wantCtx: 1048576, wantIn: 1048576, wantOut: 1048576},
		{modelID: "z-ai/glm-5.3", wantCtx: 1000000, wantIn: 1000000, wantOut: 131072},
		{modelID: "minimax/minimax-m3", wantCtx: 1000000, wantIn: 1000000, wantOut: 524288},
	} {
		res := ResolveModelLimits(ModelLimits{}, nil, tc.modelID, "", now)
		if res.Source != LimitsSourceRegistry ||
			res.ContextWindowTokens != tc.wantCtx ||
			res.MaxInputTokens != tc.wantIn ||
			res.MaxOutputTokens != tc.wantOut {
			t.Fatalf("model %q limits=%+v, want ctx=%d in=%d out=%d", tc.modelID, res, tc.wantCtx, tc.wantIn, tc.wantOut)
		}
	}

	// 4. 未识别的自定义代理别名不得伪造模型容量
	got = ResolveModelLimits(ModelLimits{}, nil, "gemini-3.8-flash-high", "", now)
	if got.Source != LimitsSourceUnknown || got.ContextWindowTokens != 0 || got.MaxInputTokens != 0 || got.MaxOutputTokens != 0 {
		t.Fatalf("expected unknown limits for custom alias without base_model, got %+v", got)
	}

	// 5. 自定义代理别名指定精确 base_model 时，走注册表容量
	got = ResolveModelLimits(ModelLimits{}, nil, "gemini-3.8-flash-high", "gemini-3.8-flash", now)
	if got.Source != LimitsSourceRegistry || got.ContextWindowTokens != 1048576 || got.MaxOutputTokens != 65536 {
		t.Fatalf("expected registry match via base_model gemini-3.8-flash, got %+v", got)
	}
}

func TestListModelsExtractsProviderLimitsAndCaches(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{
					"id": "custom-coder-xl",
					"name": "Custom Coder XL",
					"context_length": 262144,
					"max_input_tokens": 240000,
					"max_completion_tokens": 32768
				},
				{
					"id": "gpt-4o",
					"name": "GPT-4o"
				},
				{
					"id": "gemini-3.8-flash-high",
					"name": "Gemini Proxy Alias"
				}
			]
		}`))
	}))
	defer server.Close()

	client := New()
	models, err := client.ListModels(context.Background(), Config{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(models))
	}
	if models[0].Limits == nil || models[0].Limits.Source != LimitsSourceProvider || models[0].Limits.ContextWindowTokens != 262144 || models[0].Limits.MaxInputTokens != 240000 || models[0].Limits.MaxOutputTokens != 32768 {
		t.Fatalf("unexpected provider limits for custom-coder-xl: %+v", models[0].Limits)
	}
	if models[1].Limits == nil || models[1].Limits.Source != LimitsSourceRegistry || models[1].Limits.ContextWindowTokens != 128000 {
		t.Fatalf("expected registry fallback for gpt-4o, got %+v", models[1].Limits)
	}
	if models[2].Limits != nil {
		t.Fatalf("expected nil limits for unrecognized alias in catalog, got %+v", models[2].Limits)
	}

	// ProbeModelLimits 应直接命中短时缓存，不再重复请求上游 /models
	probed, err := client.ProbeModelLimits(context.Background(), Config{BaseURL: server.URL}, "custom-coder-xl")
	if err != nil {
		t.Fatalf("ProbeModelLimits failed: %v", err)
	}
	if probed == nil || probed.ContextWindowTokens != 262144 || probed.Source != LimitsSourceProvider {
		t.Fatalf("unexpected probed limits: %+v", probed)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 upstream /models request due to cache, got %d", hits.Load())
	}
}

func TestListModelsRejectsRedirectsAndPrivateIPs(t *testing.T) {
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer redirectServer.Close()

	client := New()
	_, err := client.ListModels(context.Background(), Config{BaseURL: redirectServer.URL})
	if !errors.Is(err, ErrRedirectDisallowed) {
		t.Fatalf("expected redirect to be rejected with ErrRedirectDisallowed, got %v", err)
	}

	_, err = client.ListModels(context.Background(), Config{BaseURL: "https://169.254.169.254/v1"})
	if err == nil {
		t.Fatal("expected link-local IP endpoint to be rejected")
	}
}
