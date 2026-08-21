package overlaybd

import (
	"context"
	"reflect"
	"testing"
)

func TestCtrPullerUsesExplicitAddressNamespaceAndSnapshotter(t *testing.T) {
	runner := &recordingRunner{}
	puller := NewCtrPuller(CtrPullerConfig{
		BinaryPath:        "/opt/overlaybd/snapshotter/ctr",
		ContainerdAddress: "/run/containerd/containerd.sock",
		Namespace:         "novitabox",
		Snapshotter:       "overlaybd",
	}, runner)

	if err := puller.Pull(context.Background(), "registry.example/team/image:tag"); err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	want := []string{
		"--address", "/run/containerd/containerd.sock",
		"--namespace", "novitabox",
		"rpull", "--snapshotter", "overlaybd",
		"registry.example/team/image:tag",
	}
	if runner.path != "/opt/overlaybd/snapshotter/ctr" || !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("command = %q %#v, want %q %#v", runner.path, runner.args, "/opt/overlaybd/snapshotter/ctr", want)
	}
}

type recordingRunner struct {
	path string
	args []string
}

func (r *recordingRunner) Run(_ context.Context, path string, args ...string) ([]byte, error) {
	r.path = path
	r.args = append([]string(nil), args...)
	return nil, nil
}
