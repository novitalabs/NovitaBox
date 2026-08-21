package store

import (
	"context"
	"errors"
	"time"

	"github.com/novitalabs/NovitaBox/internal/sandbox"
)

var ErrNotFound = errors.New("record not found")

type SandboxRecord struct {
	ID                 string
	State              sandbox.State
	RuntimeType        string
	TemplateID         string
	ImageID            string
	SnapshotID         string
	NetworkSlot        uint32
	RootfsProvider     string
	RootfsSourceRef    string
	RootfsSourceDigest string
	RootfsSnapshotKey  string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ImageRecord struct {
	ID         string
	RootfsPath string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type TemplateRecord struct {
	ID           string
	RootfsPath   string
	MemfilePath  string
	SnapfilePath string
	Aliases      []string
	Names        []string
	Metadata     map[string]string
	Public       bool
	CPUCount     int32
	MemoryMB     int32
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SnapshotRecord struct {
	ID           string
	SandboxID    string
	RootfsPath   string
	MemfilePath  string
	SnapfilePath string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type TemplateBuildStatus string

const (
	TemplateBuildStatusWaiting  TemplateBuildStatus = "waiting"
	TemplateBuildStatusBuilding TemplateBuildStatus = "building"
	TemplateBuildStatusReady    TemplateBuildStatus = "ready"
	TemplateBuildStatusError    TemplateBuildStatus = "error"
)

type TemplateBuildRecord struct {
	ID         string
	TemplateID string
	Status     TemplateBuildStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type TransitionRecord struct {
	ID           int64
	ResourceType string
	ResourceID   string
	FromState    string
	ToState      string
	Action       string
	CreatedAt    time.Time
}

type Store interface {
	Close() error

	CreateSandbox(ctx context.Context, record SandboxRecord) error
	GetSandbox(ctx context.Context, sandboxID string) (*SandboxRecord, error)
	ListSandboxes(ctx context.Context) ([]SandboxRecord, error)
	UpdateSandboxState(ctx context.Context, sandboxID string, from, to sandbox.State, action string) error
	AssignSandboxNetworkSlot(ctx context.Context, sandboxID string, maxSlot uint32) (uint32, error)
	ReleaseSandboxNetworkSlot(ctx context.Context, sandboxID string) error
	UpdateSandboxRootfsDigest(ctx context.Context, sandboxID string, sourceDigest string) error
	DeleteSandbox(ctx context.Context, sandboxID string) error

	CreateImage(ctx context.Context, record ImageRecord) error
	GetImage(ctx context.Context, imageID string) (*ImageRecord, error)
	ListImages(ctx context.Context) ([]ImageRecord, error)
	DeleteImage(ctx context.Context, imageID string) error

	CreateTemplate(ctx context.Context, record TemplateRecord) error
	GetTemplate(ctx context.Context, templateID string) (*TemplateRecord, error)
	ListTemplates(ctx context.Context) ([]TemplateRecord, error)
	DeleteTemplate(ctx context.Context, templateID string) error

	CreateTemplateBuild(ctx context.Context, record TemplateBuildRecord) error
	GetTemplateBuild(ctx context.Context, templateID string, buildID string) (*TemplateBuildRecord, error)
	GetLatestTemplateBuild(ctx context.Context, templateID string) (*TemplateBuildRecord, error)
	ListTemplateBuilds(ctx context.Context, templateID string) ([]TemplateBuildRecord, error)
	UpdateTemplateBuildStatus(ctx context.Context, templateID string, buildID string, from TemplateBuildStatus, to TemplateBuildStatus) error

	CreateSnapshot(ctx context.Context, record SnapshotRecord) error
	GetSnapshot(ctx context.Context, snapshotID string) (*SnapshotRecord, error)
	ListSnapshotsBySandbox(ctx context.Context, sandboxID string) ([]SnapshotRecord, error)
	DeleteSnapshot(ctx context.Context, snapshotID string) error

	ListTransitions(ctx context.Context, resourceType string, resourceID string) ([]TransitionRecord, error)
}
