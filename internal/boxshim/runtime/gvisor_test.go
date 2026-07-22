package runtime

import (
	"os"
	"testing"

	"github.com/novitalabs/NovitaBox/internal/config"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
)

func TestNewGVisorOCISpecDoesNotInjectHostProxy(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:6789")
	t.Setenv("HTTPS_PROXY", "http://localhost:7890")
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:7891")
	t.Setenv("NO_PROXY", "example.com")

	spec, err := newGVisorOCISpec(config.Default(), gvisorSpecForTest("sbx-test", "10.11.0.9"))
	if err != nil {
		t.Fatalf("new gvisor oci spec: %v", err)
	}
	env := envMap(spec.Process.Env)

	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		if got := env[key]; got != "" {
			t.Fatalf("%s = %q, want no inherited proxy env", key, got)
		}
	}
}

func TestNewGVisorOCISpecDoesNotInjectProxyForTemplateBuild(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:6789")
	t.Setenv("HTTPS_PROXY", "http://localhost:7890")
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:7891")

	spec, err := newGVisorOCISpec(config.Default(), gvisorSpecForTest("template-build-tpl-test", "10.11.0.1"))
	if err != nil {
		t.Fatalf("new gvisor oci spec: %v", err)
	}
	env := envMap(spec.Process.Env)

	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		if got := env[key]; got != "" {
			t.Fatalf("%s = %q, want no inherited proxy env", key, got)
		}
	}
}

func TestNewGVisorOCISpecInjectsNvidiaDevicesWhenGpuEnabled(t *testing.T) {
	oldStat := statNvidiaDevice
	t.Cleanup(func() {
		statNvidiaDevice = oldStat
	})

	statNvidiaDevice = func(path string) (nvidiaDeviceStat, error) {
		switch path {
		case "/dev/nvidiactl":
			return nvidiaDeviceStat{major: 195, minor: 255, mode: 0o666, uid: 0, gid: 0}, nil
		case "/dev/nvidia-uvm":
			return nvidiaDeviceStat{major: 511, minor: 0, mode: 0o666, uid: 0, gid: 0}, nil
		case "/dev/nvidia0":
			return nvidiaDeviceStat{major: 195, minor: 0, mode: 0o666, uid: 0, gid: 0}, nil
		default:
			return nvidiaDeviceStat{}, os.ErrNotExist
		}
	}

	spec, err := newGVisorOCISpec(config.Default(), func() *novitaboxv1.RuntimeSpec {
		spec := gvisorSpecForTest("sbx-gpu", "10.11.0.9")
		spec.Machine.Gpu = 1
		return spec
	}())
	if err != nil {
		t.Fatalf("new gvisor oci spec: %v", err)
	}

	env := envMap(spec.Process.Env)
	if got := env["NVIDIA_VISIBLE_DEVICES"]; got != "0" {
		t.Fatalf("NVIDIA_VISIBLE_DEVICES = %q, want 0", got)
	}
	if got := env["CUDA_VISIBLE_DEVICES"]; got != "0" {
		t.Fatalf("CUDA_VISIBLE_DEVICES = %q, want 0", got)
	}
	if got := env["NVIDIA_DRIVER_CAPABILITIES"]; got != "compute,utility" {
		t.Fatalf("NVIDIA_DRIVER_CAPABILITIES = %q, want compute,utility", got)
	}
	if got := env["LD_LIBRARY_PATH"]; got != "/usr/lib/x86_64-linux-gnu:/usr/lib/x86_64-linux-gnu/vdpau:/usr/lib64" {
		t.Fatalf("LD_LIBRARY_PATH = %q, want NVIDIA library paths", got)
	}
	if len(spec.Linux.Devices) != 3 {
		t.Fatalf("linux.devices len = %d, want 3", len(spec.Linux.Devices))
	}
	if len(spec.Linux.Resources.Devices) != 3 {
		t.Fatalf("linux.resources.devices len = %d, want 3", len(spec.Linux.Resources.Devices))
	}
}

func TestMergeCDIContainerEdits(t *testing.T) {
	major := int64(195)
	minor := int64(0)
	mode := uint32(0o666)
	uid := uint32(0)
	gid := uint32(0)
	dst := cdiNvidiaEdits{}

	mergeCDIContainerEdits(&dst, &cdiContainerEdits{
		Env: []string{"NVIDIA_VISIBLE_DEVICES=0"},
		DeviceNodes: []cdiDeviceNode{
			{
				Path:     "/dev/nvidia0",
				Type:     "c",
				Major:    &major,
				Minor:    &minor,
				FileMode: &mode,
				UID:      &uid,
				GID:      &gid,
			},
		},
		Mounts: []cdiMount{
			{HostPath: "/host/lib", ContainerPath: "/usr/lib/libnvidia.so", Options: []string{"ro"}},
		},
		Hooks: []cdiHook{
			{HookName: "createContainer", Path: "/usr/bin/nvidia-ctk", Args: []string{"hook", "createContainer"}},
		},
	})

	if got := len(dst.env); got != 1 {
		t.Fatalf("env len = %d, want 1", got)
	}
	if got := len(dst.devices); got != 1 {
		t.Fatalf("devices len = %d, want 1", got)
	}
	if got := dst.devices[0].Path; got != "/dev/nvidia0" {
		t.Fatalf("device path = %q, want /dev/nvidia0", got)
	}
	if got := len(dst.rules); got != 1 {
		t.Fatalf("rules len = %d, want 1", got)
	}
	if got := len(dst.mounts); got != 1 {
		t.Fatalf("mounts len = %d, want 1", got)
	}
	if got := len(dst.hooks.Prestart); got != 1 {
		t.Fatalf("hooks len = %d, want 1", got)
	}
}

