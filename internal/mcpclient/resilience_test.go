package mcpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestClassifyError(t *testing.T) {
	// 1. Caller canceled
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if cat := ClassifyError(canceledCtx, errors.New("something went wrong")); cat != CategoryCallerCanceled {
		t.Fatalf("expected CategoryCallerCanceled, got %v", cat)
	}

	// 2. Timeout
	_, dCancel := context.WithTimeout(context.Background(), time.Nanosecond)
	time.Sleep(time.Millisecond)
	dCancel()
	if cat := ClassifyError(context.Background(), context.DeadlineExceeded); cat != CategoryTimeout {
		t.Fatalf("expected CategoryTimeout, got %v", cat)
	}

	// 3. Connection closed
	if cat := ClassifyError(context.Background(), errors.New("mcp: connection is closed")); cat != CategoryConnectionClosed {
		t.Fatalf("expected CategoryConnectionClosed, got %v", cat)
	}
	if cat := ClassifyError(context.Background(), errors.New("read tcp 127.0.0.1: wsarecv: connection reset by peer")); cat != CategoryConnectionClosed {
		t.Fatalf("expected CategoryConnectionClosed, got %v", cat)
	}

	// 4. Transient error
	if cat := ClassifyError(context.Background(), errors.New("status 503 Service Unavailable")); cat != CategoryTransient {
		t.Fatalf("expected CategoryTransient, got %v", cat)
	}
	if cat := ClassifyError(context.Background(), errors.New("HTTP 429 Too Many Requests")); cat != CategoryTransient {
		t.Fatalf("expected CategoryTransient, got %v", cat)
	}

	// 5. Terminal error
	if cat := ClassifyError(context.Background(), errors.New("invalid parameter: foo")); cat != CategoryTerminal {
		t.Fatalf("expected CategoryTerminal, got %v", cat)
	}
}

func TestSessionSet_ReconnectOnBrokenConnection(t *testing.T) {
	ctx := context.Background()
	var callCount int32

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0"}, nil)
	type pingInput struct{}
	type pingOutput struct {
		Reply string `json:"reply"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "ping tool"},
		func(ctx context.Context, request *mcp.CallToolRequest, input pingInput) (*mcp.CallToolResult, pingOutput, error) {
			c := atomic.AddInt32(&callCount, 1)
			return nil, pingOutput{Reply: "pong-" + string(rune('0'+c))}, nil
		},
	)

	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	set, err := Connect(ctx, []Connection{{
		Name:    "demo",
		URL:     httpServer.URL,
		Timeout: 5 * time.Second,
	}})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer set.Close()

	// First call succeeds
	res1, err := set.CallExternal(ctx, "demo", "ping", nil)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if res1 == "" {
		t.Fatalf("expected non-empty result")
	}

	// Simulate broken connection by closing the underlying session
	set.mu.Lock()
	sess := set.sessions["demo"]
	set.mu.Unlock()
	if sess != nil && sess.session != nil {
		_ = sess.session.Close()
	}

	// Second call should detect connection closed, auto-reconnect, and succeed!
	res2, err := set.CallExternal(ctx, "demo", "ping", nil)
	if err != nil {
		t.Fatalf("call after connection close should auto-reconnect, but got error: %v", err)
	}
	if res2 == "" {
		t.Fatalf("expected non-empty result after reconnect")
	}
}

func TestSessionSet_CallerCancelDoesNotRetry(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0"}, nil)
	type pingInput struct{}
	type pingOutput struct {
		Reply string `json:"reply"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "ping tool"},
		func(ctx context.Context, request *mcp.CallToolRequest, input pingInput) (*mcp.CallToolResult, pingOutput, error) {
			return nil, pingOutput{Reply: "ok"}, nil
		},
	)

	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	set, err := Connect(context.Background(), []Connection{{
		Name:    "demo",
		URL:     httpServer.URL,
		Timeout: 5 * time.Second,
	}})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer set.Close()

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before calling

	_, err = set.CallExternal(canceledCtx, "demo", "ping", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestSessionSet_ToolTimeoutDoesNotKillSession(t *testing.T) {
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0"}, nil)
	type dummyInput struct{}
	type dummyOutput struct {
		Reply string `json:"reply"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "slow", Description: "slow tool"},
		func(ctx context.Context, request *mcp.CallToolRequest, input dummyInput) (*mcp.CallToolResult, dummyOutput, error) {
			select {
			case <-time.After(300 * time.Millisecond):
				return nil, dummyOutput{Reply: "slow-done"}, nil
			case <-ctx.Done():
				return nil, dummyOutput{}, ctx.Err()
			}
		},
	)
	mcp.AddTool(server, &mcp.Tool{Name: "fast", Description: "fast tool"},
		func(ctx context.Context, request *mcp.CallToolRequest, input dummyInput) (*mcp.CallToolResult, dummyOutput, error) {
			return nil, dummyOutput{Reply: "fast-done"}, nil
		},
	)

	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	// Short timeout of 50ms so "slow" will definitely timeout
	set, err := Connect(ctx, []Connection{{
		Name:    "demo",
		URL:     httpServer.URL,
		Timeout: 50 * time.Millisecond,
	}})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer set.Close()

	// 1. Call slow tool - should fail with timeout
	_, err = set.CallExternal(ctx, "demo", "slow", nil)
	if err == nil {
		t.Fatalf("expected slow tool to timeout, but it succeeded")
	}

	// 2. Immediately call fast tool with fresh context - should succeed without connection closed!
	res, err := set.CallExternal(ctx, "demo", "fast", nil)
	if err != nil {
		t.Fatalf("fast tool should succeed after slow tool timeout, but got: %v", err)
	}
	if res == "" {
		t.Fatalf("expected non-empty result from fast tool")
	}
}

func TestSessionSet_ConcurrentCallsDuringReconnect(t *testing.T) {
	ctx := context.Background()
	var callCount int32

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0"}, nil)
	type pingInput struct{}
	type pingOutput struct {
		Reply string `json:"reply"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "ping tool"},
		func(ctx context.Context, request *mcp.CallToolRequest, input pingInput) (*mcp.CallToolResult, pingOutput, error) {
			c := atomic.AddInt32(&callCount, 1)
			return nil, pingOutput{Reply: "pong-" + string(rune('0'+c))}, nil
		},
	)

	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	set, err := Connect(ctx, []Connection{{
		Name:    "demo",
		URL:     httpServer.URL,
		Timeout: 5 * time.Second,
	}})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer set.Close()

	// Break session
	set.mu.Lock()
	sess := set.sessions["demo"]
	set.mu.Unlock()
	if sess != nil && sess.session != nil {
		_ = sess.session.Close()
	}

	// Concurrently invoke tools
	const concurrency = 5
	errChan := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			_, callErr := set.CallExternal(ctx, "demo", "ping", nil)
			errChan <- callErr
		}()
	}

	for i := 0; i < concurrency; i++ {
		if err := <-errChan; err != nil {
			t.Fatalf("concurrent call failed: %v", err)
		}
	}
}
