package server

import (
	"net"
	"testing"

	"github.com/novitalabs/NovitaBox/internal/config"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
)

func TestSandboxNetworkSpecDefaults(t *testing.T) {
	cfg := config.Default()
	manager := newSandboxNetworkManager(cfg)

	spec, err := manager.Spec("i-test-sandbox")
	if err != nil {
		t.Fatalf("Spec() error = %v", err)
	}
	if spec.GetNamespaceName() == "" || len(spec.GetNamespaceName()) > 15 {
		t.Fatalf("namespace = %q, want non-empty Linux interface-safe name", spec.GetNamespaceName())
	}
	if spec.GetTapName() != "tap0" {
		t.Fatalf("tap = %q, want tap0", spec.GetTapName())
	}
	if spec.GetGuestIp() != "169.254.0.21" {
		t.Fatalf("guest ip = %q, want 169.254.0.21", spec.GetGuestIp())
	}
	if spec.GetGatewayIp() != "169.254.0.22" {
		t.Fatalf("gateway ip = %q, want 169.254.0.22", spec.GetGatewayIp())
	}
	if spec.GetMac() != "02:FC:00:00:00:05" {
		t.Fatalf("mac = %q, want 02:FC:00:00:00:05", spec.GetMac())
	}
	if ip := net.ParseIP(spec.GetHostAccessIp()); ip == nil {
		t.Fatalf("host access ip = %q, want valid ip", spec.GetHostAccessIp())
	}
	if _, err := net.ParseMAC(spec.GetMac()); err != nil {
		t.Fatalf("mac = %q, want valid mac: %v", spec.GetMac(), err)
	}
}

func TestVethAddrsUseSlotPair(t *testing.T) {
	addrs, err := vethAddrsForSlot("10.12.0.0/16", 1)
	if err != nil {
		t.Fatalf("vethAddrsForSlot() error = %v", err)
	}
	if addrs.hostCIDR() != "10.12.0.2/31" {
		t.Fatalf("host cidr = %q, want 10.12.0.2/31", addrs.hostCIDR())
	}
	if addrs.peerCIDR() != "10.12.0.3/31" {
		t.Fatalf("peer cidr = %q, want 10.12.0.3/31", addrs.peerCIDR())
	}
}

func TestSandboxNetworkCompletePreservesExplicitValues(t *testing.T) {
	cfg := config.Default()
	manager := newSandboxNetworkManager(cfg)

	spec, err := manager.Complete("i-test-sandbox", &novitaboxv1.NetworkSpec{
		NamespaceName: "custom-ns",
		TapName:       "customtap",
		GuestIp:       "10.0.0.2",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if spec.GetNamespaceName() != "custom-ns" || spec.GetTapName() != "customtap" || spec.GetGuestIp() != "10.0.0.2" {
		t.Fatalf("spec = %#v, explicit fields were not preserved", spec)
	}
	if spec.GetGatewayIp() == "" || spec.GetHostAccessIp() == "" || spec.GetMac() == "" {
		t.Fatalf("spec = %#v, defaults were not filled", spec)
	}
}

func TestSandboxNetworkDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Network.Enabled = false
	spec, err := newSandboxNetworkManager(cfg).Spec("i-test-sandbox")
	if err != nil {
		t.Fatalf("Spec() error = %v", err)
	}
	if spec != nil {
		t.Fatalf("spec = %#v, want nil when network is disabled", spec)
	}
}
