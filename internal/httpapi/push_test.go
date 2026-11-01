package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/uvwt/CycleDock/internal/store"
)

func TestNewCardMessageIgnoresPinAndStatus(t *testing.T) {
	previous := store.PushCardSnapshot{EventSequence: "1", AttemptStatus: "running", MessagePreview: "你好"}
	reply := cardUpdate{EventSequence: "2", AttemptStatus: "completed", MessagePreview: "可以"}
	if !newCardMessage(reply, previous) || cardPushBody(reply) != "可以" {
		t.Fatal("new reply should notify")
	}
	failed := cardUpdate{EventSequence: "1", AttemptStatus: "failed", MessagePreview: "你好"}
	if newCardMessage(failed, previous) {
		t.Fatal("status change is not a new message")
	}
	pinned := cardUpdate{EventSequence: "1", AttemptStatus: "running", MessagePreview: "你好", Title: "已置顶"}
	if newCardMessage(pinned, previous) {
		t.Fatal("pin or title change is not a new message")
	}
}

func TestNormalizePushEndpointRejectsNonHTTPS(t *testing.T) {
	recorder := httptest.NewRecorder()
	if _, ok := normalizePushEndpoint(recorder, "http://push.example/send"); ok || recorder.Code != 400 {
		t.Fatalf("http endpoint accepted: %d", recorder.Code)
	}
}
