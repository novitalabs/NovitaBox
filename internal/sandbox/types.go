package sandbox

type State string

const (
	StateCreating  State = "creating"
	StateRunning   State = "running"
	StatePausing   State = "pausing"
	StatePaused    State = "paused"
	StateResuming  State = "resuming"
	StateStopping  State = "stopping"
	StateStopped   State = "stopped"
	StateStarting  State = "starting"
	StateRebooting State = "rebooting"
	StateKilling   State = "killing"
	StateKilled    State = "killed"
	StateFailed    State = "failed"
	StateUnknown   State = "unknown"
)

type Info struct {
	ID    string
	State State
}
