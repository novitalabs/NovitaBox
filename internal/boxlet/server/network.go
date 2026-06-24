package server

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"os/exec"
	"strings"

	"github.com/novitalabs/NovitaBox/internal/config"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
)

const (
	sandboxTapName       = "tap0"
	vethMask             = 31
	vethAddressesPerSlot = 2
	hostAccessMask       = 32
	tapMask              = 30
)

type sandboxNetworkManager struct {
	cfg config.Config
}

func newSandboxNetworkManager(cfg config.Config) sandboxNetworkManager {
	return sandboxNetworkManager{cfg: cfg}
}

func (m sandboxNetworkManager) Spec(sandboxID string) (*novitaboxv1.NetworkSpec, error) {
	if !m.cfg.Network.Enabled {
		return nil, nil
	}
	slot, err := networkSlotForSandbox(m.cfg.Network.VethCIDR, sandboxID)
	if err != nil {
		return nil, err
	}
	hostAccessIP, err := indexedIP(m.cfg.Network.HostAccessCIDR, slot)
	if err != nil {
		return nil, fmt.Errorf("allocate host access ip: %w", err)
	}
	return &novitaboxv1.NetworkSpec{
		NamespaceName: sandboxNetNSName(sandboxID),
		TapName:       sandboxTapName,
		GuestIp:       m.cfg.Network.GuestIP,
		GatewayIp:     m.cfg.Network.GatewayIP,
		HostAccessIp:  hostAccessIP,
		Mac:           m.cfg.Network.GuestMAC,
	}, nil
}

func (m sandboxNetworkManager) Complete(sandboxID string, spec *novitaboxv1.NetworkSpec) (*novitaboxv1.NetworkSpec, error) {
	defaults, err := m.Spec(sandboxID)
	if err != nil || defaults == nil {
		return defaults, err
	}
	if spec == nil {
		return defaults, nil
	}
	if spec.NamespaceName == "" {
		spec.NamespaceName = defaults.GetNamespaceName()
	}
	if spec.TapName == "" {
		spec.TapName = defaults.GetTapName()
	}
	if spec.GuestIp == "" {
		spec.GuestIp = defaults.GetGuestIp()
	}
	if spec.GatewayIp == "" {
		spec.GatewayIp = defaults.GetGatewayIp()
	}
	if spec.HostAccessIp == "" {
		spec.HostAccessIp = defaults.GetHostAccessIp()
	}
	if spec.Mac == "" {
		spec.Mac = defaults.GetMac()
	}
	return spec, nil
}

func (m sandboxNetworkManager) Ensure(ctx context.Context, spec *novitaboxv1.NetworkSpec) error {
	if spec == nil || spec.GetNamespaceName() == "" {
		return nil
	}

	hostVeth := hostVethName(spec.GetNamespaceName())
	nsVeth := nsVethName(spec.GetNamespaceName())
	slot, err := networkSlotFromHostAccessIP(m.cfg.Network.HostAccessCIDR, spec.GetHostAccessIp())
	if err != nil {
		return err
	}
	veth, err := vethAddrsForSlot(m.cfg.Network.VethCIDR, slot)
	if err != nil {
		return err
	}

	if err := runCommand(ctx, "ip", "netns", "add", spec.GetNamespaceName()); err != nil && !commandOutputContains(err, "File exists") {
		return fmt.Errorf("create netns %s: %w", spec.GetNamespaceName(), err)
	}
	_ = runCommand(ctx, "ip", "link", "del", hostVeth)

	if err := runCommand(ctx, "ip", "link", "add", hostVeth, "type", "veth", "peer", "name", nsVeth); err != nil {
		return fmt.Errorf("create veth pair: %w", err)
	}
	if err := runCommand(ctx, "ip", "link", "set", nsVeth, "netns", spec.GetNamespaceName()); err != nil {
		return fmt.Errorf("move veth to netns: %w", err)
	}
	if err := runCommand(ctx, "ip", "addr", "replace", veth.hostCIDR(), "dev", hostVeth); err != nil {
		return fmt.Errorf("configure host veth: %w", err)
	}
	if err := runCommand(ctx, "ip", "link", "set", hostVeth, "up"); err != nil {
		return fmt.Errorf("set host veth up: %w", err)
	}
	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "addr", "replace", veth.peerCIDR(), "dev", nsVeth); err != nil {
		return fmt.Errorf("configure namespace veth: %w", err)
	}
	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "link", "set", nsVeth, "up"); err != nil {
		return fmt.Errorf("set namespace veth up: %w", err)
	}
	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "link", "set", "lo", "up"); err != nil {
		return fmt.Errorf("set namespace loopback up: %w", err)
	}
	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("enable namespace ip forwarding: %w", err)
	}

	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "tuntap", "add", "dev", spec.GetTapName(), "mode", "tap"); err != nil && !commandOutputContains(err, "File exists") {
		return fmt.Errorf("create tap %s: %w", spec.GetTapName(), err)
	}
	gatewayCIDR := spec.GetGatewayIp() + "/30"
	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "addr", "replace", gatewayCIDR, "dev", spec.GetTapName()); err != nil {
		return fmt.Errorf("configure tap gateway: %w", err)
	}
	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "link", "set", spec.GetTapName(), "up"); err != nil {
		return fmt.Errorf("set tap up: %w", err)
	}
	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "route", "replace", "default", "via", veth.hostIP); err != nil {
		return fmt.Errorf("configure namespace default route: %w", err)
	}

	if err := runCommand(ctx, "ip", "route", "replace", spec.GetHostAccessIp()+"/32", "via", veth.peerIP); err != nil {
		return fmt.Errorf("configure host access route: %w", err)
	}
	if err := ensureNAT(ctx, spec); err != nil {
		return err
	}

	return nil
}

