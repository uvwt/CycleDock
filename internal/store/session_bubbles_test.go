package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/sqldb"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func TestExtractSessionBubbles(t *testing.T) {
	baseTime := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	mustJSON := func(v any) []byte {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	timeline := []db.ListCardTimelineRow{
		{
			EventType: string(domain.EventTypeComment),
			ActorKind: string(domain.SourceKindHuman),
			ActorName: "Alice",
			Content:   "请排查构建失败原因",
			CreatedAt: sqldb.Timestamptz{Time: baseTime, Valid: true},
		},
		{
			EventType: string(domain.EventTypeStatusChange),
			ActorKind: string(domain.SourceKindCycleAgent),
			ActorName: "Cycle Agent",
			Content:   "todo -> working",
			CreatedAt: sqldb.Timestamptz{Time: baseTime.Add(time.Second), Valid: true},
		},
		{
			EventType: string(domain.EventTypeToolApproval),
			ActorKind: string(domain.SourceKindCycleAgent),
			ActorName: "Cycle Agent",
			Content:   "approval requested",
			CreatedAt: sqldb.Timestamptz{Time: baseTime.Add(2 * time.Second), Valid: true},
		},
		{
			EventType: string(domain.EventTypeCheckinAck),
			ActorKind: string(domain.SourceKindHuman),
			ActorName: "Alice",
			Content:   "acknowledged",
			CreatedAt: sqldb.Timestamptz{Time: baseTime.Add(3 * time.Second), Valid: true},
		},
		{
			EventType: "message",
			ActorKind: string(domain.SourceKindCycleAgent),
			ActorName: "Cycle Agent",
			Message: mustJSON(replyPayload{
				ReplyMessage: ReplyMessage{
					Type:    "text",
					Content: "预览中的草稿不应该出现",
				},
				Preview: true,
			}),
			CreatedAt: sqldb.Timestamptz{Time: baseTime.Add(4 * time.Second), Valid: true},
		},
		{
			EventType: "message",
			ActorKind: string(domain.SourceKindCycleAgent),
			ActorName: "Cycle Agent",
			Message: mustJSON(ReplyMessage{
				Type:   "text",
				Images: []string{"https://cdn.example.com/a.png"},
			}),
			CreatedAt: sqldb.Timestamptz{Time: baseTime.Add(5 * time.Second), Valid: true},
		},
		{
			EventType: "message",
			ActorKind: string(domain.SourceKindCycleAgent),
			ActorName: "Cycle Agent",
			Message: mustJSON(ReplyMessage{
				Type:    "choice",
				Content: "发现两种修复方案",
				Widget: &ChoiceWidget{
					Question: "请选择处理方式",
					Options: []ChoiceOption{
						{Value: "fix_now", Label: "立即修复"},
						{Value: "defer", Label: ""},
					},
				},
			}),
			CreatedAt: sqldb.Timestamptz{Time: baseTime.Add(6 * time.Second), Valid: true},
		},
		{
			EventType: string(domain.EventTypeComment),
			ActorKind: string(domain.SourceKindHuman),
			ActorName: "",
			Content:   "附上一张截图",
			Message: mustJSON(CommentMessagePayload{
				Attachments: []CommentAttachmentRef{
					{Kind: "image", URL: "https://cdn.example.com/user-shot.png"},
					{Kind: "text", URL: "https://cdn.example.com/log.txt"},
				},
			}),
			CreatedAt: sqldb.Timestamptz{Time: baseTime.Add(7 * time.Second), Valid: true},
		},
	}

	bubbles := ExtractSessionBubbles(timeline, 50)
	if len(bubbles) != 4 {
		t.Fatalf("got %d bubbles, want 4: %+v", len(bubbles), bubbles)
	}
	if bubbles[0].Author != "Alice" || bubbles[0].Content != "请排查构建失败原因" {
		t.Fatalf("bubble[0]=%+v", bubbles[0])
	}
	if bubbles[1].Author != "Cycle" || bubbles[1].Content != "[图片]" || len(bubbles[1].Images) != 1 || bubbles[1].Images[0] != "https://cdn.example.com/a.png" {
		t.Fatalf("bubble[1]=%+v", bubbles[1])
	}
	wantChoice := "发现两种修复方案\n请选择处理方式\n1. 立即修复\n2. defer"
	if bubbles[2].Author != "Cycle" || bubbles[2].Content != wantChoice {
		t.Fatalf("bubble[2]=%+v, want content %q", bubbles[2], wantChoice)
	}
	if bubbles[3].Author != "User" || bubbles[3].Content != "附上一张截图" || len(bubbles[3].Images) != 1 || bubbles[3].Images[0] != "https://cdn.example.com/user-shot.png" {
		t.Fatalf("bubble[3]=%+v", bubbles[3])
	}

	limited := ExtractSessionBubbles(timeline, 2)
	if len(limited) != 2 || limited[0].Content != wantChoice || limited[1].Content != "附上一张截图" {
		t.Fatalf("limited bubbles=%+v", limited)
	}
}
