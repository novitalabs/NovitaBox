package server

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
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
	return m.SpecForSlot(sandboxID, 1)
}

func (m sandboxNetworkManager) SpecForSlot(sandboxID string, slot uint32) (*novitaboxv1.NetworkSpec, error) {
	if !m.cfg.Network.Enabled {
		return nil, nil
	}
	if slot == 0 {
		return nil, fmt.Errorf("network slot is required")
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
		Slot:          slot,
	}, nil
}

func (m sandboxNetworkManager) Complete(sandboxID string, slot uint32, spec *novitaboxv1.NetworkSpec) (*novitaboxv1.NetworkSpec, error) {
	defaults, err := m.SpecForSlot(sandboxID, slot)
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
	if spec.Slot == 0 {
		spec.Slot = defaults.GetSlot()
	}
	return spec, nil
}

func (m sandboxNetworkManager) MaxSlots() (uint32, error) {
	hostAccessSlots, err := networkSlotsForCIDR(m.cfg.Network.HostAccessCIDR, 1)
	if err != nil {
		return 0, err
	}
	vethSlots, err := networkSlotsForCIDR(m.cfg.Network.VethCIDR, vethAddressesPerSlot)
	if err != nil {
		return 0, err
	}
	if hostAccessSlots < vethSlots {
		return hostAccessSlots, nil
	}
	return vethSlots, nil
}

func (m sandboxNetworkManager) Prepare(ctx context.Context, runtimeType novitaboxv1.RuntimeType, spec *novitaboxv1.NetworkSpec) error {
	if spec == nil || spec.GetNamespaceName() == "" {
		return nil
	}

	hostVeth := hostVethName(spec.GetNamespaceName())
	nsVeth := nsVethName(spec.GetNamespaceName())
	nsPeerVeth := nsPeerVethName(spec.GetNamespaceName())
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
	_ = runCommand(ctx, "ip", "link", "del", nsPeerVeth)
	_ = runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "link", "del", nsVeth)
	_ = runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "link", "del", spec.GetTapName())
	if err := runCommand(ctx, "ip", "link", "add", hostVeth, "type", "veth", "peer", "name", nsPeerVeth); err != nil {
		return fmt.Errorf("create veth pair: %w", err)
	}
	if err := runCommand(ctx, "ip", "link", "set", nsPeerVeth, "netns", spec.GetNamespaceName()); err != nil {
		return fmt.Errorf("move veth to netns: %w", err)
	}
	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "link", "set", nsPeerVeth, "name", nsVeth); err != nil {
		return fmt.Errorf("rename namespace veth: %w", err)
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

	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "route", "replace", "default", "via", veth.hostIP); err != nil {
		return fmt.Errorf("configure namespace default route: %w", err)
	}
	if err := runCommand(ctx, "ip", "route", "replace", spec.GetHostAccessIp()+"/32", "via", veth.peerIP, "dev", hostVeth); err != nil {
		return fmt.Errorf("configure host access route: %w", err)
	}

	switch runtimeType {
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER:
		if err := m.prepareGVisorNamespace(ctx, spec); err != nil {
			return err
		}
	default:
		if err := m.prepareFirecrackerNamespace(ctx, spec); err != nil {
			return err
		}
	}
	if err := m.prepareHostInternetNAT(ctx); err != nil {
		return err
	}

	return nil
}

func (m sandboxNetworkManager) Cleanup(ctx context.Context, sandboxID string, slot uint32) error {
	spec, err := m.SpecForSlot(sandboxID, slot)
	if err != nil || spec == nil {
		return err
	}
	_ = m.removeHostInternetNAT(ctx)
	_ = m.removeNAT(ctx, spec)
	_ = runCommand(ctx, "ip", "route", "del", spec.GetHostAccessIp()+"/32")
	_ = runCommand(ctx, "ip", "link", "del", hostVethName(spec.GetNamespaceName()))
	_ = runCommand(ctx, "ip", "netns", "del", spec.GetNamespaceName())
	return nil
}

