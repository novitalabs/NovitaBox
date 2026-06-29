package config

const DefaultRoot = "/var/lib/novitabox"

type Config struct {
	RootDir string

	BoxAPI      ServiceConfig
	Boxlet      ServiceConfig
	BoxProxy    ServiceConfig
	Boxshim     ShimConfig
	Boxd        ServiceConfig
	Storage     StorageConfig
	Firecracker FirecrackerConfig
	Template    TemplateBuildConfig
	Network     NetworkConfig
}

type ServiceConfig struct {
	Addr string
}

type ShimConfig struct {
	SocketPath    string
	RuntimeDriver string
	BinaryPath    string
}

type StorageConfig struct {
	DBPath string
}

type FirecrackerConfig struct {
	BinaryPath string
}

type NetworkConfig struct {
	Enabled        bool
	HostAccessCIDR string
	VethCIDR       string
	GuestIP        string
	GatewayIP      string
	GuestMAC       string
}

type TemplateBuildConfig struct {
	KernelPath       string
	KernelArgs       []string
	SnapshotWaitSecs uint32
	AgentHealthURL   string
	AgentExecURL     string
	AgentWaitSecs    uint32
	BoxdBinaryPath   string
	BoxdGuestPath    string
	BoxdGuestAddr    string
	VCPU             uint32
	MemoryMB         uint32
}

func Default() Config {
	return Config{
		RootDir:     DefaultRoot,
		BoxAPI:      ServiceConfig{Addr: "127.0.0.1:8080"},
		Boxlet:      ServiceConfig{Addr: "127.0.0.1:8081"},
		BoxProxy:    ServiceConfig{Addr: "127.0.0.1:8082"},
		Boxshim:     ShimConfig{RuntimeDriver: "firecracker"},
		Boxd:        ServiceConfig{Addr: "169.254.0.21:49983"},
		Storage:     StorageConfig{},
		Firecracker: FirecrackerConfig{},
		Network: NetworkConfig{
			Enabled:        true,
			HostAccessCIDR: "10.11.0.0/16",
			VethCIDR:       "10.12.0.0/16",
			GuestIP:        "169.254.0.21",
			GatewayIP:      "169.254.0.22",
			GuestMAC:       "02:FC:00:00:00:05",
		},
		Template: TemplateBuildConfig{
			SnapshotWaitSecs: 3,
			AgentWaitSecs:    30,
			BoxdGuestPath:    "/novitabox/agent/boxd",
			BoxdGuestAddr:    "0.0.0.0:49983",
			VCPU:             1,
			MemoryMB:         512,
		},
	}
}
