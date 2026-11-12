package modelclient

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	LimitsSourceUser     = "user"
	LimitsSourceProvider = "provider"
	LimitsSourceRegistry = "registry"
	LimitsSourceUnknown  = "unknown"

	maxReasonableTokenLimit = 10_000_000

	DefaultModelsDevAPIURL  = "https://models.dev/api.json"
	DefaultOpenRouterAPIURL = "https://openrouter.ai/api/v1/models"

	onlineRegistryCacheTTL      = 6 * time.Hour
	onlineRegistryRetryCooldown = 5 * time.Minute
	onlineRegistryFetchTimeout  = 4 * time.Second
	maxOnlineRegistryBytes      = 8 << 20
)

// ModelLimits 描述模型路由的上下文容量与输入/输出上限。
// 0 表示未知，不得自动解释为某个固定默认容量。
type ModelLimits struct {
	ContextWindowTokens int        `json:"context_window_tokens,omitempty"`
	MaxInputTokens      int        `json:"max_input_tokens,omitempty"`
	MaxOutputTokens     int        `json:"max_output_tokens,omitempty"`
	Source              string     `json:"source"`
	VerifiedAt          *time.Time `json:"verified_at,omitempty"`
}

func (m ModelLimits) HasKnownCapacity() bool {
	return m.ContextWindowTokens > 0 || m.MaxInputTokens > 0 || m.MaxOutputTokens > 0
}

func ValidLimitsSource(source string) bool {
	switch strings.TrimSpace(source) {
	case LimitsSourceUser, LimitsSourceProvider, LimitsSourceRegistry, LimitsSourceUnknown:
		return true
	default:
		return false
	}
}

// ValidateUserModelLimits 校验用户配置或持久化的模型容量参数。
func ValidateUserModelLimits(limits ModelLimits) (ModelLimits, error) {
	if limits.ContextWindowTokens < 0 || limits.MaxInputTokens < 0 || limits.MaxOutputTokens < 0 {
		return ModelLimits{}, fmt.Errorf("model token limits must be positive integers")
	}
	if limits.ContextWindowTokens > maxReasonableTokenLimit ||
		limits.MaxInputTokens > maxReasonableTokenLimit ||
		limits.MaxOutputTokens > maxReasonableTokenLimit {
		return ModelLimits{}, fmt.Errorf("model token limits exceed maximum supported bound (%d)", maxReasonableTokenLimit)
	}
	if limits.ContextWindowTokens > 0 {
		if limits.MaxInputTokens > limits.ContextWindowTokens {
			return ModelLimits{}, fmt.Errorf("max_input_tokens cannot exceed context_window_tokens")
		}
		if limits.MaxOutputTokens > limits.ContextWindowTokens {
			return ModelLimits{}, fmt.Errorf("max_output_tokens cannot exceed context_window_tokens")
		}
	}
	source := strings.TrimSpace(limits.Source)
	if source == "" {
		if limits.HasKnownCapacity() {
			source = LimitsSourceUser
		} else {
			source = LimitsSourceUnknown
		}
	}
	if !ValidLimitsSource(source) {
		return ModelLimits{}, fmt.Errorf("unsupported model limits source %q", source)
	}
	if source == LimitsSourceUser && !limits.HasKnownCapacity() {
		source = LimitsSourceUnknown
	}
	limits.Source = source
	return limits, nil
}

type registryEntry struct {
	contextWindow int
	maxInput      int
	maxOutput     int
}

var (
	onlineRegistryMu        sync.RWMutex
	onlineRegistryFlight    singleflight.Group
	onlineRegistryEntries   map[string]registryEntry
	onlineRegistryExpiresAt time.Time
	onlineRegistryNextRetry time.Time
	modelsDevAPIURL         = DefaultModelsDevAPIURL
	openRouterAPIURL        = DefaultOpenRouterAPIURL
	onlineRegistryTestMode  bool
)

// SetOnlineRegistryURLsForTest 供测试注入本地 httptest 源地址，返回恢复函数。
func SetOnlineRegistryURLsForTest(modelsDevURL, openRouterURL string) func() {
	onlineRegistryMu.Lock()
	prevModelsDev := modelsDevAPIURL
	prevOpenRouter := openRouterAPIURL
	prevEntries := onlineRegistryEntries
	prevExpires := onlineRegistryExpiresAt
	prevRetry := onlineRegistryNextRetry
	prevTestMode := onlineRegistryTestMode
	modelsDevAPIURL = modelsDevURL
	openRouterAPIURL = openRouterURL
	onlineRegistryEntries = nil
	onlineRegistryExpiresAt = time.Time{}
	onlineRegistryNextRetry = time.Time{}
	onlineRegistryTestMode = true
	onlineRegistryMu.Unlock()

	return func() {
		onlineRegistryMu.Lock()
		modelsDevAPIURL = prevModelsDev
		openRouterAPIURL = prevOpenRouter
		onlineRegistryEntries = prevEntries
		onlineRegistryExpiresAt = prevExpires
		onlineRegistryNextRetry = prevRetry
		onlineRegistryTestMode = prevTestMode
		onlineRegistryMu.Unlock()
	}
}

