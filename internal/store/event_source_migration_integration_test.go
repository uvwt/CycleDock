package store

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestEventSourceMigrationReconcilesVersion18Collision(t *testing.T) {
	for _, scenario := range []struct {
		name             string
		legacySource     bool
		legacyConstraint bool
	}{
		{name: "normal_upgrade"},
		{name: "legacy_version18", legacySource: true, legacyConstraint: true},
		{name: "existing_column_without_constraint", legacySource: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := requireIntegration(t)
			f := newFixture(t, s, domain.CardKindTodo)
			database := stdlib.OpenDB(*s.pool.Config().ConnConfig)
			defer database.Close()
			const migrations = "../../db/migrations"
			defer func() {
				if err := goose.Up(database, migrations); err != nil {
					t.Errorf("restore latest schema: %v", err)
				}
			}()
			if err := goose.DownTo(database, migrations, 17); err != nil {
				t.Fatal(err)
			}

			var eventID uuid.UUID
			if scenario.legacySource {
				// 还原合并前的版本 18：已记录迁移，只有来源字段，没有优先模型字段。
				if _, err := database.Exec(`
					ALTER TABLE events ADD COLUMN source_card_id uuid;
					INSERT INTO goose_db_version (version_id, is_applied) VALUES (18, true);
				`); err != nil {
					t.Fatal(err)
				}
				if scenario.legacyConstraint {
					if _, err := database.Exec(`
						ALTER TABLE events ADD CONSTRAINT events_source_card_fk
						FOREIGN KEY (workspace_id, source_card_id)
						REFERENCES cards(workspace_id, id) ON DELETE SET NULL
					`); err != nil {
						t.Fatal(err)
					}
				}
				eventID = uuid.New()
				if _, err := database.Exec(`
					INSERT INTO events (id, workspace_id, card_id, event_type, actor_source_id, content, source_card_id)
					VALUES ($1, $2, $3, 'comment', $4, 'existing source evidence', $3)
				`, eventID, f.WorkspaceID, f.CardID, f.HumanID); err != nil {
					t.Fatal(err)
				}
			}

			if err := goose.Up(database, migrations); err != nil {
				t.Fatal(err)
			}
			var routeColumns, routeConstraints, sourceConstraints int
			if err := database.QueryRow(`
				SELECT count(*) FROM pg_attribute
				WHERE attrelid = 'card_turns'::regclass
				  AND attname = 'preferred_model_route_id' AND atttypid = 'uuid'::regtype
				  AND NOT attisdropped
			`).Scan(&routeColumns); err != nil || routeColumns != 1 {
				t.Fatalf("preferred model route column count=%d err=%v", routeColumns, err)
			}
			if err := database.QueryRow(`
				SELECT count(*) FROM pg_constraint
				WHERE conrelid = 'card_turns'::regclass AND contype = 'f'
				  AND confrelid = 'workspace_model_routes'::regclass
				  AND conkey = ARRAY[(SELECT attnum FROM pg_attribute
				      WHERE attrelid = 'card_turns'::regclass AND attname = 'preferred_model_route_id')]
			`).Scan(&routeConstraints); err != nil || routeConstraints != 1 {
				t.Fatalf("preferred model route foreign key count=%d err=%v", routeConstraints, err)
			}
			if err := database.QueryRow(`
				SELECT count(*) FROM pg_constraint
				WHERE conrelid = 'events'::regclass AND conname = 'events_source_card_fk'
				  AND contype = 'f' AND confrelid = 'cards'::regclass
			`).Scan(&sourceConstraints); err != nil || sourceConstraints != 1 {
				t.Fatalf("source card foreign key count=%d err=%v", sourceConstraints, err)
			}
			if scenario.legacySource {
				var sourceID uuid.UUID
				if err := database.QueryRow("SELECT source_card_id FROM events WHERE id=$1", eventID).Scan(&sourceID); err != nil || sourceID != f.CardID {
					t.Fatalf("preserved source card=%s err=%v", sourceID, err)
				}
			}

			// 验证新迁移可以回退后再次应用，不删除由版本 18 管理的模型路由字段。
			if err := goose.DownTo(database, migrations, 20); err != nil {
				t.Fatal(err)
			}
			if err := goose.Up(database, migrations); err != nil {
				t.Fatal(err)
			}
		})
	}
}
