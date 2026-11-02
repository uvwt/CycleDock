package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestProjectInstructionsMigrationPreservesContent(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, "Project description migration")
	if err != nil {
		t.Fatal(err)
	}
	database := stdlib.OpenDB(*s.pool.Config().ConnConfig)
	defer database.Close()
	const migrations = "../../db/migrations"
	defer func() {
		if err := goose.Up(database, migrations); err != nil {
			t.Errorf("restore latest schema: %v", err)
		}
	}()
	if err := goose.DownTo(database, migrations, 23); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ description, instructions, want string }{
		{"项目背景", "工作要求", "项目背景\n\n工作要求"},
		{"", "工作要求", "工作要求"},
		{"项目背景", "", "项目背景"},
		{"项目背景", "  ", "项目背景"},
		{"相同内容", "相同内容", "相同内容"},
	}
	ids := make([]uuid.UUID, len(cases))
	for i, tc := range cases {
		ids[i] = uuid.New()
		if _, err := s.pool.Exec(ctx, `INSERT INTO projects (id, workspace_id, name, description, instructions) VALUES ($1,$2,'migration',$3,$4)`, ids[i], workspace.ID, tc.description, tc.instructions); err != nil {
			t.Fatal(err)
		}
	}
	if err := goose.Up(database, migrations); err != nil {
		t.Fatal(err)
	}
	for i, tc := range cases {
		project, err := s.GetProject(ctx, workspace.ID, ids[i])
		if err != nil || project.Description != tc.want {
			t.Errorf("case %d: description=%q want=%q err=%v", i, project.Description, tc.want, err)
		}
	}
}
