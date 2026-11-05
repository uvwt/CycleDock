package agentartifact

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentsession"
)

var artifactIntegrationPool *pgxpool.Pool

func TestMain(m *testing.M) {
	os.Exit(runArtifactTests(m))
}

func runArtifactTests(m *testing.M) int {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		return m.Run()
	}

	adminPool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		panic(err)
	}
	defer adminPool.Close()
	// 集成测试必须在独立临时 schema 中执行迁移，避免修改已有开发库表。
	schema := "agentartifact_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(context.Background(), "CREATE SCHEMA "+quotedSchema); err != nil {
		panic(err)
	}
	defer func() {
		if _, err := adminPool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			panic(err)
		}
	}()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		panic(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	sqlDB := stdlib.OpenDB(*config.ConnConfig)
	defer sqlDB.Close()

	_, filename, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		panic(err)
	}
	if err := goose.Up(sqlDB, migrations); err != nil {
		panic(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		panic(err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		panic(err)
	}
	defer pool.Close()
	artifactIntegrationPool = pool

	return m.Run()
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

	// 大段工具输出被外置为 Artifact 时，若包含 NUL 或异常 Unicode 字符也必须能成功写入 PostgreSQL jsonb。
	abnormalPayload := []byte(`{"stdout":"artifact\u0000line\n\t中文🚀","stderr":"ok"}`)
	savedAbnormal, err := service.Save(ctx, &artifact.SaveRequest{
		AppName:        appName,
		UserID:         userID,
		SessionID:      sessionID,
		FileName:       "abnormal-tool-output.json",
		Part:           genai.NewPartFromText("artifact text\x00with nul and 中文🚀"),
		CustomMetadata: map[string]any{"tool\x00name": "exec\x00command", "preview": string(abnormalPayload)},
	})
	if err != nil {
		t.Fatalf("save artifact with NUL characters: %v", err)
	}
	loadedAbnormal, err := service.Load(ctx, &artifact.LoadRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
		FileName: "abnormal-tool-output.json", Version: savedAbnormal.Version,
	})
	if err != nil {
		t.Fatalf("load artifact with NUL characters: %v", err)
	}
	if loadedAbnormal.Part.Text != "artifact text\uFFFDwith nul and 中文🚀" {
		t.Fatalf("loaded abnormal artifact text=%q", loadedAbnormal.Part.Text)
	}
}
