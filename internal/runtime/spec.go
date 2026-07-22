package runtime

type Spec struct {
	SandboxID   string
	RuntimeType string
	Machine     MachineSpec
	Rootfs      RootfsSpec
	Snapshot    SnapshotSpec
	Network     NetworkSpec
	Agent       AgentSpec
}

type MachineSpec struct {
	VCPU     uint32
	MemoryMB uint32
	GPU      uint32
}

type RootfsSpec struct {
	Path     string
	Readonly bool
	Format   string
}

type SnapshotSpec struct {
	MemfilePath  string
	SnapfilePath string
	Type         string
}

type NetworkSpec struct {
	NamespaceName string
	TapName       string
	GuestIP       string
	GatewayIP     string
	HostAccessIP  string
	MAC           string
}

type AgentSpec struct {
	Type     string
	Protocol string
	Port     uint32
	Token    string
}

type Info struct {
	SandboxID   string
	RuntimeType string
	State       string
}

type Capabilities struct {
	StartFromImage    bool
	StartFromTemplate bool
	StartFromSnapshot bool
	Pause             bool
	Resume            bool
	FullSnapshot      bool
	DiffSnapshot      bool
	GPU               bool
}
