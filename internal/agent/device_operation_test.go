package agent

import (
	"context"
	"iter"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/platform"
	adkrunner "google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/realtime"
)

func TestOperatingDeviceNameUsesSnapshotThenNodeID(t *testing.T) {
	devices := map[string]string{"node_a": "RN"}
	if got := operatingDeviceName(devices, map[string]any{"node_id": "node_a", "cmd": "docker ps"}); got != "RN" {
		t.Fatalf("named device = %q", got)
	}
	if got := operatingDeviceName(devices, map[string]any{"node_id": "node_missing"}); got != "node_missing" {
		t.Fatalf("unknown device = %q", got)
	}
	if got := operatingDeviceName(devices, map[string]any{"query": "deploy"}); got != "" {
		t.Fatalf("tool without node_id = %q", got)
	}
}

func TestToolCallPublishesOperatingDeviceUntilItReturns(t *testing.T) {
	cardID := uuid.New()
	attemptID := uuid.New()
	hub := realtime.NewHub()
	hub.Publish(cardID, realtime.Event{Type: "attempt.started", AttemptID: attemptID.String()})
	callbacks := operatingDeviceCallbacks(hub, cardID, attemptID, map[string]string{"node_a": "RN"}, &replyState{})
	execTool, err := functiontool.New(functiontool.Config{
		Name: "exec_command", Description: "Run a command on a device",
	}, func(agent.Context, map[string]any) (map[string]any, error) {
		sub, snapshot := hub.Subscribe(cardID)
		sub.Close()
		if snapshot == nil || snapshot.DeviceName != "RN" {
			t.Errorf("device while tool runs = %+v", snapshot)
		}
		return map[string]any{"ok": true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	recall, err := functiontool.New(functiontool.Config{
		Name: "recall_search", Description: "Search shared knowledge",
	}, func(agent.Context, map[string]any) (map[string]any, error) {
		sub, snapshot := hub.Subscribe(cardID)
		sub.Close()
		if snapshot == nil || snapshot.DeviceName != "" {
			t.Errorf("non-device tool should not keep a device: %+v", snapshot)
		}
		return map[string]any{"ok": true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	llm := &deviceOperationTestModel{}
	root, err := llmagent.New(llmagent.Config{
		Name: "device_test", Model: llm, Tools: []tool.Tool{execTool, recall},
		BeforeToolCallbacks: callbacks.before,
		AfterToolCallbacks:  callbacks.after,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := adkrunner.New(adkrunner.Config{
		AppName: "device_test", Agent: root, SessionService: session.InMemoryService(), AutoCreateSession: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 与正式 Runner 一样按模型给出的顺序执行工具，避免后一个工具提前读到上一台设备。
	runCtx := platform.WithTaskRunner(context.Background(), func(ctx context.Context, tasks []func(context.Context)) {
		for _, task := range tasks {
			task(ctx)
		}
	})
	for _, err := range runner.Run(runCtx, "workspace", "card", genai.NewContentFromText("inspect", genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	sub, snapshot := hub.Subscribe(cardID)
	sub.Close()
	if snapshot == nil || snapshot.DeviceName != "" {
		t.Fatalf("device remains after tools return: %+v", snapshot)
	}
	if llm.calls != 2 {
		t.Fatalf("model calls = %d", llm.calls)
	}
}

type deviceOperationTestModel struct{ calls int }

func (*deviceOperationTestModel) Name() string { return "device_test" }

func (m *deviceOperationTestModel) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.calls++
		if m.calls == 1 {
			yield(&model.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
				{FunctionCall: &genai.FunctionCall{ID: "device", Name: "exec_command", Args: map[string]any{"node_id": "node_a", "cmd": "docker ps"}}},
				{FunctionCall: &genai.FunctionCall{ID: "recall", Name: "recall_search", Args: map[string]any{"query": "deploy"}}},
			}}}, nil)
			return
		}
		yield(&model.LLMResponse{Content: genai.NewContentFromText("done", genai.RoleModel)}, nil)
	}
}
