package modelclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/uvwt/CycleDock/internal/agentconfig"
)

const (
	maxModelCatalogBytes int64 = 2 << 20
	maxCatalogTimeout          = 10 * time.Second
	modelCatalogCacheTTL       = 10 * time.Minute
)

var ErrRedirectDisallowed = errors.New("model catalog redirects are not allowed")

type ModelInfo struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Limits *ModelLimits `json:"limits,omitempty"`
}

type catalogCacheEntry struct {
	expiresAt      time.Time
	models         []ModelInfo
	providerLimits map[string]ModelLimits
}

type rawModelLimitFields struct {
	ContextWindow       int `json:"context_window"`
	ContextLength       int `json:"context_length"`
	MaxContextLength    int `json:"max_context_length"`
	MaxModelLen         int `json:"max_model_len"`
	MaxInputTokens      int `json:"max_input_tokens"`
	InputTokenLimit     int `json:"input_token_limit"`
	MaxPromptTokens     int `json:"max_prompt_tokens"`
	MaxOutputTokens     int `json:"max_output_tokens"`
	MaxCompletionTokens int `json:"max_completion_tokens"`
	OutputTokenLimit    int `json:"output_token_limit"`
}

type rawCatalogModelItem struct {
	Slug           string               `json:"slug"`
	ID             string               `json:"id"`
	Model          string               `json:"model"`
	DisplayName    string               `json:"display_name"`
	Name           string               `json:"name"`
	Visibility     string               `json:"visibility"`
	SupportedInAPI *bool                `json:"supported_in_api"`
	Limits         *rawModelLimitFields `json:"limits"`
	TopProvider    *rawModelLimitFields `json:"top_provider"`
	rawModelLimitFields
}

func extractProviderModelLimits(item rawCatalogModelItem, verifiedAt time.Time) *ModelLimits {
	pickFirstPositive := func(values ...int) int {
		for _, v := range values {
			if s := sanitizePositiveTokenLimit(v); s > 0 {
				return s
			}
		}
		return 0
	}

	var nestedLimits, topProvider rawModelLimitFields
	if item.Limits != nil {
		nestedLimits = *item.Limits
	}
	if item.TopProvider != nil {
		topProvider = *item.TopProvider
	}

	contextWindow := pickFirstPositive(
		item.ContextWindow,
		item.ContextLength,
		item.MaxContextLength,
		item.MaxModelLen,
		nestedLimits.ContextWindow,
		nestedLimits.ContextLength,
		nestedLimits.MaxContextLength,
		nestedLimits.MaxModelLen,
		topProvider.ContextWindow,
		topProvider.ContextLength,
		topProvider.MaxContextLength,
		topProvider.MaxModelLen,
	)
	maxInput := pickFirstPositive(
		item.MaxInputTokens,
		item.InputTokenLimit,
		item.MaxPromptTokens,
		nestedLimits.MaxInputTokens,
		nestedLimits.InputTokenLimit,
		nestedLimits.MaxPromptTokens,
		topProvider.MaxInputTokens,
		topProvider.InputTokenLimit,
		topProvider.MaxPromptTokens,
	)
	maxOutput := pickFirstPositive(
		item.MaxOutputTokens,
		item.MaxCompletionTokens,
		item.OutputTokenLimit,
		nestedLimits.MaxOutputTokens,
		nestedLimits.MaxCompletionTokens,
		nestedLimits.OutputTokenLimit,
		topProvider.MaxOutputTokens,
		topProvider.MaxCompletionTokens,
		topProvider.OutputTokenLimit,
	)

	if contextWindow == 0 && maxInput == 0 {
		return nil
	}
	ts := verifiedAt.UTC()
	out := clampInputOutputToContextWindow(ModelLimits{
		ContextWindowTokens: contextWindow,
		MaxInputTokens:      maxInput,
		MaxOutputTokens:     maxOutput,
		Source:              LimitsSourceProvider,
		VerifiedAt:          &ts,
	})
	return &out
}

func normalizeCatalogBaseURL(raw string) (string, bool, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return "", false, errors.New("model base URL is required")
	}
	if err := agentconfig.ValidateEndpoint(trimmed); err != nil {
		return "", false, err
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", false, err
	}
	host := strings.ToLower(parsed.Hostname())
	allowLoopback := host == "localhost" || strings.HasSuffix(host, ".localhost")
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		allowLoopback = true
	}
	return trimmed, allowLoopback, nil
}

func (c *Client) catalogHTTPClient(allowLoopback bool) *http.Client {
	base := c.httpClient
	if base == nil {
		base = &http.Client{}
	}
	cloned := *base
	cloned.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return ErrRedirectDisallowed
	}
	if cloned.Transport == nil {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		cloned.Transport = &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				for _, ipAddr := range ips {
					if agentconfig.IsDisallowedTargetIP(ipAddr.IP, allowLoopback) {
						return nil, fmt.Errorf("disallowed model endpoint IP %s", ipAddr.IP.String())
					}
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("no IP addresses resolved for %s", host)
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
			},
		}
	}
	return &cloned
}