func TestMergeCDIContainerEditsSkipsUpdateLdcacheHook(t *testing.T) {
	dst := cdiNvidiaEdits{}

	mergeCDIContainerEdits(&dst, &cdiContainerEdits{
		Hooks: []cdiHook{
			{HookName: "createContainer", Path: "/usr/bin/nvidia-cdi-hook", Args: []string{"nvidia-cdi-hook", "update-ldcache", "--folder", "/usr/lib/x86_64-linux-gnu"}},
			{HookName: "createContainer", Path: "/usr/bin/nvidia-cdi-hook", Args: []string{"nvidia-cdi-hook", "enable-cuda-compat"}},
		},
	})

	if dst.hooks == nil {
		t.Fatalf("hooks should contain non-ldcache hook")
	}
	if got := len(dst.hooks.Prestart); got != 1 {
		t.Fatalf("hooks len = %d, want 1", got)
	}
	if got := dst.hooks.Prestart[0].Args[1]; got != "enable-cuda-compat" {
		t.Fatalf("hook = %q, want enable-cuda-compat", got)
	}
}

func TestMergeCDIContainerEditsAddsNvidiaLibrarySymlinkHook(t *testing.T) {
	dst := cdiNvidiaEdits{}

	mergeCDIContainerEdits(&dst, &cdiContainerEdits{
		Mounts: []cdiMount{
			{HostPath: "/host/libcuda.so.570.124.06", ContainerPath: "/usr/lib/x86_64-linux-gnu/libcuda.so.570.124.06"},
			{HostPath: "/host/libnvidia-ml.so.570.124.06", ContainerPath: "/usr/lib/x86_64-linux-gnu/libnvidia-ml.so.570.124.06"},
		},
	})

	if dst.hooks == nil || len(dst.hooks.Prestart) != 1 {
		t.Fatalf("hooks = %#v, want generated symlink hook", dst.hooks)
	}
	args := dst.hooks.Prestart[0].Args
	for _, want := range []string{
		"libcuda.so.570.124.06::/usr/lib/x86_64-linux-gnu/libcuda.so.1",
		"libnvidia-ml.so.570.124.06::/usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1",
		"libnvidia-ml.so.1::/usr/lib/x86_64-linux-gnu/libnvidia-ml.so",
	} {
		if !containsString(args, want) {
			t.Fatalf("symlink hook args = %#v, missing %q", args, want)
		}
	}
}

func TestMergeCDIContainerEditsUsesHostDeviceStatWhenMetadataMissing(t *testing.T) {
	dst := cdiNvidiaEdits{}

	mergeCDIContainerEdits(&dst, &cdiContainerEdits{
		DeviceNodes: []cdiDeviceNode{
			{
				Path: "/dev/null",
				Type: "c",
			},
		},
	})

	if got := len(dst.rules); got != 1 {
		t.Fatalf("rules len = %d, want 1", got)
	}
	if dst.rules[0].Major == nil || dst.rules[0].Minor == nil {
		t.Fatalf("rules major/minor should be populated from host device stat: %#v", dst.rules[0])
	}
}

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HTTP_PROXY", "http_proxy",
		"HTTPS_PROXY", "https_proxy",
		"ALL_PROXY", "all_proxy",
		"NO_PROXY", "no_proxy",
	} {
		t.Setenv(key, "")
	}
}

func gvisorSpecForTest(sandboxID string, hostAccessIP string) *novitaboxv1.RuntimeSpec {
	return &novitaboxv1.RuntimeSpec{
		SandboxId: sandboxID,
		Machine: &novitaboxv1.MachineSpec{
			Vcpu:     1,
			MemoryMb: 512,
		},
		Rootfs: &novitaboxv1.RootfsSpec{
			Path: "/tmp/rootfs",
		},
		Network: &novitaboxv1.NetworkSpec{
			NamespaceName: "nb-test",
			HostAccessIp:  hostAccessIP,
			Slot:          1,
		},
	}
}

func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		for i, ch := range entry {
			if ch == '=' {
				out[entry[:i]] = entry[i+1:]
				break
			}
		}
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