// WarmOnlineRegistryAsync 在后台异步预热在线模型目录，不阻塞只读 HTTP 请求；测试环境自动跳过。
func (c *Client) WarmOnlineRegistryAsync() {
	onlineRegistryMu.RLock()
	testMode := onlineRegistryTestMode
	onlineRegistryMu.RUnlock()
	if flag.Lookup("test.v") != nil && !testMode {
		return
	}
	go c.EnsureOnlineRegistry(context.Background())
}

// EnsureOnlineRegistry 按需同步 models.dev（主）与 OpenRouter（辅）的实时模型容量目录。
// 结果缓存 6 小时；若外网不可达则冷却 5 分钟并自动回退内置 curatedModelRegistry，永不阻塞业务流程。
func (c *Client) EnsureOnlineRegistry(ctx context.Context) {
	now := time.Now().UTC()
	onlineRegistryMu.RLock()
	fresh := len(onlineRegistryEntries) > 0 && now.Before(onlineRegistryExpiresAt)
	coolingDown := !onlineRegistryNextRetry.IsZero() && now.Before(onlineRegistryNextRetry)
	mURL := modelsDevAPIURL
	oURL := openRouterAPIURL
	testMode := onlineRegistryTestMode
	onlineRegistryMu.RUnlock()

	// 单元测试环境下若未显式注入测试源 URL，不主动发起外网请求，保证测试离线确定性。
	if flag.Lookup("test.v") != nil && !testMode {
		return
	}
	if fresh || coolingDown || (mURL == "" && oURL == "") {
		return
	}

	// 合并并发同步请求，避免多个请求同时冷启动穿透外部注册表。
	_, _, _ = onlineRegistryFlight.Do(mURL+"|"+oURL, func() (any, error) {
		nowCheck := time.Now().UTC()
		onlineRegistryMu.RLock()
		stillFresh := len(onlineRegistryEntries) > 0 && nowCheck.Before(onlineRegistryExpiresAt)
		stillCooling := !onlineRegistryNextRetry.IsZero() && nowCheck.Before(onlineRegistryNextRetry)
		onlineRegistryMu.RUnlock()
		if stillFresh || stillCooling {
			return nil, nil
		}

		httpClient := &http.Client{Timeout: onlineRegistryFetchTimeout}
		if c != nil && c.httpClient != nil {
			copied := *c.httpClient
			copied.Timeout = onlineRegistryFetchTimeout
			httpClient = &copied
		}
		syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), onlineRegistryFetchTimeout*2)
		defer cancel()
		return nil, SyncOnlineModelRegistryFromURLs(syncCtx, httpClient, mURL, oURL)
	})
}

// SyncOnlineModelRegistryFromURLs 从 models.dev（主源，区分 context/input/output）和 OpenRouter（辅源，补充长尾模型）并行拉取并合并实时模型目录。
func SyncOnlineModelRegistryFromURLs(
	ctx context.Context,
	httpClient *http.Client,
	modelsDevURL string,
	openRouterURL string,
) error {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: onlineRegistryFetchTimeout}
	}
	now := time.Now().UTC()

	var (
		wg             sync.WaitGroup
		primaryEntries map[string]registryEntry
		primaryErr     error
		backupEntries  map[string]registryEntry
		backupErr      error
	)

	if strings.TrimSpace(modelsDevURL) != "" {
		primaryEntries = make(map[string]registryEntry, 512)
		wg.Add(1)
		go func() {
			defer wg.Done()
			primaryErr = fetchModelsDevCatalog(ctx, httpClient, modelsDevURL, primaryEntries)
		}()
	}

	if strings.TrimSpace(openRouterURL) != "" {
		backupEntries = make(map[string]registryEntry, 512)
		wg.Add(1)
		go func() {
			defer wg.Done()
			backupErr = fetchOpenRouterCatalog(ctx, httpClient, openRouterURL, backupEntries)
		}()
	}

	wg.Wait()

	merged := make(map[string]registryEntry, len(primaryEntries)+len(backupEntries))
	for k, v := range primaryEntries {
		merged[k] = v
	}
	// 辅源仅补充主源尚未收录的模型 ID，严格保持原有优先级。
	for k, v := range backupEntries {
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}

	firstErr := primaryErr
	if firstErr == nil {
		firstErr = backupErr
	}

	onlineRegistryMu.Lock()
	defer onlineRegistryMu.Unlock()
	if len(merged) > 0 {
		onlineRegistryEntries = merged
		onlineRegistryExpiresAt = now.Add(onlineRegistryCacheTTL)
		onlineRegistryNextRetry = time.Time{}
		return nil
	}
	onlineRegistryNextRetry = now.Add(onlineRegistryRetryCooldown)
	return firstErr
}

type modelsDevProviderPayload struct {
	Models map[string]struct {
		ID    string `json:"id"`
		Limit struct {
			Context int `json:"context"`
			Input   int `json:"input"`
			Output  int `json:"output"`
		} `json:"limit"`
	} `json:"models"`
}

