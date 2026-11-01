package modelclient

import "testing"

func TestIsContextTooLargeError(t *testing.T) {
	for _, err := range []error{
		&APIError{StatusCode: 400, Body: "maximum context length is 32768 tokens"},
		&APIError{StatusCode: 413, Body: "input exceeds the context window"},
		&APIError{StatusCode: 400, Body: "context_length_exceeded"},
	} {
		if !IsContextTooLargeError(err) {
			t.Fatalf("expected context overflow for %v", err)
		}
	}
	if IsContextTooLargeError(&APIError{StatusCode: 429, Body: "too many input tokens per minute"}) {
		t.Fatal("rate limit must not be treated as context overflow")
	}
}
