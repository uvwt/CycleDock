package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestBuildPushPayload_CardRouteURLs(t *testing.T) {
	workspaceID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	projectID := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	cases := []struct {
		name             string
		kind             string
		cardID           string
		notificationKind string
		wantURL          string
	}{
		{
			name:             "todo card message",
			kind:             "todo",
			cardID:           "33333333-3333-3333-3333-333333333331",
			notificationKind: "message",
			wantURL:          "/cards/" + projectID.String() + "/33333333-3333-3333-3333-333333333331",
		},
		{
			name:             "discussion card message",
			kind:             "discussion",
			cardID:           "33333333-3333-3333-3333-333333333332",
			notificationKind: "message",
			wantURL:          "/chat/" + projectID.String() + "/33333333-3333-3333-3333-333333333332",
		},
		{
			name:             "project main discussion message",
			kind:             "discussion",
			cardID:           "33333333-3333-3333-3333-333333333333",
			notificationKind: "message",
			wantURL:          "/chat/" + projectID.String() + "/33333333-3333-3333-3333-333333333333",
		},
		{
			name:             "ongoing card message",
			kind:             "ongoing",
			cardID:           "33333333-3333-3333-3333-333333333334",
			notificationKind: "message",
			wantURL:          "/plans/" + projectID.String() + "/33333333-3333-3333-3333-333333333334",
		},
		{
			name:             "todo pending tool approval",
			kind:             "todo",
			cardID:           "33333333-3333-3333-3333-333333333335",
			notificationKind: "tool_approval",
			wantURL:          "/cards/" + projectID.String() + "/33333333-3333-3333-3333-333333333335",
		},
		{
			name:             "discussion pending tool approval",
			kind:             "discussion",
			cardID:           "33333333-3333-3333-3333-333333333336",
			notificationKind: "tool_approval",
			wantURL:          "/chat/" + projectID.String() + "/33333333-3333-3333-3333-333333333336",
		},
		{
			name:             "ongoing pending tool approval",
			kind:             "ongoing",
			cardID:           "33333333-3333-3333-3333-333333333337",
			notificationKind: "tool_approval",
			wantURL:          "/plans/" + projectID.String() + "/33333333-3333-3333-3333-333333333337",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := buildPushPayload(workspaceID, cardUpdate{
				ID:               tc.cardID,
				ProjectID:        projectID.String(),
				Kind:             tc.kind,
				Title:            "测试卡片",
				Revision:         "rev-1",
				EventSequence:    "2",
				MessagePreview:   "有新的卡片更新",
				NotificationKind: tc.notificationKind,
			})
			if err != nil {
				t.Fatalf("buildPushPayload failed: %v", err)
			}
			var got pushPayload
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			if got.URL != tc.wantURL {
				t.Fatalf("URL = %q, want %q", got.URL, tc.wantURL)
			}
			if got.URL == "/" {
				t.Fatalf("URL must never fall back to root path")
			}
		})
	}
}