func (m sandboxNetworkManager) ensureNAT(ctx context.Context, spec *novitaboxv1.NetworkSpec) error {
	if err := ensureIPTablesRule(ctx, spec.GetNamespaceName(), "nat", "PREROUTING", dnatRuleArgs(spec)); err != nil {
		return fmt.Errorf("configure sandbox DNAT: %w", err)
	}
	if err := ensureIPTablesRule(ctx, spec.GetNamespaceName(), "nat", "POSTROUTING", snatRuleArgs(spec)); err != nil {
		return fmt.Errorf("configure sandbox SNAT: %w", err)
	}
	if err := ensureIPTablesRule(ctx, spec.GetNamespaceName(), "nat", "POSTROUTING", vethSnatRuleArgs(m.cfg.Network.VethCIDR, spec)); err != nil {
		return fmt.Errorf("configure sandbox veth SNAT: %w", err)
	}
	return nil
}

func (m sandboxNetworkManager) prepareFirecrackerNamespace(ctx context.Context, spec *novitaboxv1.NetworkSpec) error {
	// Firecracker keeps the guest IP inside the VM, so the netns needs tap0
	// plus the fixed guest-facing /30.
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
	if err := m.ensureNAT(ctx, spec); err != nil {
		return err
	}
	return nil
}

func (m sandboxNetworkManager) prepareGVisorNamespace(ctx context.Context, spec *novitaboxv1.NetworkSpec) error {
	// gVisor runs the agent as a process inside the namespace, so we expose the
	// agent IP directly on eth0 so the host access route can hit the process
	// without an extra DNAT hop.
	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "addr", "replace", spec.GetHostAccessIp()+"/32", "dev", nsVethName(spec.GetNamespaceName())); err != nil {
		return fmt.Errorf("configure gvisor host access address: %w", err)
	}
	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "addr", "replace", spec.GetGuestIp()+"/30", "dev", "lo"); err != nil {
		return fmt.Errorf("configure gvisor agent address: %w", err)
	}
	if err := runCommand(ctx, "ip", "netns", "exec", spec.GetNamespaceName(), "ip", "link", "set", "lo", "up"); err != nil {
		return fmt.Errorf("set namespace loopback up: %w", err)
	}
	if err := m.ensureNAT(ctx, spec); err != nil {
		return err
	}
	return nil
}

func (m sandboxNetworkManager) removeNAT(ctx context.Context, spec *novitaboxv1.NetworkSpec) error {
	dnatErr := deleteIPTablesRule(ctx, spec.GetNamespaceName(), "nat", "PREROUTING", dnatRuleArgs(spec))
	snatErr := deleteIPTablesRule(ctx, spec.GetNamespaceName(), "nat", "POSTROUTING", snatRuleArgs(spec))
	vethSnatErr := deleteIPTablesRule(ctx, spec.GetNamespaceName(), "nat", "POSTROUTING", vethSnatRuleArgs(m.cfg.Network.VethCIDR, spec))
	if dnatErr != nil {
		return dnatErr
	}
	if vethSnatErr != nil {
		return vethSnatErr
	}
	return snatErr
}

