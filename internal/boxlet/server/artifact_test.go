package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/store/sqlite"
)

func TestArtifactServiceCreateTemplateFromLocalRootfs(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source-rootfs.ext4")
	if err := os.WriteFile(source, []byte("rootfs"), 0o644); err != nil {
		t.Fatalf("write source rootfs: %v", err)
	}

	st, err := sqlite.Open(context.Background(), filepath.Join(root, "novitabox.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.RootDir = root
	svc := newArtifactService(cfg, log.NewNop(), st)

	info, err := svc.CreateTemplate(context.Background(), &novitaboxv1.CreateTemplateRequest{
		TemplateId:  "tpl-local",
		DockerImage: source,
	})
	if err != nil {
		t.Fatalf("CreateTemplate() error = %v", err)
	}

	wantRootfs := filepath.Join(root, "templates", "tpl-local", "rootfs.ext4")
	if info.GetRootfsPath() != wantRootfs {
		t.Fatalf("rootfs path = %q, want %q", info.GetRootfsPath(), wantRootfs)
	}

	data, err := os.ReadFile(wantRootfs)
	if err != nil {
		t.Fatalf("read materialized rootfs: %v", err)
	}
	if string(data) != "rootfs" {
		t.Fatalf("rootfs content = %q, want rootfs", string(data))
	}

	if _, err := os.Stat(filepath.Join(root, "templates", "tpl-local", "memfile")); err != nil {
		t.Fatalf("memfile not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "templates", "tpl-local", "snapfile")); err != nil {
		t.Fatalf("snapfile not created: %v", err)
	}

	record, err := st.GetTemplate(context.Background(), "tpl-local")
	if err != nil {
		t.Fatalf("get template record: %v", err)
	}
	if record.RootfsPath != wantRootfs {
		t.Fatalf("stored rootfs path = %q, want %q", record.RootfsPath, wantRootfs)
	}
}

func TestArtifactServiceCreateTemplateSnapshotRequiresKernel(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source-rootfs.ext4")
	if err := os.WriteFile(source, []byte("rootfs"), 0o644); err != nil {
		t.Fatalf("write source rootfs: %v", err)
	}

	st, err := sqlite.Open(context.Background(), filepath.Join(root, "novitabox.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.RootDir = root
	cfg.Template.SnapshotEnabled = true
	svc := newArtifactService(cfg, log.NewNop(), st)

	_, err = svc.CreateTemplate(context.Background(), &novitaboxv1.CreateTemplateRequest{
		TemplateId:  "tpl-local",
		DockerImage: source,
	})
	if err == nil {
		t.Fatal("CreateTemplate() error = nil, want kernel required error")
	}
}

func TestWaitHTTPHealth(t *testing.T) {
	requests := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			status := http.StatusOK
			if requests < 2 {
				status = http.StatusServiceUnavailable
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(bytes.NewBufferString("{}")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	if err := waitHTTPHealthWithClient(context.Background(), "http://boxd/healthz", time.Second, client); err != nil {
		t.Fatalf("waitHTTPHealth() error = %v", err)
	}
	if requests < 2 {
		t.Fatalf("requests = %d, want at least 2", requests)
	}
}

func TestExecTemplateCommand(t *testing.T) {
	var gotCmd []string
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var body struct {
				Cmd []string `json:"cmd"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode exec request: %v", err)
			}
			gotCmd = body.Cmd
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`{"exitCode":0}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	if err := execTemplateCommandWithClient(context.Background(), "http://boxd/exec", []string{"/bin/sh", "-c", "echo ok"}, nil, client); err != nil {
		t.Fatalf("execTemplateCommand() error = %v", err)
	}
	if len(gotCmd) != 3 || gotCmd[2] != "echo ok" {
		t.Fatalf("cmd = %#v, want /bin/sh -c echo ok", gotCmd)
	}
}

func TestResolveShimBinaryAbsolute(t *testing.T) {
	path, err := resolveShimBinary("/opt/novitabox/bin/boxshim")
	if err != nil {
		t.Fatalf("resolveShimBinary() error = %v", err)
	}
	if path != "/opt/novitabox/bin/boxshim" {
		t.Fatalf("path = %q, want absolute path unchanged", path)
	}
}

func TestTemplateBoxdInitScript(t *testing.T) {
	script := templateBoxdInitScript("/novitabox/boxd", "0.0.0.0:49983")
	if !strings.Contains(script, "exec /novitabox/boxd --addr 0.0.0.0:49983") {
		t.Fatalf("init script does not start boxd: %s", script)
	}
	if strings.Contains(script, "exec /sbin/init") || strings.Contains(script, "exec /bin/sh") {
		t.Fatalf("init script should keep boxd as PID 1: %s", script)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