func (c *Client) ListModels(ctx context.Context, config Config) ([]ModelInfo, error) {
	models, _, err := c.fetchCatalog(ctx, config)
	return models, err
}

// CachedProviderModelLimits 从短时缓存中读取服务商已返回的模型容量信息。
func (c *Client) CachedProviderModelLimits(baseURL, modelID string) (*ModelLimits, bool) {
	normalizedURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	normalizedModel := strings.TrimSpace(modelID)
	if normalizedURL == "" || normalizedModel == "" {
		return nil, false
	}
	now := time.Now()
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	entry, ok := c.catalogCache[normalizedURL]
	if !ok || now.After(entry.expiresAt) {
		return nil, false
	}
	limits, ok := entry.providerLimits[normalizedModel]
	if !ok {
		limits, ok = entry.providerLimits[strings.ToLower(normalizedModel)]
		if !ok {
			return nil, false
		}
	}
	copied := limits
	return &copied, true
}

// ProbeModelLimits 查询指定服务商返回的模型容量信息（优先命中短时缓存，未命中时探测 /models）。
func (c *Client) ProbeModelLimits(ctx context.Context, config Config, modelID string) (*ModelLimits, error) {
	if cached, ok := c.CachedProviderModelLimits(config.BaseURL, modelID); ok {
		return cached, nil
	}
	_, providerMap, err := c.fetchCatalog(ctx, config)
	if err != nil {
		return nil, err
	}
	normalizedModel := strings.TrimSpace(modelID)
	if limits, ok := providerMap[normalizedModel]; ok {
		copied := limits
		return &copied, nil
	}
	if limits, ok := providerMap[strings.ToLower(normalizedModel)]; ok {
		copied := limits
		return &copied, nil
	}
	return nil, nil
}

func (c *Client) fetchCatalog(ctx context.Context, config Config) ([]ModelInfo, map[string]ModelLimits, error) {
	baseURL, allowLoopback, err := normalizeCatalogBaseURL(config.BaseURL)
	if err != nil {
		return nil, nil, err
	}
	timeout := config.Timeout
	if timeout <= 0 || timeout > maxCatalogTimeout {
		timeout = maxCatalogTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodGet,
		baseURL+"/models",
		nil,
	)
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Accept", "application/json")
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	for name, value := range config.Headers {
		if strings.TrimSpace(name) != "" && strings.TrimSpace(value) != "" {
			request.Header.Set(name, value)
		}
	}
	response, err := c.catalogHTTPClient(allowLoopback).Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("list models request: %w", err)
	}
	defer response.Body.Close()

	body, err := readBounded(response.Body, response.ContentLength, maxModelCatalogBytes)
	if err != nil {
		return nil, nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, nil, &APIError{StatusCode: response.StatusCode, Body: summarizeBody(body)}
	}

	var decoded struct {
		Models []rawCatalogModelItem `json:"models"`
		Data   []rawCatalogModelItem `json:"data"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, nil, fmt.Errorf("%w: invalid models response", ErrMalformedResponse)
	}

	now := time.Now().UTC()
	c.EnsureOnlineRegistry(ctx)
	providerLimits := make(map[string]ModelLimits)
	models := make([]ModelInfo, 0, len(decoded.Models)+len(decoded.Data))

	appendItem := func(item rawCatalogModelItem, checkVisibility bool) {
		if checkVisibility {
			if item.Visibility != "" && item.Visibility != "list" {
				return
			}
			if item.SupportedInAPI != nil && !*item.SupportedInAPI {
				return
			}
		}
		id := strings.TrimSpace(item.Slug)
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if id == "" {
			id = strings.TrimSpace(item.Model)
		}
		if id == "" {
			return
		}
		name := strings.TrimSpace(item.DisplayName)
		if name == "" {
			name = strings.TrimSpace(item.Name)
		}
		if name == "" {
			name = id
		}
		extracted := extractProviderModelLimits(item, now)
		if extracted != nil {
			providerLimits[id] = *extracted
			providerLimits[strings.ToLower(id)] = *extracted
		}
		resolved := ResolveModelLimits(ModelLimits{}, extracted, id, "", now)
		var itemLimits *ModelLimits
		if resolved.HasKnownCapacity() {
			itemLimits = &resolved
		}
		models = append(models, ModelInfo{
			ID:     id,
			Name:   name,
			Limits: itemLimits,
		})
	}

	for _, item := range decoded.Models {
		appendItem(item, true)
	}
	for _, item := range decoded.Data {
		appendItem(item, false)
	}
	if len(models) == 0 {
		return nil, nil, ErrEmptyResponse
	}

	c.catalogMu.Lock()
	if c.catalogCache == nil {
		c.catalogCache = make(map[string]catalogCacheEntry)
	}
	c.catalogCache[baseURL] = catalogCacheEntry{
		expiresAt:      now.Add(modelCatalogCacheTTL),
		models:         models,
		providerLimits: providerLimits,
	}
	c.catalogMu.Unlock()

	return models, providerLimits, nil
}
