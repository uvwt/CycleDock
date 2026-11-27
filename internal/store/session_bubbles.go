package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

// SessionBubble 表示从卡片 Timeline 提取的一条干净的人机对话气泡，
// 供内置 Agent 的 cycledock_inspect_card_session 与对外 MCP 的 get_card_session(mode=simple) 共用。
type SessionBubble struct {
	Author    string    `json:"author"`
	Content   string    `json:"content,omitempty"`
	Images    []string  `json:"images,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type timelineBubbleMessage struct {
	Type        string                 `json:"type"`
	Content     string                 `json:"content"`
	URL         string                 `json:"url"`
	Alt         string                 `json:"alt"`
	Images      []string               `json:"images"`
	Widget      *ChoiceWidget          `json:"widget"`
	Preview     bool                   `json:"preview"`
	Attachments []CommentAttachmentRef `json:"attachments"`
}

// ExtractSessionBubbles 从按时间正序排列的卡片 Timeline 中提取人机对话气泡：
// 过滤纯状态流转、巡检确认和工具审批等非对话杂项，保留最近 limit 条（limit<=0 时保留全部）。
func ExtractSessionBubbles(timeline []db.ListCardTimelineRow, limit int) []SessionBubble {
	bubbles := make([]SessionBubble, 0, len(timeline))
	for _, event := range timeline {
		// 过滤纯状态流转与系统辅助事件，避免对话摘要混入 "todo -> working" 等非对话杂项。
		switch domain.EventType(event.EventType) {
		case domain.EventTypeStatusChange, domain.EventTypeCheckinAck, domain.EventTypeToolApproval:
			continue
		}

		author := "Cycle"
		if event.ActorKind == string(domain.SourceKindHuman) {
			if strings.TrimSpace(event.ActorName) != "" {
				author = event.ActorName
			} else {
				author = "User"
			}
		} else if strings.TrimSpace(event.ActorName) != "" && event.ActorName != "Cycle Agent" {
			author = event.ActorName
		}

		var content string
		var images []string
		var imageAlt string
		if len(event.Message) > 0 {
			var msg timelineBubbleMessage
			if err := json.Unmarshal(event.Message, &msg); err == nil {
				// 未落盘的流式预览气泡不作为正式历史返回。
				if msg.Preview {
					continue
				}
				content = msg.Content
				images = msg.Images
				imageAlt = strings.TrimSpace(msg.Alt)
				if len(images) == 0 && len(msg.Attachments) > 0 {
					for _, att := range msg.Attachments {
						if att.Kind != "text" && strings.TrimSpace(att.URL) != "" {
							images = append(images, strings.TrimSpace(att.URL))
						}
					}
				}
				switch {
				case msg.Type == "choice" && msg.Widget != nil:
					var choiceLines []string
					if strings.TrimSpace(content) != "" {
						choiceLines = append(choiceLines, content)
					}
					if strings.TrimSpace(msg.Widget.Question) != "" {
						choiceLines = append(choiceLines, msg.Widget.Question)
					}
					for i, opt := range msg.Widget.Options {
						label := opt.Label
						if label == "" {
							label = opt.Value
						}
						choiceLines = append(choiceLines, fmt.Sprintf("%d. %s", i+1, label))
					}
					content = strings.Join(choiceLines, "\n")
				case msg.Type == "attachment" && strings.TrimSpace(msg.URL) != "":
					if strings.TrimSpace(content) == "" {
						if imageAlt != "" {
							content = fmt.Sprintf("%s (%s)", imageAlt, strings.TrimSpace(msg.URL))
						} else {
							content = strings.TrimSpace(msg.URL)
						}
					}
				}
			}
		}

		// 人类评论的正文存储在 event.Content 而非 event.Message.content，
		// 因此需先回退 event.Content，再对真正的纯图片气泡填充兜底文案。
		if strings.TrimSpace(content) == "" && strings.TrimSpace(event.Content) != "" {
			content = event.Content
		}
		if strings.TrimSpace(content) == "" && len(images) > 0 {
			if imageAlt != "" {
				content = imageAlt
			} else if len(images) == 1 {
				content = "[图片]"
			} else {
				content = fmt.Sprintf("[%d 张图片]", len(images))
			}
		}
		if strings.TrimSpace(content) == "" && len(images) == 0 {
			continue
		}

		bubbles = append(bubbles, SessionBubble{
			Author:    author,
			Content:   content,
			Images:    images,
			Timestamp: event.CreatedAt.Time.UTC(),
		})
	}

	if limit > 0 && len(bubbles) > limit {
		bubbles = bubbles[len(bubbles)-limit:]
	}
	return bubbles
}
