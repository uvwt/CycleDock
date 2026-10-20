package agent

import (
	"testing"

	"github.com/uvwt/CycleDock/internal/modelclient"
)

func TestMergeToolDefinitionsRejectsNameCollision(t *testing.T) {
	internal := []modelclient.ToolDefinition{{
		Type:     "function",
		Function: modelclient.ToolDefinitionFunc{Name: "cycledock_confirm_card"},
	}}
	external := []modelclient.ToolDefinition{{
		Type:     "function",
		Function: modelclient.ToolDefinitionFunc{Name: "cycledock_confirm_card"},
	}}

	if _, err := mergeToolDefinitions(internal, external); err == nil {
		t.Fatal("expected duplicate tool name to be rejected")
	}
}

func TestMergeToolDefinitionsKeepsDistinctNativeTools(t *testing.T) {
	internal := []modelclient.ToolDefinition{{
		Type:     "function",
		Function: modelclient.ToolDefinitionFunc{Name: "cycledock_confirm_card"},
	}}
	external := []modelclient.ToolDefinition{{
		Type:     "function",
		Function: modelclient.ToolDefinitionFunc{Name: "exec_command"},
	}}

	merged, err := mergeToolDefinitions(internal, external)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 2 || merged[1].Function.Name != "exec_command" {
		t.Fatalf("merged=%+v", merged)
	}
}