func (m sandboxNetworkManager) Cleanup(ctx context.Context, sandboxID string) error {
	spec, err := m.Spec(sandboxID)
	if err != nil || spec == nil {
		return err
	}
	_ = removeNAT(ctx, spec)
	_ = runCommand(ctx, "ip", "route", "del", spec.GetHostAccessIp()+"/32")
	_ = runCommand(ctx, "ip", "link", "del", hostVethName(spec.GetNamespaceName()))
	_ = runCommand(ctx, "ip", "netns", "del", spec.GetNamespaceName())
	return nil
}

func ensureNAT(ctx context.Context, spec *novitaboxv1.NetworkSpec) error {
	if err := ensureIPTablesRule(ctx, spec.GetNamespaceName(), "nat", "PREROUTING", dnatRuleArgs(spec)); err != nil {
		return fmt.Errorf("configure sandbox DNAT: %w", err)
	}
	if err := ensureIPTablesRule(ctx, spec.GetNamespaceName(), "nat", "POSTROUTING", snatRuleArgs(spec)); err != nil {
		return fmt.Errorf("configure sandbox SNAT: %w", err)
	}
	return nil
}

func removeNAT(ctx context.Context, spec *novitaboxv1.NetworkSpec) error {
	dnatErr := deleteIPTablesRule(ctx, spec.GetNamespaceName(), "nat", "PREROUTING", dnatRuleArgs(spec))
	snatErr := deleteIPTablesRule(ctx, spec.GetNamespaceName(), "nat", "POSTROUTING", snatRuleArgs(spec))
	if dnatErr != nil {
		return dnatErr
	}
	return snatErr
}

func ensureIPTablesRule(ctx context.Context, netns string, table string, chain string, args []string) error {
	check := append([]string{"-t", table, "-C", chain}, args...)
	if err := runCommand(ctx, "ip", append([]string{"netns", "exec", netns, "iptables"}, check...)...); err == nil {
		return nil
	}
	add := append([]string{"-t", table, "-A", chain}, args...)
	return runCommand(ctx, "ip", append([]string{"netns", "exec", netns, "iptables"}, add...)...)
}

func deleteIPTablesRule(ctx context.Context, netns string, table string, chain string, args []string) error {
	del := append([]string{"-t", table, "-D", chain}, args...)
	return runCommand(ctx, "ip", append([]string{"netns", "exec", netns, "iptables"}, del...)...)
}

func dnatRuleArgs(spec *novitaboxv1.NetworkSpec) []string {
	return []string{"-i", nsVethName(spec.GetNamespaceName()), "-d", spec.GetHostAccessIp() + "/32", "-j", "DNAT", "--to-destination", spec.GetGuestIp()}
}

func snatRuleArgs(spec *novitaboxv1.NetworkSpec) []string {
	return []string{"-o", nsVethName(spec.GetNamespaceName()), "-s", spec.GetGuestIp(), "-j", "SNAT", "--to-source", spec.GetHostAccessIp()}
}

