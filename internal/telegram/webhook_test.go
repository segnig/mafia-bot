package telegram

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookEndpointJoinsThePublicURL(t *testing.T) {
	got := webhookEndpoint("https://mafia-bot.onrender.com/")
	want := "https://mafia-bot.onrender.com/telegram/webhook"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeriveWebhookSecretIsStable(t *testing.T) {
	a := deriveWebhookSecret("token-a")
	b := deriveWebhookSecret("token-a")
	c := deriveWebhookSecret("token-b")
	if a != b {
		t.Error("the same bot token must produce the same secret across restarts")
	}
	if a == c {
		t.Error("different tokens must not share a secret")
	}
	if len(a) < 16 {
		t.Errorf("secret is too short to use as secret_token: %q", a)
	}
}

func TestDecodeUpdateReadsACallbackAndACommand(t *testing.T) {
	body := `{"update_id":1,"message":{"message_id":2,"text":"/start","chat":{"id":9,"type":"private"},"from":{"id":7},"entities":[{"type":"bot_command","offset":0,"length":6}]}}`
	update, err := decodeUpdate(bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	if update.Message == nil || update.Message.Text != "/start" {
		t.Fatalf("did not decode the message: %+v", update.Message)
	}

	cb := `{"update_id":2,"callback_query":{"id":"c1","from":{"id":7},"data":"join:g1"}}`
	update, err = decodeUpdate(bytes.NewReader([]byte(cb)))
	if err != nil {
		t.Fatal(err)
	}
	if update.CallbackQuery == nil || update.CallbackQuery.Data != "join:g1" {
		t.Fatalf("did not decode the callback: %+v", update.CallbackQuery)
	}
}

func TestDecodeUpdateRejectsGarbage(t *testing.T) {
	if _, err := decodeUpdate(bytes.NewReader([]byte("not json"))); err == nil {
		t.Error("garbage should not decode as an update")
	}
}

func TestWebhookHandlerChecksMethodSecretAndBody(t *testing.T) {
	b := &Bot{}
	handler := b.webhookHandler("s3cret")

	req := httptest.NewRequest(http.MethodGet, webhookPath, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", rec.Code)
	}

	body := `{"update_id":99}`
	req = httptest.NewRequest(http.MethodPost, webhookPath, bytes.NewReader([]byte(body)))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing secret = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, webhookPath, bytes.NewReader([]byte(body)))
	req.Header.Set(secretHeader, "wrong")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, webhookPath, bytes.NewReader([]byte("{")))
	req.Header.Set(secretHeader, "s3cret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json = %d, want 400", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, webhookPath, bytes.NewReader([]byte(body)))
	req.Header.Set(secretHeader, "s3cret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("valid update = %d, want 200", rec.Code)
	}

	time.Sleep(20 * time.Millisecond)
}
