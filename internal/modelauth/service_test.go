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
