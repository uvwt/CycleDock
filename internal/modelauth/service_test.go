package modelauth

import "testing"

func TestAllowsDynamicChatGPTRegistration(t *testing.T) {
	tests := []struct {
		name        string
		publicURL   string
		allowHosted bool
		want        bool
	}{
		{name: "loopback remains allowed", publicURL: "http://127.0.0.1:8080", want: true},
		{name: "hosted defaults to denied", publicURL: "https://cycledock.example.com", want: false},
		{name: "hosted explicit test override", publicURL: "https://cycledock.example.com", allowHosted: true, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{
				publicURL:                             test.publicURL,
				allowHostedChatGPTDynamicRegistration: test.allowHosted,
			}
			if got := service.allowsDynamicChatGPTRegistration(); got != test.want {
				t.Fatalf("allowsDynamicChatGPTRegistration() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestChatGPTAuthorizationConfig(t *testing.T) {
	tests := []struct {
		name         string
		publicURL    string
		clientID     string
		allowHosted  bool
		wantClientID string
		wantRedirect string
		wantManual   bool
		wantHostID   bool
		wantError    bool
	}{
		{
			name:         "loopback dynamic registration",
			publicURL:    "http://127.0.0.1:8080",
			wantClientID: chatGPTDynamicID,
			wantRedirect: "http://127.0.0.1:8080/auth/model/chatgpt/callback",
		},
		{
			name:      "hosted requires approved client by default",
			publicURL: "https://cycledock.example.com",
			wantError: true,
		},
		{
			name:         "hosted explicit test override uses manual loopback callback",
			publicURL:    "https://cycledock.example.com",
			allowHosted:  true,
			wantClientID: chatGPTDynamicID,
			wantRedirect: chatGPTHostedTestRedirectURI,
			wantManual:   true,
		},
		{
			name:         "approved hosted client keeps public callback",
			publicURL:    "https://cycledock.example.com",
			clientID:     "approved-client",
			wantClientID: "approved-client",
			wantRedirect: "https://cycledock.example.com/auth/model/chatgpt/callback",
			wantHostID:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{
				publicURL:                             test.publicURL,
				chatGPTClientID:                       test.clientID,
				allowHostedChatGPTDynamicRegistration: test.allowHosted,
			}
			config, err := service.chatGPTAuthorizationConfig()
			if test.wantError {
				if err == nil {
					t.Fatal("chatGPTAuthorizationConfig() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("chatGPTAuthorizationConfig() error = %v", err)
			}
			if config.ClientID != test.wantClientID ||
				config.RedirectURI != test.wantRedirect ||
				config.ManualCallback != test.wantManual ||
				config.IncludeHostID != test.wantHostID {
				t.Fatalf("chatGPTAuthorizationConfig() = %#v", config)
			}
		})
	}
}
