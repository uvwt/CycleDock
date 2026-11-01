package modelclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *Client) ListModels(ctx context.Context, config Config) ([]ModelInfo, error) {
	if strings.TrimSpace(config.BaseURL) == "" {
		return nil, errors.New("model base URL is required")
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodGet,
		strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")+"/models",
		nil,
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	for name, value := range config.Headers {
		if strings.TrimSpace(name) != "" && strings.TrimSpace(value) != "" {
			request.Header.Set(name, value)
		}
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list models request: %w", err)
	}
	defer response.Body.Close()

	body, err := readBounded(response.Body, response.ContentLength, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &APIError{StatusCode: response.StatusCode, Body: summarizeBody(body)}
	}

	var decoded struct {
		Models []struct {
			Slug           string `json:"slug"`
			DisplayName    string `json:"display_name"`
			Visibility     string `json:"visibility"`
			SupportedInAPI *bool  `json:"supported_in_api"`
		} `json:"models"`
		Data []struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Name  string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("%w: invalid models response", ErrMalformedResponse)
	}

	models := make([]ModelInfo, 0, len(decoded.Models)+len(decoded.Data))
	for _, model := range decoded.Models {
		if model.Visibility != "" && model.Visibility != "list" {
			continue
		}
		if model.SupportedInAPI != nil && !*model.SupportedInAPI {
			continue
		}
		id := strings.TrimSpace(model.Slug)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(model.DisplayName)
		if name == "" {
			name = id
		}
		models = append(models, ModelInfo{ID: id, Name: name})
	}
	for _, model := range decoded.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			id = strings.TrimSpace(model.Model)
		}
		if id == "" {
			continue
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = id
		}
		models = append(models, ModelInfo{ID: id, Name: name})
	}
	if len(models) == 0 {
		return nil, ErrEmptyResponse
	}
	return models, nil
}
