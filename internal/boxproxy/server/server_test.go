package server

import (
	"net/http/httptest"
	"testing"
)

func TestBoxdPathFromSandboxRestSupportsProcessConnectRPC(t *testing.T) {
	got, err := boxdPathFromSandboxRest("process.Process/Start")
	if err != nil {
		t.Fatalf("boxdPathFromSandboxRest() error = %v", err)
	}
	if got != "/process.Process/Start" {
		t.Fatalf("boxdPathFromSandboxRest() = %q, want /process.Process/Start", got)
	}
}

func TestSandboxIDFromRequestSupportsHeaderAndHost(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1/process.Process/Start", nil)
	req.Header.Set(sandboxIDHeader, "sbx-header")
	if got := sandboxIDFromRequest(req); got != "sbx-header" {
		t.Fatalf("sandboxIDFromRequest(header) = %q, want sbx-header", got)
	}

	req = httptest.NewRequest("GET", "http://127.0.0.1/processes/proc/connect?sandboxID=sbx-query", nil)
	if got := sandboxIDFromRequest(req); got != "sbx-query" {
		t.Fatalf("sandboxIDFromRequest(query) = %q, want sbx-query", got)
	}

	req = httptest.NewRequest("POST", "https://49983-sbx-host.novitabox.localhost/process.Process/Start", nil)
	if got := sandboxIDFromRequest(req); got != "sbx-host" {
		t.Fatalf("sandboxIDFromRequest(host) = %q, want sbx-host", got)
	}
}
