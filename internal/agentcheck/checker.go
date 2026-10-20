package agentcheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/uvwt/CycleDock/internal/agentconfig"
	"github.com/uvwt/CycleDock/internal/mcpclient"
	"github.com/uvwt/CycleDock/internal/modelclient"
)

type Result struct {
	OK         bool   `json:"ok"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	ToolCount  int    `json:"tool_count,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type ProviderInput struct {
	BaseURL   string
	Model     string
	APIKeyEnv string
	Timeout   time.Duration
}

type MCPInput struct {
	Name           string
	URL            string
	BearerTokenEnv string
	Timeout        time.Duration
}

type Checker struct {
	model *modelclient.Client
}

func New() *Checker {
	return &Checker{model: modelclient.New()}
}

func NewWithModelClient(model *modelclient.Client) *Checker {
	if model == nil {
		model = modelclient.New()
	}
	return &Checker{model: model}
}

func (c *Checker) Provider(ctx context.Context, input ProviderInput) Result {
	started := time.Now()
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	input.Model = strings.TrimSpace(input.Model)
	input.APIKeyEnv = strings.TrimSpace(input.APIKeyEnv)
	if err := agentconfig.ValidateEndpoint(input.BaseURL); err != nil {
		return failure(started, "invalid_endpoint", err.Error())
	}
	if input.Model == "" {
		return failure(started, "invalid_model", "Model is required.")
	}
	if input.APIKeyEnv != "" && !agentconfig.ValidEnvName(input.APIKeyEnv) {
		return failure(started, "invalid_secret_env", "Model API key environment variable name is invalid.")
	}
	apiKey, ok := resolveSecret(input.APIKeyEnv)
	if !ok {
		return failure(started, "secret_env_missing", "Configured model API key environment variable is not set.")
	}
	if input.Timeout <= 0 {
		input.Timeout = 120 * time.Second
	}
	return c.checkProvider(ctx, started, modelclient.Config{
		BaseURL: input.BaseURL,
		Model:   input.Model,
		APIKey:  apiKey,
		Timeout: input.Timeout,
	})
}

func (c *Checker) ProviderConfig(ctx context.Context, config modelclient.Config) Result {
	started := time.Now()
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	config.Model = strings.TrimSpace(config.Model)
	if err := agentconfig.ValidateEndpoint(config.BaseURL); err != nil {
		return failure(started, "invalid_endpoint", err.Error())
	}
	if config.Model == "" {
		return failure(started, "invalid_model", "Model is required.")
	}
	if config.Timeout <= 0 {
		config.Timeout = 120 * time.Second
	}
	return c.checkProvider(ctx, started, config)
}

func (c *Checker) checkProvider(ctx context.Context, started time.Time, config modelclient.Config) Result {
	_, err := c.model.Complete(ctx, config, []modelclient.Message{{
		Role:    "user",
		Content: "Connection test. Reply with OK.",
	}}, nil)
	if err == nil {
		return success(started, "Provider connected and model responded.", 0)
	}

	var apiErr *modelclient.APIError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return failure(started, "timeout", "Provider request timed out.")
	case errors.As(err, &apiErr):
		switch apiErr.StatusCode {
		case 401, 403:
			return failure(started, "authentication_failed", "Provider rejected authentication.")
		case 404:
			return failure(started, "model_or_endpoint_not_found", "Provider endpoint or model was not found.")
		case 429:
			return failure(started, "rate_limited", "Provider rate limit rejected the test request.")
		default:
			if apiErr.StatusCode >= 500 {
				return failure(started, "provider_unavailable", "Provider returned a server error.")
			}
			return failure(started, "provider_http_error", fmt.Sprintf("Provider returned HTTP %d.", apiErr.StatusCode))
		}
	case errors.Is(err, modelclient.ErrMalformedResponse), errors.Is(err, modelclient.ErrEmptyResponse):
		return failure(started, "malformed_response", "Provider returned an invalid model response.")
	case errors.Is(err, modelclient.ErrResponseTooLarge):
		return failure(started, "response_too_large", "Provider response exceeded the safety limit.")
	default:
		return failure(started, "connection_failed", "Could not connect to the model provider.")
	}
}

func (c *Checker) MCP(ctx context.Context, input MCPInput) Result {
	started := time.Now()
	input.Name = strings.TrimSpace(input.Name)
	input.URL = strings.TrimSpace(input.URL)
	input.BearerTokenEnv = strings.TrimSpace(input.BearerTokenEnv)
	if input.Name == "" {
		return failure(started, "invalid_name", "MCP connection name is required.")
	}
	if err := agentconfig.ValidateEndpoint(input.URL); err != nil {
		return failure(started, "invalid_endpoint", err.Error())
	}
	if input.BearerTokenEnv != "" && !agentconfig.ValidEnvName(input.BearerTokenEnv) {
		return failure(started, "invalid_secret_env", "MCP bearer token environment variable name is invalid.")
	}
	token, ok := resolveSecret(input.BearerTokenEnv)
	if !ok {
		return failure(started, "secret_env_missing", "Configured MCP bearer token environment variable is not set.")
	}
	if input.Timeout <= 0 {
		input.Timeout = 30 * time.Second
	}

	sessions, err := mcpclient.Connect(ctx, []mcpclient.Connection{{
		Name: input.Name, URL: input.URL, BearerToken: token, Timeout: input.Timeout,
	}})
	if err == nil {
		defer sessions.Close()
		return success(started, "MCP initialized and tools were listed.", len(sessions.ToolDefinitions()))
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return failure(started, "timeout", "MCP connection timed out.")
	}
	if looksLikeAuthFailure(err) {
		return failure(started, "authentication_failed", "MCP server rejected authentication.")
	}
	var connectErr *mcpclient.ConnectError
	if errors.As(err, &connectErr) {
		switch connectErr.Phase {
		case mcpclient.ConnectPhaseListTools:
			return failure(started, "list_tools_failed", "MCP initialized but ListTools failed.")
		default:
			return failure(started, "initialize_failed", "MCP initialize failed.")
		}
	}
	return failure(started, "connection_failed", "Could not connect to the MCP endpoint.")
}

func resolveSecret(envName string) (string, bool) {
	if envName == "" {
		return "", true
	}
	if !agentconfig.ValidEnvName(envName) {
		return "", false
	}
	value, ok := os.LookupEnv(envName)
	if !ok || strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}

func looksLikeAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "401") ||
		strings.Contains(text, "403") ||
		strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "forbidden")
}

func success(started time.Time, message string, toolCount int) Result {
	return Result{
		OK: true, Code: "ok", Message: message, ToolCount: toolCount,
		DurationMS: elapsedMilliseconds(started),
	}
}

func failure(started time.Time, code, message string) Result {
	return Result{
		OK: false, Code: code, Message: message,
		DurationMS: elapsedMilliseconds(started),
	}
}

func elapsedMilliseconds(started time.Time) int64 {
	elapsed := time.Since(started).Milliseconds()
	if elapsed < 0 {
		return 0
	}
	return elapsed
}
