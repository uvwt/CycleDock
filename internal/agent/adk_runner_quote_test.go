package agent

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestTurnMessageIncludesStructuredQuote(t *testing.T) {
	got := turnMessage(store.AgentCardContext{
		TurnContent: "这里为什么要用写后失效？",
		TurnQuote: &store.CommentQuote{
			EventID: uuid.New(),
			Sender:  "Cycle",
			Text:    "第一步检查迁移\n第二步采用写后失效",
		},
	})
	want := "[引用 Cycle 的消息内容]\n> 第一步检查迁移\n> 第二步采用写后失效\n\n这里为什么要用写后失效？"
	if got != want {
		t.Fatalf("turnMessage mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestTurnMessageWithoutQuoteRemainsClean(t *testing.T) {
	got := turnMessage(store.AgentCardContext{
		TurnContent: "直接继续下一步",
	})
	if got != "直接继续下一步" {
		t.Fatalf("unexpected turnMessage without quote: %q", got)
	}
}

func TestBuildUserTurnContentEmbedsQuoteTextInModelInput(t *testing.T) {
	content := buildUserTurnContent(store.AgentCardContext{
		TurnContent: "请展开说明",
		TurnQuote: &store.CommentQuote{
			EventID: uuid.New(),
			Sender:  "",
			Text:    "核心策略摘要",
		},
	})
	text := textFromADKContent(content)
	if !strings.Contains(text, "[引用历史消息内容]") || !strings.Contains(text, "> 核心策略摘要") || !strings.Contains(text, "请展开说明") {
		t.Fatalf("expected quote and user text in ADK content, got %q", text)
	}
}
