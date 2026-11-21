package store

import (
	"strings"
	"testing"
	"time"
)

func TestBuildBuiltInProactivePrompt_包含卡片引用块级排版约束(t *testing.T) {
	prompt := buildBuiltInProactivePrompt(
		BuiltInProactiveSlots()[0],
		time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
		projectUserInterests{},
	)
	for _, want := range []string{
		"[卡片标题](cycledock://card/<card_id>)",
		"独占一行的块级卡片组件",
		"单独成行/成段",
		"严禁在前后紧跟冒号",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("built-in proactive prompt missing %q: %s", want, prompt)
		}
	}
}
