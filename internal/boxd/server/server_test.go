package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
)

func TestHealthz(t *testing.T) {
	cfg := config.Default()
	s := New(cfg, log.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Status  string `json:"status"`
		Service string `json:"service"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body.Status != "ok" || body.Service != "boxd" {
		t.Fatalf("health response = %#v, want status=ok service=boxd", body)
	}
}

func TestExec(t *testing.T) {
	cfg := config.Default()
	s := New(cfg, log.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/exec", bytes.NewBufferString(`{"cmd":["/bin/sh","-c","echo ok"]}`))
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body execResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode exec response: %v", err)
	}
	if body.ExitCode != 0 || body.Stdout != "ok\n" {
		t.Fatalf("exec response = %#v, want exitCode=0 stdout=ok", body)
	}
}
