package modelclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestOutputSchemaWireFormats(t *testing.T) {
	for _, transport := range []Transport{TransportChatCompletions, TransportResponses} {
		for _, structured := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/schema=%v", transport, structured), func(t *testing.T) {
				schema := map[string]any{
					"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}},
					"required": []any{"title"}, "additionalProperties": false,
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if !structured {
						if body["response_format"] != nil || body["text"] != nil {
							t.Errorf("ordinary request contains output format: %+v", body)
						}
					} else {
						format := map[string]any{"name": "test_output", "strict": true, "schema": schema}
						if transport == TransportResponses {
							format["type"] = "json_schema"
							if !reflect.DeepEqual(body["text"], map[string]any{"format": format}) || body["response_format"] != nil {
								t.Errorf("incorrect Responses text.format: %+v", body)
							}
						} else if !reflect.DeepEqual(body["response_format"], map[string]any{"type": "json_schema", "json_schema": format}) || body["text"] != nil {
							t.Errorf("incorrect Chat Completions response_format: %+v", body)
						}
					}
					if transport == TransportResponses {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintln(w, `data: {"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"{\"title\":\"Test\"}"}]}]}}`)
						fmt.Fprintln(w)
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"title\":\"Test\"}"}}]}`)
					}
				}))
				defer server.Close()
				config := Config{BaseURL: server.URL, Model: "test", Transport: transport}
				if structured {
					config.OutputSchema = &OutputSchema{Name: "test_output", Schema: schema, Strict: true}
				}
				completion, err := New().Complete(context.Background(), config, []Message{{Role: "user", Content: "Generate a title"}}, nil)
				if err != nil || completion.Content != `{"title":"Test"}` {
					t.Fatalf("unexpected completion: %+v err=%v", completion, err)
				}
			})
		}
	}
}
