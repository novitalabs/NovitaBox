package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	"github.com/novitalabs/NovitaBox/internal/storage/store/sqlite"
)

func TestRouterHealthz(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestRouterNotImplemented(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/images", nil)
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected status %d, got %d", http.StatusNotImplemented, rec.Code)
	}
}

func TestRouterListSandboxes(t *testing.T) {
	s := newTestServer(t)
	createReq := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(`{"sandbox_id":"sbx-test","templateID":"tpl-test"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()

	s.router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create expected status %d, got %d body=%s", http.StatusCreated, createRec.Code, createRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes", nil)
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var got struct {
		Sandboxes []struct {
			SandboxID string `json:"sandboxID"`
			State     string `json:"state"`
		} `json:"sandboxes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Sandboxes) != 1 {
		t.Fatalf("len(sandboxes) = %d, want 1", len(got.Sandboxes))
	}
	if got.Sandboxes[0].SandboxID != "sbx-test" || got.Sandboxes[0].State != "running" {
		t.Fatalf("sandbox = %#v, want sbx-test running", got.Sandboxes[0])
	}
}

func TestRouterSandboxLifecycleFallback(t *testing.T) {
	s := newTestServer(t)
	createReq := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(`{"sandbox_id":"sbx-test","templateID":"tpl-test"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()

	s.router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create expected status %d, got %d body=%s", http.StatusCreated, createRec.Code, createRec.Body.String())
	}

	pauseReq := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/sbx-test/pause", nil)
	pauseRec := httptest.NewRecorder()
	s.router().ServeHTTP(pauseRec, pauseReq)

	if pauseRec.Code != http.StatusOK {
		t.Fatalf("pause expected status %d, got %d body=%s", http.StatusOK, pauseRec.Code, pauseRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/sbx-test", nil)
	getRec := httptest.NewRecorder()
	s.router().ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("get expected status %d, got %d body=%s", http.StatusOK, getRec.Code, getRec.Body.String())
	}

	var got struct {
		SandboxID string `json:"sandboxID"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.SandboxID != "sbx-test" || got.State != "paused" {
		t.Fatalf("sandbox = %#v, want sbx-test paused", got)
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/sbx-test/resume", nil)
	resumeRec := httptest.NewRecorder()
	s.router().ServeHTTP(resumeRec, resumeReq)

	if resumeRec.Code != http.StatusOK {
		t.Fatalf("resume expected status %d, got %d body=%s", http.StatusOK, resumeRec.Code, resumeRec.Body.String())
	}

	sandboxDir := filepath.Join(s.cfg.RootDir, "sandboxes", "sbx-test")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatalf("create sandbox dir: %v", err)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/sbx-test", nil)
	deleteRec := httptest.NewRecorder()
	s.router().ServeHTTP(deleteRec, deleteReq)

	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete expected status %d, got %d body=%s", http.StatusNoContent, deleteRec.Code, deleteRec.Body.String())
	}
	if _, err := os.Stat(sandboxDir); !os.IsNotExist(err) {
		t.Fatalf("sandbox dir stat error = %v, want not exist", err)
	}
}

func TestRouterSandboxPowerLifecycleFallback(t *testing.T) {
	s := newTestServer(t)
	createReq := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(`{"sandbox_id":"sbx-power","templateID":"tpl-test"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	s.router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create expected status %d, got %d body=%s", http.StatusCreated, createRec.Code, createRec.Body.String())
	}

	for _, tc := range []struct {
		name  string
		path  string
		state string
	}{
		{name: "poweroff", path: "/v1/sandboxes/sbx-power/poweroff", state: "stopped"},
		{name: "poweron", path: "/v1/sandboxes/sbx-power/poweron", state: "running"},
		{name: "reboot", path: "/v1/sandboxes/sbx-power/reboot", state: "running"},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, nil)
		rec := httptest.NewRecorder()
		s.router().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s expected status %d, got %d body=%s", tc.name, http.StatusOK, rec.Code, rec.Body.String())
		}
		var got struct {
			SandboxID string `json:"sandboxID"`
			State     string `json:"state"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode %s response: %v", tc.name, err)
		}
		if got.SandboxID != "sbx-power" || got.State != tc.state {
			t.Fatalf("%s sandbox = %#v, want sbx-power %s", tc.name, got, tc.state)
		}
	}
}

