package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"testing"
	"time"

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

func TestConnectRPCStartStreamsProcessEvents(t *testing.T) {
	cfg := config.Default()
	s := New(cfg, log.NewNop())

	body := `{"process":{"cmd":"/bin/sh","args":["-c","printf ok"]},"pty":{"size":{"cols":80,"rows":24}}}`
	req := httptest.NewRequest(http.MethodPost, "/process.Process/Start", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	first := readConnectJSONFrame(t, rec.Body)
	var start connectStreamResponse
	if err := json.Unmarshal(first, &start); err != nil {
		t.Fatalf("decode start frame: %v", err)
	}
	if start.Event.Start == nil || start.Event.Start.PID == 0 {
		t.Fatalf("start frame = %#v, want start pid", start)
	}
}

func TestConnectRPCProtoStartStreamsProcessEvents(t *testing.T) {
	cfg := config.Default()
	s := New(cfg, log.NewNop())

	body := marshalProtoConnectEnvelopeForTest(t, 0, protoBytesField(1, marshalProtoProcessConfig(connectProcessConfig{
		Cmd:  "/bin/sh",
		Args: []string{"-c", "printf ok"},
	})))
	req := httptest.NewRequest(http.MethodPost, "/process.Process/Start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	first := readConnectJSONFrame(t, rec.Body)
	if len(first) == 0 {
		t.Fatalf("first proto frame is empty")
	}
}

func TestConnectRPCSendSignal(t *testing.T) {
	cfg := config.Default()
	s := New(cfg, log.NewNop())

	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start test process: %v", err)
	}
	defer cmd.Process.Kill()
	proc := &managedProcess{
		ID:      "proc-test",
		Cmd:     []string{"/bin/sh", "-c", "sleep 30"},
		Started: time.Now(),
		cmd:     cmd,
		done:    make(chan struct{}),
		output:  newOutputHub(),
		manager: s.processes,
	}
	s.processes.add(proc)

	signalBody := []byte(`{"process":{"pid":` + strconv.Itoa(cmd.Process.Pid) + `},"signal":9}`)
	signalReq := httptest.NewRequest(http.MethodPost, "/process.Process/SendSignal", bytes.NewReader(signalBody))
	signalReq.Header.Set("Content-Type", "application/json")
	signalRec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(signalRec, signalReq)
	if signalRec.Code != http.StatusOK {
		t.Fatalf("signal status = %d, want %d body=%s", signalRec.Code, http.StatusOK, signalRec.Body.String())
	}
}

func readConnectJSONFrame(t *testing.T, r io.Reader) []byte {
	t.Helper()
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		t.Fatalf("read connect frame header: %v", err)
	}
	size := binary.BigEndian.Uint32(header[1:])
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatalf("read connect frame payload: %v", err)
	}
	return payload
}

func marshalProtoConnectEnvelopeForTest(t *testing.T, flags byte, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := writeConnectEnvelope(&out, flags, payload); err != nil {
		t.Fatalf("write connect envelope: %v", err)
	}
	return out.Bytes()
}
