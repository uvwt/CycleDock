package httpapi

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uvwt/CycleDock/skills"
)

func TestAIConnectGuideUsesCurrentInstanceAndTurnAttemptTools(t *testing.T) {
	response := httptest.NewRecorder()
	serveAIConnectGuide(response, "https://cycle.example/", "https://cycle.example/mcp")

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, expected := range []string{
		"https://cycle.example/docs/ai-connect/",
		"https://cycle.example/mcp",
		"https://cycle.example/skills/cycledock/SKILL.md",
		"list_projects", "list_cards", "create_turn", "claim_attempt", "heartbeat_attempt",
		"report_progress", "add_finding", "check_in", "finish_attempt", "confirm_card",
		"原生 Remote MCP", "OAuth",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("guide does not contain %q", expected)
		}
	}
	for _, forbidden := range []string{
		"board_id", "add_event", "create_followup", "claim_work", "heartbeat_run", "finish_run",
		"CycleDock 不运行 AI", "CycleDock 不提供 Scheduler",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("guide still contains obsolete text %q", forbidden)
		}
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("Content-Type: got %q", contentType)
	}
}

func TestCycleDockSkillRawAndZIPUseEmbeddedSources(t *testing.T) {
	api := &API{}
	raw := httptest.NewRecorder()
	api.cycleDockSkillSource(raw, httptest.NewRequest(http.MethodGet, "/skills/cycledock/SKILL.md", nil))
	if raw.Code != http.StatusOK || raw.Body.String() != skills.CycleDockSkillMarkdown {
		t.Fatal("raw Skill does not match embedded source")
	}
	if !strings.Contains(raw.Body.String(), "create_turn") ||
		!strings.Contains(raw.Body.String(), "claim_attempt") ||
		!strings.Contains(raw.Body.String(), "finish_attempt") {
		t.Fatal("Skill does not describe Turn/Attempt work semantics")
	}
	for _, obsolete := range []string{"BoardID", "add_event", "create_followup", "claim_work", "heartbeat_run", "finish_run"} {
		if strings.Contains(raw.Body.String(), obsolete) {
			t.Fatalf("Skill contains obsolete tool/domain semantics %q", obsolete)
		}
	}

	archive := httptest.NewRecorder()
	api.cycleDockSkillZIP(archive, httptest.NewRequest(http.MethodGet, "/skills/cycledock.zip", nil))
	if archive.Code != http.StatusOK {
		t.Fatalf("Skill ZIP status: got %d", archive.Code)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Body.Bytes()), int64(archive.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 2 {
		t.Fatalf("Skill ZIP entries=%d want 2", len(reader.File))
	}
	expected := map[string]string{
		"cycledock/SKILL.md":            skills.CycleDockSkillMarkdown,
		"cycledock/references/tools.md": skills.CycleDockToolsReferenceMarkdown,
	}
	for _, entry := range reader.File {
		want, ok := expected[entry.Name]
		if !ok {
			t.Fatalf("unexpected Skill ZIP entry %q", entry.Name)
		}
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("Skill ZIP entry %q differs from embedded source", entry.Name)
		}
	}
}

func TestSafeInternalReturnTo(t *testing.T) {
	for _, valid := range []string{"/oauth/authorize?client_id=x", "/projects/abc"} {
		if got := safeInternalReturnTo(valid); got != valid {
			t.Fatalf("safe return %q => %q", valid, got)
		}
	}
	for _, invalid := range []string{"", "https://evil.example", "//evil.example/x", "/\\evil", "javascript:alert(1)"} {
		if got := safeInternalReturnTo(invalid); got != "/" {
			t.Fatalf("unsafe return %q => %q", invalid, got)
		}
	}
}
