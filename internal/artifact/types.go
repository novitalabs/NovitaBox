package artifact

type Image struct {
	ID         string
	RootfsPath string
}

type Template struct {
	ID           string
	RootfsPath   string
	MemfilePath  string
	SnapfilePath string
}

type Snapshot struct {
	ID           string
	SandboxID    string
	RootfsPath   string
	MemfilePath  string
	SnapfilePath string
}
