package agent

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/mcpclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestPrepareMCPStructuredResultAndReadArtifactForImages(t *testing.T) {
	ctx := context.Background()
	artifactSvc := artifact.InMemoryService()
	workspaceID := uuid.New()
	cardID := uuid.New()
	attemptID := uuid.New()

	// 超过 12000 字符 base64 阈值的 PNG 图片（20KB -> ~27KB base64）。
	largePNG := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x7f}, 20*1024)...)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "native-vision", Version: "1.0.0"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "view_image", Description: "View image"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "loaded screenshot"},
					&mcp.ImageContent{MIMEType: "image/png", Data: largePNG},
				},
			}, nil, nil
		},
	)
	mcpHTTP := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil))
	defer mcpHTTP.Close()

	sessions, err := mcpclient.Connect(ctx, []mcpclient.Connection{{
		Name:            "__nexusdock_first_party",
		URL:             mcpHTTP.URL,
		Timeout:         2 * time.Second,
		NativeToolNames: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Close()

	callRes, err := sessions.CallStructured(ctx, "view_image", nil)
	if err != nil {
		t.Fatalf("CallStructured failed: %v", err)
	}

	rt := attemptToolRuntime{
		context: store.AgentCardContext{
			WorkspaceID: workspaceID,
			CardID:      cardID,
			AttemptID:   attemptID,
			CardKind:    domain.CardKindTodo,
		},
		mcpSessions:     sessions,
		artifactService: artifactSvc,
	}

	prepared, err := rt.prepareMCPStructuredResult(ctx, "view_image", callRes)
	if err != nil {
		t.Fatalf("prepareMCPStructuredResult failed: %v", err)
	}
	if truncated, _ := prepared["truncated"].(bool); truncated {
		t.Fatalf("image MCP result must not be marked truncated by 12000-char text threshold: %+v", prepared)
	}
	rawRefs, ok := prepared[imageArtifactsFieldKey].([]map[string]any)
	if !ok || len(rawRefs) != 1 {
		t.Fatalf("expected 1 image artifact ref, got %+v", prepared[imageArtifactsFieldKey])
	}
	refName, _ := rawRefs[0][imageArtifactRefKey].(string)
	refVer, _ := rawRefs[0][imageArtifactVersionKey].(int64)
	if refName == "" || refVer == 0 {
		t.Fatalf("invalid image artifact ref: %+v", rawRefs[0])
	}

	// 验证持久化到 artifactService 的二进制内容完全无损。
	appName, userID, sessionID := agentsession.Identity(workspaceID, cardID)
	loaded, err := artifactSvc.Load(ctx, &artifact.LoadRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
		FileName: refName, Version: refVer,
	})
	if err != nil {
		t.Fatalf("artifactService.Load failed: %v", err)
	}
	if loaded.Part == nil || loaded.Part.InlineData == nil || !bytes.Equal(loaded.Part.InlineData.Data, largePNG) {
		t.Fatalf("stored artifact bytes mismatch")
	}

	// 验证通过 cycledock_read_artifact 读取图片 Artifact 时返回可恢复的 image_artifacts 元数据而非乱码文本。
	readHandler, err := rt.internalHandler("cycledock_read_artifact")
	if err != nil {
		t.Fatalf("internalHandler failed: %v", err)
	}
	readOut, err := readHandler(dummyToolContext{ctx: ctx}, map[string]any{
		"file_name": refName,
		"version":   refVer,
	})
	if err != nil {
		t.Fatalf("cycledock_read_artifact on image failed: %v", err)
	}
	if isImg, _ := readOut["is_image"].(bool); !isImg {
		t.Fatalf("expected is_image=true, got %+v", readOut)
	}
	if _, hasContent := readOut["content"]; hasContent {
		t.Fatalf("image artifact should not dump raw binary into content text field: %+v", readOut)
	}

	// 验证 adkRequestToModelClientWithArtifacts 能从 cycledock_read_artifact 的 FunctionResponse 重新水合图像给模型。
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{
				Role: genai.RoleModel,
				Parts: []*genai.Part{
					func() *genai.Part {
						p := genai.NewPartFromFunctionCall("cycledock_read_artifact", map[string]any{"file_name": refName})
						p.FunctionCall.ID = "read-img-1"
						return p
					}(),
				},
			},
			{
				Role: genai.RoleUser,
				Parts: []*genai.Part{{
					FunctionResponse: &genai.FunctionResponse{
						ID:       "read-img-1",
						Name:     "cycledock_read_artifact",
						Response: readOut,
					},
				}},
			},
		},
	}
	messages, _, err := adkRequestToModelClientWithArtifacts(ctx, req, artifactSvc, workspaceID, cardID)
	if err != nil {
		t.Fatalf("adkRequestToModelClientWithArtifacts failed: %v", err)
	}
	if len(messages) != 3 || messages[1].Role != "tool" || messages[2].Role != "user" || !messages[2].HasImageParts() {
		t.Fatalf("unexpected messages sequence: %+v", messages)
	}
	imgURL, err := messages[2].Parts[1].ImageDataURL()
	if err != nil || !strings.HasPrefix(imgURL, "data:image/png;base64,") {
		t.Fatalf("rehydrated image URL invalid: err=%v url=%q", err, imgURL)
	}
}

// dummyToolContext 仅用于单元测试直接调用内部工具 handler。
type dummyToolContext struct {
	adkagent.Context
	ctx context.Context
}

func (d dummyToolContext) Deadline() (time.Time, bool)       { return d.ctx.Deadline() }
func (d dummyToolContext) Done() <-chan struct{}             { return d.ctx.Done() }
func (d dummyToolContext) Err() error                        { return d.ctx.Err() }
func (d dummyToolContext) Value(key any) any                 { return d.ctx.Value(key) }
func (d dummyToolContext) Actions() *session.EventActions    { return &session.EventActions{} }
func (d dummyToolContext) FunctionCallID() string            { return "test-call" }