func TestRouterSandboxNotFound(t *testing.T) {
	s := newTestServer(t)
	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v1/sandboxes/sbx-missing"},
		{method: http.MethodDelete, path: "/v1/sandboxes/sbx-missing"},
		{method: http.MethodPost, path: "/v1/sandboxes/sbx-missing/pause"},
		{method: http.MethodPost, path: "/v1/sandboxes/sbx-missing/resume"},
		{method: http.MethodPost, path: "/v1/sandboxes/sbx-missing/poweroff"},
		{method: http.MethodPost, path: "/v1/sandboxes/sbx-missing/poweron"},
		{method: http.MethodPost, path: "/v1/sandboxes/sbx-missing/reboot"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		s.router().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s expected status %d, got %d body=%s", tc.method, tc.path, http.StatusNotFound, rec.Code, rec.Body.String())
		}
	}
}

func TestRouterLegacyTemplateCreateNotRegistered(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/templates", bytes.NewBufferString(`{"dockerfile":"FROM ubuntu:22.04"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

func TestRouterCreateSandbox(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(`{"sandbox_id":"sbx-test","templateID":"tpl-test"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, rec.Code, rec.Body.String())
	}

	var got struct {
		SandboxID  string `json:"sandboxID"`
		TemplateID string `json:"templateID"`
		ClientID   string `json:"clientID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.SandboxID != "sbx-test" {
		t.Fatalf("sandboxID = %q, want sbx-test", got.SandboxID)
	}
	if got.TemplateID != "tpl-test" {
		t.Fatalf("templateID = %q, want tpl-test", got.TemplateID)
	}
}

func TestRouterCreateSandboxDuplicate(t *testing.T) {
	s := newTestServer(t)

	for i, want := range []int{http.StatusCreated, http.StatusConflict} {
		req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(`{"sandbox_id":"sbx-test","templateID":"tpl-test"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		s.router().ServeHTTP(rec, req)

		if rec.Code != want {
			t.Fatalf("request %d expected status %d, got %d body=%s", i+1, want, rec.Code, rec.Body.String())
		}
	}
}

func TestRouterCreateSandboxRequiresTemplate(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(`{"sandbox_id":"sbx-test"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestRouterCreateTemplateV3(t *testing.T) {
	s := newTestServer(t)
	body := bytes.NewBufferString(`{"templateID":"tpl-explicit","name":"team/python:v1","tags":["latest","v1"],"metadata":{"owner":"test"},"cpuCount":4,"memoryMB":1024}`)
	req := httptest.NewRequest(http.MethodPost, "/v3/templates", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusAccepted, rec.Code, rec.Body.String())
	}

	var got struct {
		Aliases    []string          `json:"aliases"`
		BuildID    string            `json:"buildID"`
		Metadata   map[string]string `json:"metadata"`
		Names      []string          `json:"names"`
		Public     bool              `json:"public"`
		Tags       []string          `json:"tags"`
		TemplateID string            `json:"templateID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.TemplateID != "tpl-explicit" {
		t.Fatalf("templateID = %q, want tpl-explicit", got.TemplateID)
	}
	if got.BuildID == "" {
		t.Fatal("buildID is empty")
	}
	if len(got.Aliases) != 1 || got.Aliases[0] != "team/python" {
		t.Fatalf("aliases = %#v, want team/python", got.Aliases)
	}
	if len(got.Names) != 1 || got.Names[0] != "team/python" {
		t.Fatalf("names = %#v, want team/python", got.Names)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "v1" || got.Tags[1] != "latest" {
		t.Fatalf("tags = %#v, want [v1 latest]", got.Tags)
	}
	if got.Metadata["owner"] != "test" {
		t.Fatalf("metadata = %#v, want owner=test", got.Metadata)
	}
	if got.Public {
		t.Fatal("public = true, want false")
	}
}

func TestRouterCreateTemplateV3GeneratesTemplateID(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v3/templates", bytes.NewBufferString(`{"name":"team/python:v1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusAccepted, rec.Code, rec.Body.String())
	}

	var got struct {
		TemplateID string `json:"templateID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.HasPrefix(got.TemplateID, "tpl-") {
		t.Fatalf("templateID = %q, want generated tpl-* id", got.TemplateID)
	}
	if got.TemplateID == "tpl-team-python" {
		t.Fatalf("templateID = %q, should not be derived from template name", got.TemplateID)
	}
}

func TestRouterStartTemplateBuildV2(t *testing.T) {
	s := newTestServer(t)
	createReq := httptest.NewRequest(http.MethodPost, "/v3/templates", bytes.NewBufferString(`{"name":"team/python:v1"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()

	s.router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusAccepted {
		t.Fatalf("create expected status %d, got %d body=%s", http.StatusAccepted, createRec.Code, createRec.Body.String())
	}

	var created struct {
		BuildID    string `json:"buildID"`
		TemplateID string `json:"templateID"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	startReq := httptest.NewRequest(
		http.MethodPost,
		"/v2/templates/"+created.TemplateID+"/builds/"+created.BuildID,
		bytes.NewBufferString(`{"fromImage":"ubuntu:22.04","startCmd":"sleep infinity","readyCmd":"echo ok"}`),
	)
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()

	s.router().ServeHTTP(startRec, startReq)

	if startRec.Code != http.StatusAccepted {
		t.Fatalf("start expected status %d, got %d body=%s", http.StatusAccepted, startRec.Code, startRec.Body.String())
	}
}

func TestRouterStartTemplateBuildV2NotFound(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v2/templates/tpl-missing/builds/00000000-0000-4000-8000-000000000000", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

func TestRouterTemplateCRUD(t *testing.T) {
	s := newTestServer(t)
	createReq := httptest.NewRequest(http.MethodPost, "/v3/templates", bytes.NewBufferString(`{"templateID":"tpl-crud","name":"team/python:v1"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	s.router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusAccepted {
		t.Fatalf("create expected status %d, got %d body=%s", http.StatusAccepted, createRec.Code, createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/templates", nil)
	listRec := httptest.NewRecorder()
	s.router().ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("list expected status %d, got %d body=%s", http.StatusOK, listRec.Code, listRec.Body.String())
	}
	var listGot struct {
		Templates []struct {
			TemplateID string `json:"templateID"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listGot); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listGot.Templates) != 1 || listGot.Templates[0].TemplateID != "tpl-crud" {
		t.Fatalf("templates = %#v, want tpl-crud", listGot.Templates)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/templates/tpl-crud", nil)
	getRec := httptest.NewRecorder()
	s.router().ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("get expected status %d, got %d body=%s", http.StatusOK, getRec.Code, getRec.Body.String())
	}
	var getGot struct {
		TemplateID  string `json:"templateID"`
		RootfsPath  string `json:"rootfsPath"`
		MemfilePath string `json:"memfilePath"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getGot); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if getGot.TemplateID != "tpl-crud" || !strings.HasSuffix(getGot.RootfsPath, filepath.Join("templates", "tpl-crud", "rootfs.ext4")) || getGot.MemfilePath == "" {
		t.Fatalf("template = %#v, want tpl-crud paths", getGot)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/v1/templates/tpl-crud", nil)
	deleteRec := httptest.NewRecorder()
	s.router().ServeHTTP(deleteRec, deleteReq)

	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete expected status %d, got %d body=%s", http.StatusNoContent, deleteRec.Code, deleteRec.Body.String())
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/v1/templates/tpl-crud", nil)
	missingRec := httptest.NewRecorder()
	s.router().ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing get expected status %d, got %d body=%s", http.StatusNotFound, missingRec.Code, missingRec.Body.String())
	}
}

func TestRouterConvertTemplateToImage(t *testing.T) {
	s := newTestServer(t)
	createReq := httptest.NewRequest(http.MethodPost, "/v3/templates", bytes.NewBufferString(`{"templateID":"tpl-convert","name":"team/python:v1"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	s.router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusAccepted {
		t.Fatalf("create expected status %d, got %d body=%s", http.StatusAccepted, createRec.Code, createRec.Body.String())
	}

	convertReq := httptest.NewRequest(http.MethodPost, "/v1/templates/convert", bytes.NewBufferString(`{"templateID":"tpl-convert","imageID":"img-convert"}`))
	convertReq.Header.Set("Content-Type", "application/json")
	convertRec := httptest.NewRecorder()
	s.router().ServeHTTP(convertRec, convertReq)

	if convertRec.Code != http.StatusCreated {
		t.Fatalf("convert expected status %d, got %d body=%s", http.StatusCreated, convertRec.Code, convertRec.Body.String())
	}
	var got struct {
		ImageID    string `json:"imageID"`
		TemplateID string `json:"templateID"`
		RootfsPath string `json:"rootfsPath"`
	}
	if err := json.Unmarshal(convertRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode convert response: %v", err)
	}
	if got.ImageID != "img-convert" || got.TemplateID != "tpl-convert" || got.RootfsPath == "" {
		t.Fatalf("convert response = %#v, want img-convert from tpl-convert", got)
	}
}

func TestRouterCreateTemplateV3RequiresName(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v3/templates", bytes.NewBufferString(`{"tags":["latest"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()

	root := t.TempDir()
	cfg := config.Default()
	cfg.RootDir = root
	cfg.Storage.DBPath = filepath.Join(root, "novitabox.db")

	st, err := sqlite.Open(t.Context(), cfg.Storage.DBPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("close sqlite store: %v", err)
		}
	})

	return New(cfg, log.New(httptest.NewRecorder()), st, nil, nil)
}