func (m sandboxNetworkManager) prepareHostInternetNAT(ctx context.Context) error {
	dev, err := hostDefaultRouteDev(ctx)
	if err != nil {
		return err
	}
	if err := runCommand(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("enable host ip forwarding: %w", err)
	}
	for _, cidr := range []string{m.cfg.Network.VethCIDR, m.cfg.Network.HostAccessCIDR} {
		if cidr == "" {
			continue
		}
		if err := ensureHostIPTablesRule(ctx, "nat", "POSTROUTING", []string{"-s", cidr, "-o", dev, "-j", "MASQUERADE"}); err != nil {
			return fmt.Errorf("configure host masquerade for %s: %w", cidr, err)
		}
		if err := ensureHostIPTablesRule(ctx, "filter", "FORWARD", []string{"-s", cidr, "-o", dev, "-j", "ACCEPT"}); err != nil {
			return fmt.Errorf("configure host forward outbound for %s: %w", cidr, err)
		}
		if err := ensureHostIPTablesRule(ctx, "filter", "FORWARD", []string{"-d", cidr, "-i", dev, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"}); err != nil {
			return fmt.Errorf("configure host forward inbound for %s: %w", cidr, err)
		}
	}
	return nil
}

func (m sandboxNetworkManager) removeHostInternetNAT(ctx context.Context) error {
	dev, err := hostDefaultRouteDev(ctx)
	if err != nil {
		return err
	}
	for _, cidr := range []string{m.cfg.Network.HostAccessCIDR, m.cfg.Network.VethCIDR} {
		if cidr == "" {
			continue
		}
		_ = deleteHostIPTablesRule(ctx, "filter", "FORWARD", []string{"-d", cidr, "-i", dev, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"})
		_ = deleteHostIPTablesRule(ctx, "filter", "FORWARD", []string{"-s", cidr, "-o", dev, "-j", "ACCEPT"})
		_ = deleteHostIPTablesRule(ctx, "nat", "POSTROUTING", []string{"-s", cidr, "-o", dev, "-j", "MASQUERADE"})
	}
	return nil
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

func ensureHostIPTablesRule(ctx context.Context, table string, chain string, args []string) error {
	check := append([]string{"-t", table, "-C", chain}, args...)
	if err := runCommand(ctx, "iptables", check...); err == nil {
		return nil
	}
	add := append([]string{"-t", table, "-A", chain}, args...)
	return runCommand(ctx, "iptables", add...)
}

func deleteHostIPTablesRule(ctx context.Context, table string, chain string, args []string) error {
	del := append([]string{"-t", table, "-D", chain}, args...)
	return runCommand(ctx, "iptables", del...)
}

func dnatRuleArgs(spec *novitaboxv1.NetworkSpec) []string {
	return []string{"-i", nsVethName(spec.GetNamespaceName()), "-d", spec.GetHostAccessIp() + "/32", "-j", "DNAT", "--to-destination", spec.GetGuestIp()}
}

func snatRuleArgs(spec *novitaboxv1.NetworkSpec) []string {
	return []string{"-o", nsVethName(spec.GetNamespaceName()), "-s", spec.GetGuestIp(), "-j", "SNAT", "--to-source", spec.GetHostAccessIp()}
}

func vethSnatRuleArgs(cidr string, spec *novitaboxv1.NetworkSpec) []string {
	addrs, err := vethAddrsForSlot(cidr, spec.GetSlot())
	if err != nil {
		return []string{"-j", "RETURN"}
	}
	return []string{"-o", nsVethName(spec.GetNamespaceName()), "-s", addrs.peerIP, "-j", "SNAT", "--to-source", spec.GetHostAccessIp()}
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

func networkSlotsForCIDR(cidr string, addressesPerSlot uint32) (uint32, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, fmt.Errorf("parse network cidr %q: %w", cidr, err)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		return 0, fmt.Errorf("network cidr %q must be IPv4", cidr)
	}
	totalIPs := uint32(1) << uint(32-ones)
	totalSlots := totalIPs / addressesPerSlot
	if totalSlots <= 1 {
		return 0, fmt.Errorf("network cidr %q is too small for sandbox slots", cidr)
	}
	return totalSlots - 1, nil
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
	return hashName("nb-", sandboxID)
}

func hostVethName(netns string) string {
	return hashName("vh-", netns)
}

func nsVethName(netns string) string {
	return "eth0"
}

func nsPeerVethName(netns string) string {
	return hashName("vp-", netns)
}

func hashName(prefix string, values ...string) string {
	h := sha1.New()
	for _, value := range values {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return prefix + hex.EncodeToString(sum[:5])
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

func hostDefaultRouteDev(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "ip", "route", "show", "default")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("show host default route: %w: %s", err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		for i := 0; i < len(fields)-1; i++ {
			if fields[i] == "dev" && fields[i+1] != "" {
				return fields[i+1], nil
			}
		}
	}
	return "", fmt.Errorf("host default route device not found in %q", strings.TrimSpace(string(out)))
}
