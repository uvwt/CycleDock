package nexusdock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFetchDevices_仅返回设备摘要并复用Bearer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/integrations/devices" || r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("设备目录请求错误: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"devices":[{"id":"node-a","name":"RN","online":true,"os":"linux","arch":"amd64","agent_version":"1.1.1","access_token":"must-not-return","context":{"secret":"must-not-return"}}]}`)
	}))
	defer server.Close()
	svc := &Service{client: server.Client()}
	devices, err := svc.fetchDevices(context.Background(), RuntimeConnection{URL: server.URL + "/mcp", AccessToken: "secret-token"})
	if err != nil || len(devices) != 1 || devices[0].ID != "node-a" || !devices[0].Online {
		t.Fatalf("设备摘要错误: devices=%v err=%v", devices, err)
	}
	raw, err := json.Marshal(devices)
	if err != nil || strings.Contains(string(raw), "must-not-return") || strings.Contains(string(raw), "access_token") {
		t.Fatalf("响应不应包含凭据或上下文: %s err=%v", raw, err)
	}
}

func TestFetchDevices_拒绝重定向且不发送凭据(t *testing.T) {
	var reached atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer source.Close()
	svc := &Service{client: source.Client()}
	if _, err := svc.fetchDevices(context.Background(), RuntimeConnection{URL: source.URL + "/mcp", AccessToken: "secret-token"}); err == nil || reached.Load() {
		t.Fatalf("重定向必须在发送凭据前拒绝: err=%v reached=%v", err, reached.Load())
	}
}

func TestFetchDevices_错误响应不伪装成空列表(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		reconnect bool
	}{
		{"授权失效", 401, "upstream-secret", true},
		{"权限撤销", 403, "upstream-secret", true},
		{"服务不可用", 503, "upstream-secret", false},
		{"无效JSON", 200, "upstream-secret", false},
		{"缺少设备字段", 200, `{}`, false},
		{"响应超限", 200, strings.Repeat("x", (2<<20)+1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.status); fmt.Fprint(w, test.body) }))
			defer server.Close()
			svc := &Service{client: server.Client()}
			_, err := svc.fetchDevices(context.Background(), RuntimeConnection{URL: server.URL + "/mcp", AccessToken: "token"})
			if err == nil || errors.Is(err, ErrConnectionExpired) != test.reconnect || strings.Contains(err.Error(), "upstream-secret") {
				t.Fatalf("错误映射或脱敏错误: %v", err)
			}
		})
	}
}
