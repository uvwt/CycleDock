package store

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/domain"
)

func TestGeneratedDiscussionMetadataPreservesEditsAndWorkspaceScope(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	for _, test := range []struct{ name, title, description string }{
		{"title edited", "Human title", ""},
		{"description edited", "", "Human description"},
		{"both edited", "Human title", "Human description"},
	} {
		t.Run(test.name, func(t *testing.T) {
			card, err := s.CreateDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.UpdateCardText(ctx, f.WorkspaceID, card.ID, test.title, test.description); err != nil {
				t.Fatal(err)
			}
			updated, err := s.SaveGeneratedDiscussionMetadata(ctx, f.WorkspaceID, card.ID, "Generated", "Generated description")
			if err != nil || updated {
				t.Fatalf("overwrote human edit: updated=%v err=%v", updated, err)
			}
			got, err := s.GetCard(ctx, f.WorkspaceID, card.ID)
			if err != nil || got.Title != test.title || got.Description != test.description {
				t.Fatalf("human content changed: card=%+v err=%v", got, err)
			}
		})
	}
	card, err := s.CreateDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil {
		t.Fatal(err)
	}
	if updated, err := s.SaveGeneratedDiscussionMetadata(ctx, uuid.New(), card.ID, "Wrong workspace", "Description"); err != nil || updated {
		t.Fatalf("cross-workspace update: updated=%v err=%v", updated, err)
	}
	// 模拟多个实例同时完成生成，数据库只能接受一次更新。
	var workers sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			updated, err := s.SaveGeneratedDiscussionMetadata(ctx, f.WorkspaceID, card.ID, "Generated", "Description")
			if err != nil {
				t.Error(err)
			}
			results <- updated
		}()
	}
	workers.Wait()
	close(results)
	count := 0
	for updated := range results {
		if updated {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("accepted %d concurrent updates, want 1", count)
	}
}
