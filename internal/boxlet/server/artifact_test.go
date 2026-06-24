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
	"github.com/novitalabs/NovitaBox/internal/storage/store"
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

func TestCloneOrCopyFileCopiesContent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	dest := filepath.Join(root, "nested", "dest")
	if err := os.WriteFile(source, []byte("content"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if err := cloneOrCopyFile(source, dest); err != nil {
		t.Fatalf("cloneOrCopyFile() error = %v", err)
	}
	assertFileContent(t, dest, "content")
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

func TestArtifactServiceTemplateCRUD(t *testing.T) {
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

	if _, err := svc.CreateTemplate(context.Background(), &novitaboxv1.CreateTemplateRequest{
		TemplateId:  "tpl-crud",
		DockerImage: source,
	}); err != nil {
		t.Fatalf("CreateTemplate() error = %v", err)
	}

	list, err := svc.ListTemplates(context.Background(), &novitaboxv1.ListTemplatesRequest{})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	if len(list.GetTemplates()) != 1 || list.GetTemplates()[0].GetTemplateId() != "tpl-crud" {
		t.Fatalf("templates = %#v, want tpl-crud", list.GetTemplates())
	}

	got, err := svc.GetTemplate(context.Background(), &novitaboxv1.GetTemplateRequest{TemplateId: "tpl-crud"})
	if err != nil {
		t.Fatalf("GetTemplate() error = %v", err)
	}
	if got.GetRootfsPath() == "" {
		t.Fatalf("rootfs path is empty")
	}

	if _, err := svc.DeleteTemplate(context.Background(), &novitaboxv1.DeleteTemplateRequest{TemplateId: "tpl-crud"}); err != nil {
		t.Fatalf("DeleteTemplate() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "templates", "tpl-crud")); !os.IsNotExist(err) {
		t.Fatalf("template dir stat error = %v, want not exist", err)
	}
}

func TestArtifactServiceImageCRUD(t *testing.T) {
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

	if _, err := svc.CreateImage(context.Background(), &novitaboxv1.CreateImageRequest{
		ImageId:     "img-crud",
		DockerImage: source,
	}); err != nil {
		t.Fatalf("CreateImage() error = %v", err)
	}

	wantRootfs := filepath.Join(root, "images", "img-crud", "rootfs.ext4")
	data, err := os.ReadFile(wantRootfs)
	if err != nil {
		t.Fatalf("read image rootfs: %v", err)
	}
	if string(data) != "rootfs" {
		t.Fatalf("image rootfs content = %q, want rootfs", string(data))
	}

	list, err := svc.ListImages(context.Background(), &novitaboxv1.ListImagesRequest{})
	if err != nil {
		t.Fatalf("ListImages() error = %v", err)
	}
	if len(list.GetImages()) != 1 || list.GetImages()[0].GetImageId() != "img-crud" {
		t.Fatalf("images = %#v, want img-crud", list.GetImages())
	}

	got, err := svc.GetImage(context.Background(), &novitaboxv1.GetImageRequest{ImageId: "img-crud"})
	if err != nil {
		t.Fatalf("GetImage() error = %v", err)
	}
	if got.GetRootfsPath() != wantRootfs {
		t.Fatalf("rootfs path = %q, want %q", got.GetRootfsPath(), wantRootfs)
	}

	if _, err := svc.DeleteImage(context.Background(), &novitaboxv1.DeleteImageRequest{ImageId: "img-crud"}); err != nil {
		t.Fatalf("DeleteImage() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "images", "img-crud")); !os.IsNotExist(err) {
		t.Fatalf("image dir stat error = %v, want not exist", err)
	}
}

func TestSandboxServiceCreateFromTemplatePreparesRuntimeFiles(t *testing.T) {
	root := t.TempDir()
	st, err := sqlite.Open(context.Background(), filepath.Join(root, "novitabox.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer st.Close()

	templateDir := filepath.Join(root, "templates", "tpl-local")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatalf("create template dir: %v", err)
	}
	for name, content := range map[string]string{
		"rootfs.ext4": "rootfs",
		"memfile":     "mem",
		"snapfile":    "snap",
	} {
		if err := os.WriteFile(filepath.Join(templateDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write template %s: %v", name, err)
		}
	}
	if err := st.CreateTemplate(context.Background(), store.TemplateRecord{
		ID:           "tpl-local",
		RootfsPath:   filepath.Join(templateDir, "rootfs.ext4"),
		MemfilePath:  filepath.Join(templateDir, "memfile"),
		SnapfilePath: filepath.Join(templateDir, "snapfile"),
	}); err != nil {
		t.Fatalf("create template record: %v", err)
	}

	kernelPath := filepath.Join(root, "vmlinux.bin")
	if err := os.WriteFile(kernelPath, []byte("kernel"), 0o644); err != nil {
		t.Fatalf("write kernel: %v", err)
	}

	cfg := config.Default()
	cfg.RootDir = root
	cfg.Template.KernelPath = kernelPath
	svc := newSandboxService(cfg, log.NewNop(), st)

	spec := svc.completeRuntimeSpec(store.SandboxRecord{
		ID:          "i-test",
		RuntimeType: novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER.String(),
		TemplateID:  "tpl-local",
	}, nil)
	if err := svc.prepareSandboxRuntimeFiles(context.Background(), store.SandboxRecord{
		ID:          "i-test",
		RuntimeType: novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER.String(),
		TemplateID:  "tpl-local",
	}, spec); err != nil {
		t.Fatalf("prepareSandboxRuntimeFiles() error = %v", err)
	}

	sandboxDir := filepath.Join(root, "sandboxes", "i-test")
	assertFileContent(t, filepath.Join(sandboxDir, "snapshot", "rootfs.ext4"), "rootfs")
	assertFileContent(t, filepath.Join(sandboxDir, "snapshot", "memfile"), "mem")
	assertFileContent(t, filepath.Join(sandboxDir, "snapshot", "snapfile"), "snap")

	kernelLink := filepath.Join(sandboxDir, "kernel")
	target, err := os.Readlink(kernelLink)
	if err != nil {
		t.Fatalf("read kernel symlink: %v", err)
	}
	if target != kernelPath {
		t.Fatalf("kernel symlink target = %q, want %q", target, kernelPath)
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

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s content = %q, want %q", path, string(data), want)
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

func TestTemplateKernelArgsUseInjectedInit(t *testing.T) {
	args := strings.Join(templateKernelArgs(nil), " ")
	if !strings.Contains(args, "init=/novitabox/init") {
		t.Fatalf("template kernel args = %q, want injected init", args)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
