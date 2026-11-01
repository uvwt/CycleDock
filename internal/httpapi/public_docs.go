package httpapi

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"html/template"
	"net/http"
	"strings"

	"github.com/uvwt/CycleDock/skills"
)

//go:embed templates/ai-connect.html
var aiConnectHTML string

var aiConnectTemplate = template.Must(template.New("ai-connect").Parse(aiConnectHTML))

type aiConnectData struct {
	GuideURL string
	MCPURL   string
	SkillURL string
	SkillZIP string
	Prompt   string
}

func (api *API) aiConnectGuide(w http.ResponseWriter, r *http.Request) {
	if api.oauth == nil {
		http.NotFound(w, r)
		return
	}
	serveAIConnectGuide(w, api.oauth.Issuer(), api.oauth.Resource())
}

func serveAIConnectGuide(w http.ResponseWriter, issuer, resource string) {
	issuer = strings.TrimRight(issuer, "/")
	guideURL := issuer + "/docs/ai-connect/"
	data := aiConnectData{
		GuideURL: guideURL,
		MCPURL:   resource,
		SkillURL: issuer + "/skills/cycledock/SKILL.md",
		SkillZIP: issuer + "/skills/cycledock.zip",
	}
	data.Prompt = "请阅读 " + guideURL + "，按照官方指南帮我接入 CycleDock。尽可能使用当前客户端原生的 Skill、MCP 和 OAuth 能力自动完成；只有登录、授权确认或客户端不允许 AI 修改连接设置时再让我操作。接入后请确认 CycleDock 工具真实注入当前客户端，并实际调用 list_projects 和 list_cards 验证。"

	var body bytes.Buffer
	if err := aiConnectTemplate.Execute(&body, data); err != nil {
		http.Error(w, "render AI connection guide", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

func (api *API) cycleDockSkillSource(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(skills.CycleDockSkillMarkdown))
}

func (api *API) cycleDockSkillZIP(w http.ResponseWriter, _ *http.Request) {
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	files := []struct {
		name    string
		content string
	}{
		{name: "cycledock/SKILL.md", content: skills.CycleDockSkillMarkdown},
		{name: "cycledock/references/tools.md", content: skills.CycleDockToolsReferenceMarkdown},
	}
	for _, item := range files {
		file, err := writer.Create(item.name)
		if err != nil {
			http.Error(w, "create Skill archive", http.StatusInternalServerError)
			return
		}
		if _, err := file.Write([]byte(item.content)); err != nil {
			http.Error(w, "write Skill archive", http.StatusInternalServerError)
			return
		}
	}
	if err := writer.Close(); err != nil {
		http.Error(w, "finish Skill archive", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"cycledock.zip\"")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}
