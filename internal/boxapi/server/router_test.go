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
	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes", nil)
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected status %d, got %d", http.StatusNotImplemented, rec.Code)
	}
}

func TestRouterListSandboxes(t *testing.T) {
	s := newTestServer(t)
	sandboxID := createSandboxForTest(t, s, `{"templateID":"tpl-test"}`)

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
	if got.Sandboxes[0].SandboxID != sandboxID || got.Sandboxes[0].State != "running" {
		t.Fatalf("sandbox = %#v, want %s running", got.Sandboxes[0], sandboxID)
	}
}

func TestRouterSandboxLifecycleFallback(t *testing.T) {
	s := newTestServer(t)
	sandboxID := createSandboxForTest(t, s, `{"templateID":"tpl-test"}`)

	pauseReq := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+sandboxID+"/pause", nil)
	pauseRec := httptest.NewRecorder()
	s.router().ServeHTTP(pauseRec, pauseReq)

	if pauseRec.Code != http.StatusOK {
		t.Fatalf("pause expected status %d, got %d body=%s", http.StatusOK, pauseRec.Code, pauseRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/"+sandboxID, nil)
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
	if got.SandboxID != sandboxID || got.State != "paused" {
		t.Fatalf("sandbox = %#v, want %s paused", got, sandboxID)
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+sandboxID+"/resume", nil)
	resumeRec := httptest.NewRecorder()
	s.router().ServeHTTP(resumeRec, resumeReq)

	if resumeRec.Code != http.StatusOK {
		t.Fatalf("resume expected status %d, got %d body=%s", http.StatusOK, resumeRec.Code, resumeRec.Body.String())
	}

	sandboxDir := filepath.Join(s.cfg.RootDir, "sandboxes", sandboxID)
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatalf("create sandbox dir: %v", err)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/"+sandboxID, nil)
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
	sandboxID := createSandboxForTest(t, s, `{"templateID":"tpl-test"}`)

	for _, tc := range []struct {
		name  string
		path  string
		state string
	}{
		{name: "poweroff", path: "/v1/sandboxes/" + sandboxID + "/poweroff", state: "stopped"},
		{name: "poweron", path: "/v1/sandboxes/" + sandboxID + "/poweron", state: "running"},
		{name: "reboot", path: "/v1/sandboxes/" + sandboxID + "/reboot", state: "running"},
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
		if got.SandboxID != sandboxID || got.State != tc.state {
			t.Fatalf("%s sandbox = %#v, want %s %s", tc.name, got, sandboxID, tc.state)
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
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(`{"sandboxID":"client-id-is-ignored","sandbox_id":"client-id-is-ignored","templateID":"tpl-test"}`))
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
	if got.SandboxID == "" || !strings.HasPrefix(got.SandboxID, "i") || got.SandboxID == "client-id-is-ignored" {
		t.Fatalf("sandboxID = %q, want generated i* id", got.SandboxID)
	}
	if got.TemplateID != "tpl-test" {
		t.Fatalf("templateID = %q, want tpl-test", got.TemplateID)
	}
}

func TestRouterCreateSandboxGeneratesUniqueIDs(t *testing.T) {
	s := newTestServer(t)

	first := createSandboxForTest(t, s, `{"templateID":"tpl-test"}`)
	second := createSandboxForTest(t, s, `{"templateID":"tpl-test"}`)

	if first == second {
		t.Fatalf("generated duplicate sandboxID %q", first)
	}
}

func TestRouterCreateSandboxRequiresTemplate(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(`{}`))
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
	suffix := strings.TrimPrefix(got.TemplateID, "tpl-")
	if len(suffix) != 20 {
		t.Fatalf("templateID suffix length = %d, want 20", len(suffix))
	}
	for _, ch := range suffix {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
			t.Fatalf("templateID suffix = %q, want lowercase alphanumeric", suffix)
		}
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

func TestRouterRawTemplateBuildStartNotRegistered(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/templates/tpl-test/builds/00000000-0000-4000-8000-000000000000", nil)
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, rec.Code, rec.Body.String())
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
	createReq := httptest.NewRequest(http.MethodPost, "/v3/templates", bytes.NewBufferString(`{"templateID":"tpl-crud","name":"team/python:v1","metadata":{"owner":"api"},"cpuCount":2,"memoryMB":1024}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	s.router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusAccepted {
		t.Fatalf("create expected status %d, got %d body=%s", http.StatusAccepted, createRec.Code, createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/templates", nil)
	listRec := httptest.NewRecorder()
	s.router().ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("list expected status %d, got %d body=%s", http.StatusOK, listRec.Code, listRec.Body.String())
	}
	var listGot []struct {
		Aliases     []string          `json:"aliases"`
		BuildCount  int32             `json:"buildCount"`
		BuildID     string            `json:"buildID"`
		BuildStatus string            `json:"buildStatus"`
		CPUCount    int32             `json:"cpuCount"`
		CreatedAt   string            `json:"createdAt"`
		DiskSizeMB  int64             `json:"diskSizeMB"`
		MemoryMB    int32             `json:"memoryMB"`
		Metadata    map[string]string `json:"metadata"`
		Names       []string          `json:"names"`
		Public      bool              `json:"public"`
		SpawnCount  int64             `json:"spawnCount"`
		TemplateID  string            `json:"templateID"`
		UpdatedAt   string            `json:"updatedAt"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listGot); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listGot) != 1 || listGot[0].TemplateID != "tpl-crud" {
		t.Fatalf("templates = %#v, want tpl-crud", listGot)
	}
	if listGot[0].BuildCount != 1 || listGot[0].BuildID == "" || listGot[0].BuildStatus != "waiting" {
		t.Fatalf("template build fields = %#v, want one waiting build", listGot[0])
	}
	if listGot[0].CPUCount != 2 || listGot[0].MemoryMB != 1024 {
		t.Fatalf("template resources = cpu:%d memory:%d, want 2/1024", listGot[0].CPUCount, listGot[0].MemoryMB)
	}
	if len(listGot[0].Aliases) != 1 || listGot[0].Aliases[0] != "team/python" || len(listGot[0].Names) != 1 || listGot[0].Names[0] != "team/python" {
		t.Fatalf("template names = aliases:%#v names:%#v, want team/python", listGot[0].Aliases, listGot[0].Names)
	}
	if listGot[0].Metadata["owner"] != "api" {
		t.Fatalf("metadata = %#v, want owner=api", listGot[0].Metadata)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/templates/tpl-crud", nil)
	getRec := httptest.NewRecorder()
	s.router().ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("get expected status %d, got %d body=%s", http.StatusOK, getRec.Code, getRec.Body.String())
	}
	var getGot struct {
		Aliases []string `json:"aliases"`
		Builds  []struct {
			BuildID   string `json:"buildID"`
			CPUCount  int32  `json:"cpuCount"`
			CreatedAt string `json:"createdAt"`
			MemoryMB  int32  `json:"memoryMB"`
			Status    string `json:"status"`
			UpdatedAt string `json:"updatedAt"`
		} `json:"builds"`
		CreatedAt     string            `json:"createdAt"`
		LastSpawnedAt *string           `json:"lastSpawnedAt"`
		Metadata      map[string]string `json:"metadata"`
		Names         []string          `json:"names"`
		Public        bool              `json:"public"`
		SpawnCount    int64             `json:"spawnCount"`
		TemplateID    string            `json:"templateID"`
		UpdatedAt     string            `json:"updatedAt"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getGot); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if getGot.TemplateID != "tpl-crud" {
		t.Fatalf("templateID = %q, want tpl-crud", getGot.TemplateID)
	}
	if getGot.CreatedAt == "" || getGot.UpdatedAt == "" {
		t.Fatalf("timestamps are empty: %#v", getGot)
	}
	if len(getGot.Builds) != 1 || getGot.Builds[0].Status != "waiting" {
		t.Fatalf("builds = %#v, want one waiting build", getGot.Builds)
	}
	if getGot.Builds[0].CPUCount != 2 || getGot.Builds[0].MemoryMB != 1024 {
		t.Fatalf("build resources = %#v, want 2/1024", getGot.Builds[0])
	}
	if len(getGot.Aliases) != 1 || getGot.Aliases[0] != "team/python" || len(getGot.Names) != 1 || getGot.Names[0] != "team/python" {
		t.Fatalf("template names = aliases:%#v names:%#v, want team/python", getGot.Aliases, getGot.Names)
	}
	if getGot.Metadata["owner"] != "api" {
		t.Fatalf("metadata = %#v, want owner=api", getGot.Metadata)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(getRec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode get response as map: %v", err)
	}
	for _, field := range []string{"state", "rootfsPath", "memfilePath", "snapfilePath", "latestBuild"} {
		if _, ok := raw[field]; ok {
			t.Fatalf("response contains internal field %q: %s", field, getRec.Body.String())
		}
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/templates/tpl-crud", nil)
	deleteRec := httptest.NewRecorder()
	s.router().ServeHTTP(deleteRec, deleteReq)

	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete expected status %d, got %d body=%s", http.StatusNoContent, deleteRec.Code, deleteRec.Body.String())
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/templates/tpl-crud", nil)
	missingRec := httptest.NewRecorder()
	s.router().ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing get expected status %d, got %d body=%s", http.StatusNotFound, missingRec.Code, missingRec.Body.String())
	}
}

func TestRouterV1TemplateRoutesNotRegistered(t *testing.T) {
	s := newTestServer(t)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v1/templates"},
		{method: http.MethodGet, path: "/v1/templates/tpl-test"},
		{method: http.MethodDelete, path: "/v1/templates/tpl-test"},
		{method: http.MethodPost, path: "/v1/templates/convert"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()

		s.router().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s expected status %d, got %d body=%s", tc.method, tc.path, http.StatusNotFound, rec.Code, rec.Body.String())
		}
	}
}

func TestRouterImageCRUD(t *testing.T) {
	s := newTestServer(t)
	source := filepath.Join(s.cfg.RootDir, "source-rootfs.ext4")
	if err := os.WriteFile(source, []byte("rootfs"), 0o644); err != nil {
		t.Fatalf("write source rootfs: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewBufferString(`{"imageID":"img-crud","rootfsPath":"`+source+`"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	s.router().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create expected status %d, got %d body=%s", http.StatusCreated, createRec.Code, createRec.Body.String())
	}
	var created struct {
		ImageID    string `json:"imageID"`
		RootfsPath string `json:"rootfsPath"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ImageID != "img-crud" || !strings.HasSuffix(created.RootfsPath, filepath.Join("images", "img-crud", "rootfs.ext4")) {
		t.Fatalf("created image = %#v, want img-crud image path", created)
	}
	data, err := os.ReadFile(created.RootfsPath)
	if err != nil {
		t.Fatalf("read image rootfs: %v", err)
	}
	if string(data) != "rootfs" {
		t.Fatalf("image rootfs content = %q, want rootfs", string(data))
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/images", nil)
	listRec := httptest.NewRecorder()
	s.router().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list expected status %d, got %d body=%s", http.StatusOK, listRec.Code, listRec.Body.String())
	}
	var listed struct {
		Images []struct {
			ImageID string `json:"imageID"`
		} `json:"images"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Images) != 1 || listed.Images[0].ImageID != "img-crud" {
		t.Fatalf("images = %#v, want img-crud", listed.Images)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/images/img-crud", nil)
	getRec := httptest.NewRecorder()
	s.router().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get expected status %d, got %d body=%s", http.StatusOK, getRec.Code, getRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/v1/images/img-crud", nil)
	deleteRec := httptest.NewRecorder()
	s.router().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete expected status %d, got %d body=%s", http.StatusNoContent, deleteRec.Code, deleteRec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.cfg.RootDir, "images", "img-crud")); !os.IsNotExist(err) {
		t.Fatalf("image dir stat error = %v, want not exist", err)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/v1/images/img-crud", nil)
	missingRec := httptest.NewRecorder()
	s.router().ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing get expected status %d, got %d body=%s", http.StatusNotFound, missingRec.Code, missingRec.Body.String())
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

func createSandboxForTest(t *testing.T, s *Server, body string) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.router().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create expected status %d, got %d body=%s", http.StatusCreated, rec.Code, rec.Body.String())
	}

	var got struct {
		SandboxID string `json:"sandboxID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if got.SandboxID == "" {
		t.Fatal("created sandboxID is empty")
	}

	return got.SandboxID
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
