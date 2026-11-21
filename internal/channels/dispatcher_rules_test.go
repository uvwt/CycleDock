package channels

import (
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestShouldDeliverMainChatMessage(t *testing.T) {
	chA := uuid.New()
	chB := uuid.New()

	cases := []struct {
		name        string
		mirrorWeb   bool
		hasAttempt  bool
		origin      store.AttemptTurnOrigin
		foundOrigin bool
		want        bool
	}{
		{
			name:        "mirror_off_own_channel_human_turn_delivered",
			mirrorWeb:   false,
			hasAttempt:  true,
			origin:      store.AttemptTurnOrigin{Source: domain.TurnSourceHuman, OriginChannelID: chA, HasOrigin: true},
			foundOrigin: true,
			want:        true,
		},
		{
			name:        "mirror_off_web_human_turn_blocked",
			mirrorWeb:   false,
			hasAttempt:  true,
			origin:      store.AttemptTurnOrigin{Source: domain.TurnSourceHuman, HasOrigin: false},
			foundOrigin: true,
			want:        false,
		},
		{
			name:        "mirror_off_other_channel_human_turn_blocked",
			mirrorWeb:   false,
			hasAttempt:  true,
			origin:      store.AttemptTurnOrigin{Source: domain.TurnSourceHuman, OriginChannelID: chB, HasOrigin: true},
			foundOrigin: true,
			want:        false,
		},
		{
			name:        "mirror_off_system_callback_turn_delivered",
			mirrorWeb:   false,
			hasAttempt:  true,
			origin:      store.AttemptTurnOrigin{Source: domain.TurnSourceSystem, HasOrigin: false},
			foundOrigin: true,
			want:        true,
		},
		{
			name:        "mirror_off_schedule_proactive_turn_delivered",
			mirrorWeb:   false,
			hasAttempt:  true,
			origin:      store.AttemptTurnOrigin{Source: domain.TurnSourceSchedule, HasOrigin: false},
			foundOrigin: true,
			want:        true,
		},
		{
			name:        "mirror_off_trigger_turn_delivered",
			mirrorWeb:   false,
			hasAttempt:  true,
			origin:      store.AttemptTurnOrigin{Source: domain.TurnSourceTrigger, HasOrigin: false},
			foundOrigin: true,
			want:        true,
		},
		{
			name:        "mirror_off_no_attempt_id_system_message_delivered",
			mirrorWeb:   false,
			hasAttempt:  false,
			foundOrigin: false,
			want:        true,
		},
		{
			name:        "mirror_on_web_human_turn_delivered",
			mirrorWeb:   true,
			hasAttempt:  true,
			origin:      store.AttemptTurnOrigin{Source: domain.TurnSourceHuman, HasOrigin: false},
			foundOrigin: true,
			want:        true,
		},
		{
			name:        "mirror_on_other_channel_human_turn_delivered",
			mirrorWeb:   true,
			hasAttempt:  true,
			origin:      store.AttemptTurnOrigin{Source: domain.TurnSourceHuman, OriginChannelID: chB, HasOrigin: true},
			foundOrigin: true,
			want:        true,
		},
		{
			name:        "mirror_off_missing_attempt_row_blocked",
			mirrorWeb:   false,
			hasAttempt:  true,
			foundOrigin: false,
			want:        false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldDeliverMainChatMessage(chA, tc.mirrorWeb, tc.hasAttempt, tc.origin, tc.foundOrigin)
			if got != tc.want {
				t.Fatalf("shouldDeliverMainChatMessage() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSupervisorCallbackTargetsMainChat(t *testing.T) {
	mainID := uuid.New()
	otherDiscID := uuid.New()

	cases := []struct {
		name string
		card cardSummary
		want bool
	}{
		{
			name: "todo_without_source_discussion_falls_back_to_active_main_chat",
			card: cardSummary{
				Kind:                 domain.CardKindTodo,
				MainDiscussionID:     uuid.NullUUID{UUID: mainID, Valid: true},
				MainDiscussionActive: true,
			},
			want: true,
		},
		{
			name: "todo_sourced_from_main_chat",
			card: cardSummary{
				Kind:                   domain.CardKindTodo,
				MainDiscussionID:       uuid.NullUUID{UUID: mainID, Valid: true},
				MainDiscussionActive:   true,
				SourceDiscussionID:     uuid.NullUUID{UUID: mainID, Valid: true},
				SourceDiscussionActive: true,
			},
			want: true,
		},
		{
			name: "todo_sourced_from_archived_other_discussion_falls_back_to_main_chat",
			card: cardSummary{
				Kind:                   domain.CardKindTodo,
				MainDiscussionID:       uuid.NullUUID{UUID: mainID, Valid: true},
				MainDiscussionActive:   true,
				SourceDiscussionID:     uuid.NullUUID{UUID: otherDiscID, Valid: true},
				SourceDiscussionActive: false,
			},
			want: true,
		},
		{
			name: "todo_sourced_from_active_other_discussion_keeps_template_reminder",
			card: cardSummary{
				Kind:                   domain.CardKindTodo,
				MainDiscussionID:       uuid.NullUUID{UUID: mainID, Valid: true},
				MainDiscussionActive:   true,
				SourceDiscussionID:     uuid.NullUUID{UUID: otherDiscID, Valid: true},
				SourceDiscussionActive: true,
			},
			want: false,
		},
		{
			name: "todo_in_project_without_active_main_chat_keeps_template_reminder",
			card: cardSummary{
				Kind:                 domain.CardKindTodo,
				MainDiscussionID:     uuid.NullUUID{UUID: mainID, Valid: true},
				MainDiscussionActive: false,
			},
			want: false,
		},
		{
			name: "discussion_card_never_triggers_supervisor_callback",
			card: cardSummary{
				Kind:                 domain.CardKindDiscussion,
				MainDiscussionID:     uuid.NullUUID{UUID: mainID, Valid: true},
				MainDiscussionActive: true,
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := supervisorCallbackTargetsMainChat(tc.card); got != tc.want {
				t.Fatalf("supervisorCallbackTargetsMainChat() = %v, want %v", got, tc.want)
			}
		})
	}
}