func fetchModelsDevCatalog(
	ctx context.Context,
	httpClient *http.Client,
	endpoint string,
	dest map[string]registryEntry,
) error {
	reqCtx, cancel := context.WithTimeout(ctx, onlineRegistryFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CycleDock/1.0)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("models.dev returned HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxOnlineRegistryBytes))
	if err != nil {
		return err
	}
	var providers map[string]modelsDevProviderPayload
	if err := json.Unmarshal(raw, &providers); err != nil {
		return err
	}

	for providerKey, prov := range providers {
		pKey := strings.ToLower(strings.TrimSpace(providerKey))
		for modelKey, m := range prov.Models {
			ctxTokens := sanitizePositiveTokenLimit(m.Limit.Context)
			inTokens := sanitizePositiveTokenLimit(m.Limit.Input)
			outTokens := sanitizePositiveTokenLimit(m.Limit.Output)
			if ctxTokens == 0 && inTokens == 0 {
				continue
			}
			if inTokens == 0 {
				inTokens = ctxTokens
			}
			if ctxTokens > 0 && inTokens > ctxTokens {
				inTokens = ctxTokens
			}
			if ctxTokens > 0 && outTokens > ctxTokens {
				outTokens = ctxTokens
			}
			entry := registryEntry{
				contextWindow: ctxTokens,
				maxInput:      inTokens,
				maxOutput:     outTokens,
			}
			for _, candidateID := range []string{modelKey, m.ID} {
				norm := strings.ToLower(strings.TrimSpace(candidateID))
				if norm == "" {
					continue
				}
				// 优先保留官方主厂商（openai / anthropic / google / xai / deepseek / alibaba / moonshotai / zhipuai / minimax）的精确定义。
				if _, exists := dest[norm]; !exists || isPrimaryVendorKey(pKey) {
					dest[norm] = entry
				}
				if pKey != "" && !strings.Contains(norm, "/") {
					dest[pKey+"/"+norm] = entry
				}
			}
		}
	}
	return nil
}

type openRouterCatalogPayload struct {
	Data []struct {
		ID            string `json:"id"`
		ContextLength int    `json:"context_length"`
		TopProvider   *struct {
			ContextLength       int `json:"context_length"`
			MaxCompletionTokens int `json:"max_completion_tokens"`
		} `json:"top_provider"`
	} `json:"data"`
}

func fetchOpenRouterCatalog(
	ctx context.Context,
	httpClient *http.Client,
	endpoint string,
	dest map[string]registryEntry,
) error {
	reqCtx, cancel := context.WithTimeout(ctx, onlineRegistryFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CycleDock/1.0)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("openrouter returned HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxOnlineRegistryBytes))
	if err != nil {
		return err
	}
	var payload openRouterCatalogPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}

	for _, item := range payload.Data {
		norm := strings.ToLower(strings.TrimSpace(item.ID))
		if norm == "" {
			continue
		}
		if colon := strings.IndexByte(norm, ':'); colon > 0 {
			norm = norm[:colon]
		}
		ctxTokens := sanitizePositiveTokenLimit(item.ContextLength)
		outTokens := 0
		if item.TopProvider != nil {
			if ctxTokens == 0 {
				ctxTokens = sanitizePositiveTokenLimit(item.TopProvider.ContextLength)
			}
			outTokens = sanitizePositiveTokenLimit(item.TopProvider.MaxCompletionTokens)
		}
		if ctxTokens == 0 {
			continue
		}
		if outTokens > ctxTokens {
			outTokens = ctxTokens
		}
		entry := registryEntry{
			contextWindow: ctxTokens,
			maxInput:      ctxTokens,
			maxOutput:     outTokens,
		}
		if _, exists := dest[norm]; !exists {
			dest[norm] = entry
		}
		if slash := strings.IndexByte(norm, '/'); slash > 0 && slash < len(norm)-1 {
			bare := norm[slash+1:]
			if _, exists := dest[bare]; !exists {
				dest[bare] = entry
			}
		}
	}
	return nil
}

func isPrimaryVendorKey(providerKey string) bool {
	switch providerKey {
	case "openai", "anthropic", "google", "xai", "x-ai", "deepseek", "alibaba", "qwen", "moonshotai", "zhipuai", "minimax":
		return true
	default:
		return false
	}
}

// curatedModelRegistry 维护主流官方模型的精确容量元数据（作为离线兜底与基准测试字典）。
var curatedModelRegistry = map[string]registryEntry{
	// OpenAI GPT-6 / GPT-5.6 / GPT-5.5 / GPT-5.4 / GPT-5 / Codex / o-series
	"gpt-6.1-terra":            {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6.1-terra-pro":        {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6.1-sol":              {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6.1-sol-pro":          {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-astra":              {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-astra-pro":          {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-luna":               {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-luna-pro":           {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-sol":                {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-sol-pro":            {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6":                  {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-luna":             {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-luna-pro":         {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-sol":              {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-sol-pro":          {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-terra":            {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-terra-pro":        {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.5":                  {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.5-pro":              {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.4":                  {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.4-pro":              {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.4-mini":             {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.4-nano":             {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.3-codex":            {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.3-codex-spark":      {contextWindow: 128000, maxInput: 100000, maxOutput: 32000},
	"gpt-5.2":                  {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.2-pro":              {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.2-codex":            {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.2-chat":             {contextWindow: 128000, maxInput: 128000, maxOutput: 32000},
	"gpt-5.1":                  {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.1-codex":            {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.1-codex-max":        {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.1-codex-mini":       {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5":                    {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5-pro":                {contextWindow: 400000, maxInput: 272000, maxOutput: 272000},
	"gpt-5-mini":               {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5-nano":               {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5-codex":              {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-daybreak-blue-latest": {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-daybreak-red-latest":  {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-chat-latest":          {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-oss-120b":             {contextWindow: 131072, maxInput: 131072, maxOutput: 65536},
	"gpt-oss-20b":              {contextWindow: 131072, maxInput: 131072, maxOutput: 65536},
	"gpt-4.1":                  {contextWindow: 1047576, maxInput: 1047576, maxOutput: 32768},
	"gpt-4.1-mini":             {contextWindow: 1047576, maxInput: 1047576, maxOutput: 32768},
	"gpt-4.1-nano":             {contextWindow: 1047576, maxInput: 1047576, maxOutput: 32768},
	"gpt-4o":                   {contextWindow: 128000, maxInput: 128000, maxOutput: 16384},
	"gpt-4o-2024-11-20":        {contextWindow: 128000, maxInput: 128000, maxOutput: 16384},
	"gpt-4o-2024-08-06":        {contextWindow: 128000, maxInput: 128000, maxOutput: 16384},
	"gpt-4o-mini":              {contextWindow: 128000, maxInput: 128000, maxOutput: 16384},
	"gpt-4o-mini-2024-07-18":   {contextWindow: 128000, maxInput: 128000, maxOutput: 16384},
	"o4-mini":                  {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o3":                       {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o3-pro":                   {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o3-mini":                  {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o1":                       {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o1-pro":                   {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o1-mini":                  {contextWindow: 128000, maxInput: 128000, maxOutput: 65536},

	// Anthropic Claude 5.5 / 5 / 4.8 / 4.7 / 4.6 / 4.5
	"claude-opus-5-5":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-5.5":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-5":              {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-sonnet-5-5":          {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-sonnet-5.5":          {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-sonnet-5":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-haiku-5-5":           {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-haiku-5.5":           {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-fable-5-1":           {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-fable-5.1":           {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-fable-5":             {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4-8":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4.8":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4-7":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4.7":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4-6":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4.6":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-sonnet-4-6":          {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-sonnet-4.6":          {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4-5":            {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-opus-4.5":            {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-opus-4-5-20251101":   {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-sonnet-4-5":          {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-sonnet-4.5":          {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-sonnet-4-5-20250929": {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-haiku-4-5":           {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-haiku-4.5":           {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-haiku-4-5-20251001":  {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-opus-4-1":            {contextWindow: 200000, maxInput: 200000, maxOutput: 32000},
	"claude-opus-4.1":            {contextWindow: 200000, maxInput: 200000, maxOutput: 32000},
	"claude-opus-4-0":            {contextWindow: 200000, maxInput: 200000, maxOutput: 32000},
	"claude-sonnet-4-0":          {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-3-7-sonnet-latest":   {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-3-7-sonnet-20250219": {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-3-5-sonnet-latest":   {contextWindow: 200000, maxInput: 200000, maxOutput: 8192},
	"claude-3-5-sonnet-20241022": {contextWindow: 200000, maxInput: 200000, maxOutput: 8192},
	"claude-3-5-haiku-latest":    {contextWindow: 200000, maxInput: 200000, maxOutput: 8192},
	"claude-3-5-haiku-20241022":  {contextWindow: 200000, maxInput: 200000, maxOutput: 8192},

	// Google Gemini 4 / 3.8 / 3.7 / 3.6 / 3.5 / 3.1 / 3 / 2.5 & Gemma 4
	"gemini-4-argon":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 32768},
	"gemini-3.8-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.8-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.8-flash-lite":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.7-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.7-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.6-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.6-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.5-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.5-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.5-flash-lite":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-pro-preview":             {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-pro-preview-customtools": {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-flash-lite":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-flash-lite-preview":      {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3-pro":                       {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3-pro-preview":               {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3-flash":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3-flash-preview":             {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-pro-latest":                  {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-flash-latest":                {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-flash-lite-latest":           {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-2.5-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-2.5-pro-preview":             {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-2.5-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-2.5-flash-lite":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-2.0-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 8192},
	"gemini-2.0-flash-lite":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 8192},
	"gemini-1.5-pro":                     {contextWindow: 2097152, maxInput: 2097152, maxOutput: 8192},
	"gemini-1.5-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 8192},
	"gemma-4-31b-it":                     {contextWindow: 262144, maxInput: 262144, maxOutput: 32768},
	"gemma-4-26b-a4b-it":                 {contextWindow: 262144, maxInput: 262144, maxOutput: 32768},

	// DeepSeek V4.1 / V4 / V3.2 / V3.1 / R1
	"deepseek-v4.1-flash":          {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v4.1-flash-1017":     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v4-pro":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v4-pro-0813":         {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v4-flash":            {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v4-flash-0731":       {contextWindow: 1000000, maxInput: 1000000, maxOutput: 384000},
	"deepseek-v4-flash-vision-exp": {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-flash":               {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v3.2":                {contextWindow: 163840, maxInput: 163840, maxOutput: 147456},
	"deepseek-v3.2-exp":            {contextWindow: 163840, maxInput: 163840, maxOutput: 147456},
	"deepseek-v3.1":                {contextWindow: 163840, maxInput: 163840, maxOutput: 147456},
	"deepseek-v3.1-terminus":       {contextWindow: 163840, maxInput: 163840, maxOutput: 147456},
	"deepseek-chat-v3.1":           {contextWindow: 163840, maxInput: 163840, maxOutput: 147456},
	"deepseek-chat-v3-0324":        {contextWindow: 163840, maxInput: 163840, maxOutput: 115200},
	"deepseek-r1-0528":             {contextWindow: 163840, maxInput: 163840, maxOutput: 32768},
	"deepseek-chat":                {contextWindow: 163840, maxInput: 163840, maxOutput: 16384},
	"deepseek-reasoner":            {contextWindow: 163840, maxInput: 163840, maxOutput: 64000},
	"deepseek-v3":                  {contextWindow: 128000, maxInput: 128000, maxOutput: 8192},
	"deepseek-r1":                  {contextWindow: 128000, maxInput: 128000, maxOutput: 64000},

	// xAI Grok 4.7 / 4.6 / 4.5 / 4.3 / 4.20 / 4.1 / 4 / 3
	"grok-4.7":                     {contextWindow: 500000, maxInput: 500000, maxOutput: 500000},
	"grok-4.6":                     {contextWindow: 500000, maxInput: 500000, maxOutput: 500000},
	"grok-4.5":                     {contextWindow: 500000, maxInput: 500000, maxOutput: 500000},
	"grok-4.3":                     {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.20":                    {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.20-0309-reasoning":     {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.20-0309-non-reasoning": {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.20-multi-agent":        {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.20-multi-agent-0309":   {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.1":                     {contextWindow: 2000000, maxInput: 2000000, maxOutput: 64000},
	"grok-4.1-fast":                {contextWindow: 2000000, maxInput: 2000000, maxOutput: 64000},
	"grok-4-fast":                  {contextWindow: 2000000, maxInput: 2000000, maxOutput: 64000},
	"grok-4":                       {contextWindow: 256000, maxInput: 256000, maxOutput: 64000},
	"grok-build-0.1":               {contextWindow: 256000, maxInput: 256000, maxOutput: 256000},
	"grok-code-fast-1":             {contextWindow: 256000, maxInput: 256000, maxOutput: 64000},
	"grok-3":                       {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},
	"grok-3-mini":                  {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},
	"grok-3-fast":                  {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},
	"grok-3-mini-fast":             {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},
	"grok-2":                       {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},
	"grok-2-latest":                {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},

	// Alibaba Qwen 3.8 / 3.7 / 3.6 / 3.5 / 3
	"qwen3.8-max":                    {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-max-0902":               {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-max-prime":              {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-plus":                   {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-flash":                  {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-omni-flash":             {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-2.4t-a95b":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 131072},
	"qwen3.8-27b":                    {contextWindow: 1024000, maxInput: 1024000, maxOutput: 235929},
	"qwen3.7-max":                    {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.7-plus":                   {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.7-flash":                  {contextWindow: 1000000, maxInput: 991808, maxOutput: 131072},
	"qwen3.6-max-preview":            {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.6-plus":                   {contextWindow: 1000000, maxInput: 1000000, maxOutput: 65536},
	"qwen3.6-flash":                  {contextWindow: 1000000, maxInput: 1000000, maxOutput: 65536},
	"qwen3.6-35b-a3b":                {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.6-27b":                    {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.5-plus":                   {contextWindow: 1000000, maxInput: 1000000, maxOutput: 65536},
	"qwen3.5-flash":                  {contextWindow: 1000000, maxInput: 1000000, maxOutput: 65536},
	"qwen3.5-397b-a17b":              {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.5-122b-a10b":              {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.5-35b-a3b":                {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.5-27b":                    {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3-max":                      {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3-coder-plus":               {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"qwen3-coder-flash":              {contextWindow: 1000000, maxInput: 1000000, maxOutput: 65536},
	"qwen3-coder":                    {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3-coder-next":               {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3-coder-480b-a35b-instruct": {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3-coder-30b-a3b-instruct":   {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen-plus":                      {contextWindow: 1000000, maxInput: 1000000, maxOutput: 32768},
	"qwen-flash":                     {contextWindow: 1000000, maxInput: 1000000, maxOutput: 32768},
	"qwen-turbo":                     {contextWindow: 1000000, maxInput: 1000000, maxOutput: 16384},
	"qwen-max":                       {contextWindow: 131072, maxInput: 129024, maxOutput: 8192},
	"qwen-coder-plus":                {contextWindow: 131072, maxInput: 129024, maxOutput: 8192},
	"qwen2.5-coder-32b-instruct":     {contextWindow: 131072, maxInput: 131072, maxOutput: 8192},

	// Moonshot Kimi K3.1 / K3 / K2.7 / K2.6 / K2.5 / K2
	"kimi-k3.1-pro":            {contextWindow: 1048576, maxInput: 1048576, maxOutput: 1048576},
	"kimi-k3.1-pro-highspeed":  {contextWindow: 1048576, maxInput: 1048576, maxOutput: 1048576},
	"kimi-k3":                  {contextWindow: 1048576, maxInput: 1048576, maxOutput: 1048576},
	"kimi-k2.7-code":           {contextWindow: 262144, maxInput: 262144, maxOutput: 262144},
	"kimi-k2.7-code-highspeed": {contextWindow: 262144, maxInput: 262144, maxOutput: 262144},
	"kimi-k2.6":                {contextWindow: 262144, maxInput: 262144, maxOutput: 262144},
	"kimi-k2.5":                {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"kimi-k2-thinking":         {contextWindow: 262144, maxInput: 262144, maxOutput: 98304},
	"kimi-k2-0905":             {contextWindow: 262144, maxInput: 262144, maxOutput: 98304},
	"kimi-k2":                  {contextWindow: 131072, maxInput: 131072, maxOutput: 98304},

	// Zhipu GLM 5.3 / 5.2 / 5.1 / 5 / 4.7 / 4.6 / 4.5
	"glm-5.3":        {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"glm-5.3-prime":  {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"glm-5.3-flash":  {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"glm-5.3-flashx": {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"glm-5.2":        {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"glm-5.1":        {contextWindow: 200000, maxInput: 200000, maxOutput: 131072},
	"glm-5":          {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"glm-5-turbo":    {contextWindow: 202752, maxInput: 202752, maxOutput: 131072},
	"glm-5v-turbo":   {contextWindow: 200000, maxInput: 200000, maxOutput: 131072},
	"glm-4.7":        {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"glm-4.7-flash":  {contextWindow: 200000, maxInput: 200000, maxOutput: 131072},
	"glm-4.7-flashx": {contextWindow: 200000, maxInput: 200000, maxOutput: 131072},
	"glm-4.6":        {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"glm-4.6v":       {contextWindow: 128000, maxInput: 128000, maxOutput: 32768},
	"glm-4.6v-flash": {contextWindow: 128000, maxInput: 128000, maxOutput: 32768},
	"glm-4.5":        {contextWindow: 131072, maxInput: 131072, maxOutput: 98304},
	"glm-4.5-air":    {contextWindow: 131072, maxInput: 131072, maxOutput: 98304},
	"glm-4.5-flash":  {contextWindow: 131072, maxInput: 131072, maxOutput: 98304},

	// MiniMax M3.1 / M3 / M2.7 / M2.5 / M2.1 / M2 / M1
	"minimax-m3.1-flash":     {contextWindow: 1000000, maxInput: 1000000, maxOutput: 524288},
	"minimax-m3":             {contextWindow: 1000000, maxInput: 1000000, maxOutput: 524288},
	"minimax-m2.7":           {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m2.7-highspeed": {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m2.5":           {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m2.5-highspeed": {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m2.1":           {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m2":             {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m1":             {contextWindow: 1000000, maxInput: 1000000, maxOutput: 40000},
}

// knownProviderNamespaces 限定仅剥离已知官方/聚合命名空间前缀，不对任意未知路径做模糊拆解。
var knownProviderNamespaces = map[string]struct{}{
	"openai":     {},
	"anthropic":  {},
	"google":     {},
	"deepseek":   {},
	"x-ai":       {},
	"xai":        {},
	"qwen":       {},
	"alibaba":    {},
	"moonshotai": {},
	"moonshot":   {},
	"zhipuai":    {},
	"z-ai":       {},
	"zai":        {},
	"minimax":    {},
	"models":     {},
}

var digitDotDigitPattern = regexp.MustCompile(`(\d)\.(\d)`)

func lookupSingleKey(key string) (registryEntry, bool) {
	onlineRegistryMu.RLock()
	if len(onlineRegistryEntries) > 0 {
		if entry, ok := onlineRegistryEntries[key]; ok {
			onlineRegistryMu.RUnlock()
			return entry, true
		}
	}
	onlineRegistryMu.RUnlock()

	if entry, ok := curatedModelRegistry[key]; ok {
		return entry, true
	}
	return registryEntry{}, false
}

func lookupExactRegistryID(raw string) (registryEntry, string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return registryEntry{}, "", false
	}
	if colon := strings.IndexByte(normalized, ':'); colon > 0 {
		tag := normalized[colon+1:]
		switch tag {
		case "free", "batch", "extended", "online", "thinking":
			normalized = normalized[:colon]
		}
	}

	candidates := []string{normalized}
	if slash := strings.IndexByte(normalized, '/'); slash > 0 && slash < len(normalized)-1 {
		prefix := normalized[:slash]
		rest := normalized[slash+1:]
		if _, knownPrefix := knownProviderNamespaces[prefix]; knownPrefix {
			candidates = append(candidates, rest)
		}
	}

	for _, c := range candidates {
		if entry, ok := lookupSingleKey(c); ok {
			return entry, c, true
		}
		// 兼容点号与连字符（如 claude-sonnet-4.6 <-> claude-sonnet-4-6）
		if hyphenated := digitDotDigitPattern.ReplaceAllString(c, "$1-$2"); hyphenated != c {
			if entry, ok := lookupSingleKey(hyphenated); ok {
				return entry, hyphenated, true
			}
		}
		// 兼容官方目录中带有 -preview / -latest 后缀的标准模型（如 gemini-3-flash <-> gemini-3-flash-preview）
		for _, suffix := range []string{"-preview", "-latest"} {
			if strings.HasSuffix(c, suffix) {
				stem := strings.TrimSuffix(c, suffix)
				if entry, ok := lookupSingleKey(stem); ok {
					return entry, stem, true
				}
			} else {
				withSuffix := c + suffix
				if entry, ok := lookupSingleKey(withSuffix); ok {
					return entry, withSuffix, true
				}
			}
		}
	}
	return registryEntry{}, "", false
}

// LookupRegistryModelLimits 在实时在线目录（models.dev + OpenRouter）与内置注册表中精确查找模型或底层模型容量。
func LookupRegistryModelLimits(modelID, baseModelID string) (ModelLimits, bool) {
	limits, _, ok := LookupRegistryModelLimitsWithCanonical(modelID, baseModelID)
	return limits, ok
}

// LookupRegistryModelLimitsWithCanonical 查找模型容量并返回命中的标准底层模型 ID。
func LookupRegistryModelLimitsWithCanonical(modelID, baseModelID string) (ModelLimits, string, bool) {
	if entry, canonical, ok := lookupExactRegistryID(modelID); ok {
		return ModelLimits{
			ContextWindowTokens: entry.contextWindow,
			MaxInputTokens:      entry.maxInput,
			MaxOutputTokens:     entry.maxOutput,
			Source:              LimitsSourceRegistry,
		}, canonical, true
	}
	if entry, canonical, ok := lookupExactRegistryID(baseModelID); ok {
		return ModelLimits{
			ContextWindowTokens: entry.contextWindow,
			MaxInputTokens:      entry.maxInput,
			MaxOutputTokens:     entry.maxOutput,
			Source:              LimitsSourceRegistry,
		}, canonical, true
	}
	return ModelLimits{Source: LimitsSourceUnknown}, "", false
}

// CandidateRegistryModelIDs 提取与 rawModelID 相关的候选标准模型 ID 列表，供 LLM 进行受控映射。
func CandidateRegistryModelIDs(rawModelID string, maxCount int) []string {
	if maxCount <= 0 {
		maxCount = 120
	}
	seen := make(map[string]struct{}, 256)
	onlineRegistryMu.RLock()
	for k := range onlineRegistryEntries {
		if !strings.Contains(k, "/") {
			seen[k] = struct{}{}
		}
	}
	onlineRegistryMu.RUnlock()
	for k := range curatedModelRegistry {
		if !strings.Contains(k, "/") {
			seen[k] = struct{}{}
		}
	}

	rawLower := strings.ToLower(strings.TrimSpace(rawModelID))
	tokens := splitModelTokens(rawLower)

	type scoredID struct {
		id    string
		score int
	}
	scored := make([]scoredID, 0, len(seen))
	for id := range seen {
		score := 0
		for _, tok := range tokens {
			if strings.Contains(id, tok) {
				score += 10
				if strings.HasPrefix(id, tok) {
					score += 5
				}
			}
		}
		scored = append(scored, scoredID{id: id, score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].id < scored[j].id
	})

	if len(scored) > maxCount {
		scored = scored[:maxCount]
	}
	out := make([]string, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.id)
	}
	return out
}

func splitModelTokens(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_' || r == '/' || r == ':' || r == ' '
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) >= 2 {
			out = append(out, p)
		}
	}
	return out
}

var identifyBaseModelOutputSchema = &OutputSchema{
	Name: "identify_base_model",
	Schema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"base_model"},
		"properties": map[string]any{
			"base_model": map[string]any{
				"type":        "string",
				"description": "Exact canonical model ID from the provided candidate list, or empty string if unknown.",
			},
		},
	},
	Strict: true,
}

// IdentifyBaseModelWithLLM 使用模型自身从实时模型目录候选列表中识别自定义中转别名对应的标准底层模型 ID。
// 仅当模型返回的 base_model 在注册表中真实存在时才采纳其容量，杜绝模型幻觉编造参数。
func (c *Client) IdentifyBaseModelWithLLM(
	ctx context.Context,
	config Config,
	rawModelID string,
) (string, ModelLimits, bool) {
	rawModelID = strings.TrimSpace(rawModelID)
	if rawModelID == "" || c == nil {
		return "", ModelLimits{Source: LimitsSourceUnknown}, false
	}
	c.EnsureOnlineRegistry(ctx)

	if limits, canonical, ok := LookupRegistryModelLimitsWithCanonical(rawModelID, ""); ok {
		return canonical, limits, true
	}

	candidates := CandidateRegistryModelIDs(rawModelID, 120)
	if len(candidates) == 0 {
		return "", ModelLimits{Source: LimitsSourceUnknown}, false
	}

	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	cfg := config
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = rawModelID
	}
	cfg.Timeout = 8 * time.Second
	cfg.OutputSchema = identifyBaseModelOutputSchema
	cfg.OnTextDelta = nil
	cfg.OnToolCall = nil

	prompt := fmt.Sprintf(
		"Given the custom/proxy model alias %q, identify the exact underlying official base model ID from the following candidate list.\n"+
			"Rules:\n"+
			"1. Strip proxy routing/thinking/speed/tier suffixes or prefixes (such as -high, -low, -thinking, -nothinking, -fast, -max, custom prefix) to match the true underlying model.\n"+
			"2. You MUST choose an exact string from the candidate list below. If none of the candidates reasonably match the alias, return an empty string \"\".\n\n"+
			"Candidates:\n%s",
		rawModelID,
		strings.Join(candidates, "\n"),
	)

	completion, err := c.Complete(reqCtx, cfg, []Message{
		{
			Role:    "system",
			Content: "You map proxy model aliases to canonical model registry IDs. Respond strictly with JSON matching the schema.",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}, nil)
	if err != nil {
		return "", ModelLimits{Source: LimitsSourceUnknown}, false
	}

	var parsed struct {
		BaseModel string `json:"base_model"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(completion.Content)), &parsed); err != nil {
		return "", ModelLimits{Source: LimitsSourceUnknown}, false
	}
	candidate := strings.TrimSpace(parsed.BaseModel)
	if candidate == "" {
		return "", ModelLimits{Source: LimitsSourceUnknown}, false
	}

	limits, canonical, ok := LookupRegistryModelLimitsWithCanonical(candidate, "")
	if !ok {
		return "", ModelLimits{Source: LimitsSourceUnknown}, false
	}
	now := time.Now().UTC()
	limits.VerifiedAt = &now
	return canonical, limits, true
}

// ResolveModelLimits 按优先级解析模型上下文限制：
// 1. 用户手动覆盖 (user)
// 2. 服务商返回的模型信息 (provider)
// 3. models.dev + OpenRouter 实时注册表 / 内置注册表精确匹配 (registry，支持 model 或已识别的 base_model)
// 4. 未知 (unknown，保留 0 值，不伪造默认容量)
func ResolveModelLimits(
	userOverride ModelLimits,
	providerLimits *ModelLimits,
	modelID string,
	baseModelID string,
	now time.Time,
) ModelLimits {
	verifiedAt := now.UTC()
	if verifiedAt.IsZero() {
		verifiedAt = time.Now().UTC()
	}

	// 优先级 1：用户手动配置
	if userOverride.Source == LimitsSourceUser && userOverride.HasKnownCapacity() {
		out := ModelLimits{
			ContextWindowTokens: sanitizePositiveTokenLimit(userOverride.ContextWindowTokens),
			MaxInputTokens:      sanitizePositiveTokenLimit(userOverride.MaxInputTokens),
			MaxOutputTokens:     sanitizePositiveTokenLimit(userOverride.MaxOutputTokens),
			Source:              LimitsSourceUser,
			VerifiedAt:          userOverride.VerifiedAt,
		}
		if out.VerifiedAt == nil {
			out.VerifiedAt = &verifiedAt
		}
		out = clampInputOutputToContextWindow(out)
		if out.HasKnownCapacity() {
			return out
		}
	}

	// 优先级 2：服务商返回的模型信息
	if providerLimits != nil {
		candidate := clampInputOutputToContextWindow(ModelLimits{
			ContextWindowTokens: sanitizePositiveTokenLimit(providerLimits.ContextWindowTokens),
			MaxInputTokens:      sanitizePositiveTokenLimit(providerLimits.MaxInputTokens),
			MaxOutputTokens:     sanitizePositiveTokenLimit(providerLimits.MaxOutputTokens),
			Source:              LimitsSourceProvider,
			VerifiedAt:          providerLimits.VerifiedAt,
		})
		if candidate.ContextWindowTokens > 0 || candidate.MaxInputTokens > 0 {
			if candidate.VerifiedAt == nil {
				candidate.VerifiedAt = &verifiedAt
			}
			return candidate
		}
	}

	// 优先级 3：实时注册表 / 内置注册表（对精确识别的 model 或已识别的 base_model 生效）
	if reg, ok := LookupRegistryModelLimits(modelID, baseModelID); ok {
		reg.VerifiedAt = &verifiedAt
		return reg
	}

	// 优先级 4：未知（不伪造容量）
	return ModelLimits{
		Source: LimitsSourceUnknown,
	}
}

func sanitizePositiveTokenLimit(v int) int {
	if v <= 0 || v > maxReasonableTokenLimit {
		return 0
	}
	return v
}

func clampInputOutputToContextWindow(limits ModelLimits) ModelLimits {
	if limits.ContextWindowTokens > 0 {
		if limits.MaxInputTokens > limits.ContextWindowTokens {
			limits.MaxInputTokens = limits.ContextWindowTokens
		}
		if limits.MaxOutputTokens > limits.ContextWindowTokens {
			limits.MaxOutputTokens = limits.ContextWindowTokens
		}
	}
	return limits
}
