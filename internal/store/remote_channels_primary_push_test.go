package store

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestResolveProjectPrimaryPushChannel(t *testing.T) {
	wsID := uuid.New()
	projID := uuid.New()
	otherProjID := uuid.New()

	t1 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 20, 11, 0, 0, 0, time.UTC)

	chA := RemoteChannel{
		ID:            uuid.New(),
		WorkspaceID:   wsID,
		ProjectID:     projID,
		Name:          "Telegram",
		Platform:      domain.ChannelPlatformTelegram,
		Role:          domain.ChannelRoleMainChat,
		Transport:     domain.ChannelTransportLongPoll,
		Enabled:       true,
		Config:        RemoteChannelConfig{TargetChatID: "tg-1"},
		LastInboundAt: &t1,
	}
	chB := RemoteChannel{
		ID:            uuid.New(),
		WorkspaceID:   wsID,
		ProjectID:     projID,
		Name:          "Feishu",
		Platform:      domain.ChannelPlatformFeishu,
		Role:          domain.ChannelRoleMainChat,
		Transport:     domain.ChannelTransportWebSocket,
		Enabled:       true,
		Config:        RemoteChannelConfig{TargetChatID: "fs-1"},
		LastInboundAt: &t2,
	}
	chNoInbound := RemoteChannel{
		ID:          uuid.New(),
		WorkspaceID: wsID,
		ProjectID:   projID,
		Name:        "Weixin",
		Platform:    domain.ChannelPlatformWeixin,
		Role:        domain.ChannelRoleMainChat,
		Transport:   domain.ChannelTransportLongPoll,
		Enabled:     true,
		Config:      RemoteChannelConfig{WeixinUserID: "wx-1"},
	}
	chDisabled := RemoteChannel{
		ID:            uuid.New(),
		WorkspaceID:   wsID,
		ProjectID:     projID,
		Name:          "Disabled",
		Platform:      domain.ChannelPlatformDingTalk,
		Role:          domain.ChannelRoleMainChat,
		Transport:     domain.ChannelTransportStream,
		Enabled:       false,
		Config:        RemoteChannelConfig{TargetChatID: "dt-1"},
		LastInboundAt: &t2,
	}
	chSched := RemoteChannel{
		ID:            uuid.New(),
		WorkspaceID:   wsID,
		ProjectID:     projID,
		Name:          "Scheduled",
		Platform:      domain.ChannelPlatformDingTalk,
		Role:          domain.ChannelRoleScheduledPush,
		Transport:     domain.ChannelTransportWebhook,
		Enabled:       true,
		LastInboundAt: &t2,
	}
	chOtherProj := RemoteChannel{
		ID:            uuid.New(),
		WorkspaceID:   wsID,
		ProjectID:     otherProjID,
		Name:          "OtherProject",
		Platform:      domain.ChannelPlatformTelegram,
		Role:          domain.ChannelRoleMainChat,
		Transport:     domain.ChannelTransportLongPoll,
		Enabled:       true,
		Config:        RemoteChannelConfig{TargetChatID: "tg-other"},
		LastInboundAt: &t2,
	}

	all := []RemoteChannel{chA, chB, chNoInbound, chDisabled, chSched, chOtherProj}

	t.Run("last_active picks most recent enabled main_chat with inbound", func(t *testing.T) {
		got, ok := ResolveProjectPrimaryPushChannel(ProjectPrimaryPushRoute{
			WorkspaceID: wsID,
			ProjectID:   projID,
			Mode:        domain.PrimaryPushModeLastActive,
		}, all)
		if !ok || got.ID != chB.ID {
			t.Fatalf("expected chB (%s), got ok=%v id=%s", chB.ID, ok, got.ID)
		}
	})

	t.Run("last_active returns false when no channel has inbound", func(t *testing.T) {
		_, ok := ResolveProjectPrimaryPushChannel(ProjectPrimaryPushRoute{
			WorkspaceID: wsID,
			ProjectID:   projID,
			Mode:        domain.PrimaryPushModeLastActive,
		}, []RemoteChannel{chNoInbound, chSched})
		if ok {
			t.Fatalf("expected no effective channel when none has inbound")
		}
	})

	t.Run("channel mode picks explicit enabled main_chat even without inbound", func(t *testing.T) {
		got, ok := ResolveProjectPrimaryPushChannel(ProjectPrimaryPushRoute{
			WorkspaceID: wsID,
			ProjectID:   projID,
			Mode:        domain.PrimaryPushModeChannel,
			ChannelID:   uuid.NullUUID{UUID: chNoInbound.ID, Valid: true},
		}, all)
		if !ok || got.ID != chNoInbound.ID {
			t.Fatalf("expected explicit chNoInbound (%s), got ok=%v id=%s", chNoInbound.ID, ok, got.ID)
		}
	})

	t.Run("channel mode safely returns false when target disabled or wrong role or missing", func(t *testing.T) {
		for _, target := range []uuid.NullUUID{
			{UUID: chDisabled.ID, Valid: true},
			{UUID: chSched.ID, Valid: true},
			{UUID: chOtherProj.ID, Valid: true},
			{UUID: uuid.New(), Valid: true},
			{Valid: false},
		} {
			if got, ok := ResolveProjectPrimaryPushChannel(ProjectPrimaryPushRoute{
				WorkspaceID: wsID,
				ProjectID:   projID,
				Mode:        domain.PrimaryPushModeChannel,
				ChannelID:   target,
			}, all); ok {
				t.Fatalf("expected false for target=%+v, got %s", target, got.ID)
			}
		}
	})

	t.Run("none mode always returns false", func(t *testing.T) {
		if got, ok := ResolveProjectPrimaryPushChannel(ProjectPrimaryPushRoute{
			WorkspaceID: wsID,
			ProjectID:   projID,
			Mode:        domain.PrimaryPushModeNone,
		}, all); ok {
			t.Fatalf("expected false in none mode, got %s", got.ID)
		}
	})
}
