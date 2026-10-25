package agentartifact

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentsession"
)

var artifactIntegrationPool *pgxpool.Pool

func TestMain(m *testing.M) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		os.Exit(m.Run())
	}
	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		panic(err)
	}
	_, filename, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		panic(err)
	}
	if err := goose.Up(sqlDB, migrations); err != nil {
		panic(err)
	}
	artifactIntegrationPool, err = pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		panic(err)
	}
	code := m.Run()
	artifactIntegrationPool.Close()
	_ = sqlDB.Close()
	os.Exit(code)
}

func TestPostgresArtifactServiceVersionsAndIsolation(t *testing.T) {
	if artifactIntegrationPool == nil {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	workspaceID := uuid.New()
	cardID := uuid.New()
	appName, userID, sessionID := agentsession.Identity(workspaceID, cardID)

	sessionService := agentsession.NewPostgresService(artifactIntegrationPool)
	if _, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewPostgresService(artifactIntegrationPool)

	first, err := service.Save(ctx, &artifact.SaveRequest{
		AppName: appName, UserID: userID, SessionID: sessionID, FileName: "evidence-result.json",
		Part:           genai.NewPartFromBytes([]byte(`{"value":"first"}`), "application/json"),
		CustomMetadata: map[string]any{"tool": "inventory", "count": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Save(ctx, &artifact.SaveRequest{
		AppName: appName, UserID: userID, SessionID: sessionID, FileName: "evidence-result.json",
		Part: genai.NewPartFromBytes([]byte(`{"value":"second"}`), "application/json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || second.Version != 2 {
		t.Fatalf("versions first=%d second=%d", first.Version, second.Version)
	}

	latest, err := service.Load(ctx, &artifact.LoadRequest{
		AppName: appName, UserID: userID, SessionID: sessionID, FileName: "evidence-result.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(latest.Part.InlineData.Data) != `{"value":"second"}` {
		t.Fatalf("latest=%q", latest.Part.InlineData.Data)
	}
	exact, err := service.Load(ctx, &artifact.LoadRequest{
		AppName: appName, UserID: userID, SessionID: sessionID, FileName: "evidence-result.json", Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(exact.Part.InlineData.Data) != `{"value":"first"}` {
		t.Fatalf("version 1=%q", exact.Part.InlineData.Data)
	}

	versions, err := service.Versions(ctx, &artifact.VersionsRequest{
		AppName: appName, UserID: userID, SessionID: sessionID, FileName: "evidence-result.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(versions.Versions) != "[1 2]" {
		t.Fatalf("versions=%v", versions.Versions)
	}
	meta, err := service.GetArtifactVersion(ctx, &artifact.GetArtifactVersionRequest{
		AppName: appName, UserID: userID, SessionID: sessionID, FileName: "evidence-result.json", Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if meta.ArtifactVersion.CustomMetadata["count"] != "1" ||
		meta.ArtifactVersion.MimeType != "application/json" {
		t.Fatalf("metadata=%+v", meta.ArtifactVersion)
	}

	// Same file saved concurrently must receive unique monotonically allocated versions.
	const writers = 6
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	gotVersions := make(chan int64, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			saved, err := service.Save(ctx, &artifact.SaveRequest{
				AppName: appName, UserID: userID, SessionID: sessionID, FileName: "evidence-result.json",
				Part: genai.NewPartFromText(fmt.Sprintf("writer-%d", i)),
			})
			if err != nil {
				errs <- err
				return
			}
			gotVersions <- saved.Version
		}(i)
	}
	wg.Wait()
	close(errs)
	close(gotVersions)
	for err := range errs {
		t.Fatal(err)
	}
	var concurrent []int
	for version := range gotVersions {
		concurrent = append(concurrent, int(version))
	}
	sort.Ints(concurrent)
	if fmt.Sprint(concurrent) != "[3 4 5 6 7 8]" {
		t.Fatalf("concurrent versions=%v", concurrent)
	}

	otherCard := uuid.New()
	_, otherUser, otherSession := agentsession.Identity(workspaceID, otherCard)
	if _, err := service.Load(ctx, &artifact.LoadRequest{
		AppName: appName, UserID: otherUser, SessionID: otherSession, FileName: "evidence-result.json",
	}); err == nil {
		t.Fatal("cross-card artifact load unexpectedly succeeded")
	}
}