type sandboxVethAddrs struct {
	hostIP string
	peerIP string
}

func (a sandboxVethAddrs) hostCIDR() string {
	return fmt.Sprintf("%s/%d", a.hostIP, vethMask)
}

func (a sandboxVethAddrs) peerCIDR() string {
	return fmt.Sprintf("%s/%d", a.peerIP, vethMask)
}

func vethAddrsForSlot(cidr string, slot uint32) (sandboxVethAddrs, error) {
	hostIP, err := indexedIP(cidr, slot*vethAddressesPerSlot)
	if err != nil {
		return sandboxVethAddrs{}, fmt.Errorf("allocate host veth ip: %w", err)
	}
	peerIP, err := indexedIP(cidr, slot*vethAddressesPerSlot+1)
	if err != nil {
		return sandboxVethAddrs{}, fmt.Errorf("allocate namespace veth ip: %w", err)
	}
	return sandboxVethAddrs{hostIP: hostIP, peerIP: peerIP}, nil
}

func networkSlotForSandbox(cidr string, sandboxID string) (uint32, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, fmt.Errorf("parse network cidr %q: %w", cidr, err)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		return 0, fmt.Errorf("network cidr %q must be IPv4", cidr)
	}
	totalIPs := uint32(1) << uint(32-ones)
	totalSlots := totalIPs / vethAddressesPerSlot
	if totalSlots <= vethAddressesPerSlot {
		return 0, fmt.Errorf("network cidr %q is too small for sandbox slots", cidr)
	}
	hash := sha1.Sum([]byte(sandboxID))
	slot := (binary.BigEndian.Uint32(hash[:4]) % (totalSlots - vethAddressesPerSlot)) + 1
	return slot, nil
}

func networkSlotFromHostAccessIP(cidr string, hostAccessIP string) (uint32, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, fmt.Errorf("parse host access cidr %q: %w", cidr, err)
	}
	base := network.IP.To4()
	ip := net.ParseIP(hostAccessIP).To4()
	if base == nil || ip == nil {
		return 0, fmt.Errorf("host access cidr and ip must be IPv4")
	}
	if !network.Contains(ip) {
		return 0, fmt.Errorf("host access ip %s is outside cidr %s", hostAccessIP, cidr)
	}
	baseInt := binary.BigEndian.Uint32(base)
	ipInt := binary.BigEndian.Uint32(ip)
	if ipInt <= baseInt {
		return 0, fmt.Errorf("host access ip %s cannot be network base address", hostAccessIP)
	}
	return ipInt - baseInt, nil
}

func indexedIP(cidr string, index uint32) (string, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("parse cidr %q: %w", cidr, err)
	}
	ip4 := network.IP.To4()
	mask := network.Mask
	if ip4 == nil || len(mask) != net.IPv4len {
		return "", fmt.Errorf("cidr %q must be IPv4", cidr)
	}
	ones, bits := mask.Size()
	if bits != 32 {
		return "", fmt.Errorf("cidr %q must be IPv4", cidr)
	}
	totalIPs := uint32(1) << uint(32-ones)
	if index == 0 || index >= totalIPs {
		return "", fmt.Errorf("ip index %d is outside cidr %q", index, cidr)
	}
	base := binary.BigEndian.Uint32(ip4)
	out := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(out, base+index)
	return out.String(), nil
}

func sandboxNetNSName(sandboxID string) string {
	return shortName("nb-" + sandboxID)
}

func hostVethName(netns string) string {
	return shortName("vh-" + netns)
}

func nsVethName(netns string) string {
	return "eth0"
}

func shortName(name string) string {
	if len(name) <= 15 {
		return name
	}
	sum := sha1.Sum([]byte(name))
	return fmt.Sprintf("%s%x", name[:6], sum[:4])
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return commandError{name: name, args: args, output: string(out), err: err}
	}
	return nil
}

type commandError struct {
	name   string
	args   []string
	output string
	err    error
}

func (e commandError) Error() string {
	output := strings.TrimSpace(e.output)
	if output == "" {
		return fmt.Sprintf("%s %s: %v", e.name, strings.Join(e.args, " "), e.err)
	}
	return fmt.Sprintf("%s %s: %v: %s", e.name, strings.Join(e.args, " "), e.err, output)
}

func (e commandError) Unwrap() error {
	return e.err
}

func commandOutputContains(err error, needle string) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), needle)
}
