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

type TemplateBuildConfig struct {
	KernelPath       string
	KernelArgs       []string
	SnapshotEnabled  bool
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
		Boxshim:     ShimConfig{RuntimeDriver: "stub", BinaryPath: "boxshim"},
		Boxd:        ServiceConfig{Addr: "10.88.0.2:49983"},
		Storage:     StorageConfig{},
		Firecracker: FirecrackerConfig{BinaryPath: "firecracker"},
		Template: TemplateBuildConfig{
			SnapshotEnabled:  false,
			SnapshotWaitSecs: 3,
			AgentWaitSecs:    30,
			BoxdGuestPath:    "/novitabox/boxd",
			BoxdGuestAddr:    "0.0.0.0:49983",
			VCPU:             1,
			MemoryMB:         512,
		},
	}
}
