package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/uvwt/CycleDock/internal/nexusdock"
)

const nexusDockRESTMaxResponseBytes int64 = 4 << 20

var errNexusDockRedirect = errors.New("NexusDock REST redirect refused")

// nexusDockRESTClient 是 CycleDock 第一方 NexusDock HTTP 资源的凭据边界。
// Bearer 只发送到 MCP URL 对应的同 origin REST API，并拒绝所有重定向，避免凭据被 3xx 带走。
type nexusDockRESTClient struct {
	base  url.URL
	token string
	http  *http.Client
}

func newNexusDockRESTClient(connection nexusdock.RuntimeConnection) (*nexusDockRESTClient, bool) {
	endpoint, err := url.Parse(strings.TrimSpace(connection.URL))
	if err != nil || endpoint.Host == "" || endpoint.Path != "/mcp" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, false
	}
	loopback := endpoint.Hostname() == "localhost" || endpoint.Hostname() == "127.0.0.1" || endpoint.Hostname() == "::1"
	if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && loopback) {
		return nil, false
	}
	return &nexusDockRESTClient{
		base:  url.URL{Scheme: endpoint.Scheme, Host: endpoint.Host},
		token: connection.AccessToken,
		http: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return errNexusDockRedirect },
		},
	}, true
}

func (c *nexusDockRESTClient) request(
	ctx context.Context,
	method, path string,
	body any,
	headers map[string]string,
	maxBytes int64,
) (int, http.Header, []byte, error) {
	if maxBytes <= 0 {
		maxBytes = nexusDockRESTMaxResponseBytes
	}
	endpoint := c.base
	endpoint.Path = path
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("encode NexusDock request: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bodyReader)
	if err != nil {
		return 0, nil, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return response.StatusCode, response.Header.Clone(), nil, err
	}
	if int64(len(raw)) > maxBytes {
		return response.StatusCode, response.Header.Clone(), nil, errors.New("NexusDock response too large")
	}
	return response.StatusCode, response.Header.Clone(), raw, nil
}

func (c *nexusDockRESTClient) json(
	ctx context.Context,
	method, path string,
	body any,
	headers map[string]string,
	destination any,
) (int, http.Header, error) {
	status, responseHeaders, raw, err := c.request(ctx, method, path, body, headers, nexusDockRESTMaxResponseBytes)
	if err != nil {
		return status, responseHeaders, err
	}
	if destination != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, destination); err != nil {
			return status, responseHeaders, fmt.Errorf("parse NexusDock response: %w", err)
		}
	}
	return status, responseHeaders, nil
}

func (c *nexusDockRESTClient) stream(
	ctx context.Context,
	path string,
	headers map[string]string,
) (*http.Response, error) {
	endpoint := c.base
	endpoint.Path = path
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "text/event-stream")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return c.http.Do(request)
}

func nexusDockRequestContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return context.WithTimeout(parent, timeout)
}
