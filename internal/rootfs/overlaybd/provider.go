package overlaybd

import (
	"context"
	"fmt"
)

const ProviderName = "overlaybd"

type Mount struct {
	Type    string
	Source  string
	Options []string
}

type ResolvedImage struct {
	Reference string
	Parent    string
}

type PrepareRequest struct {
	SandboxID string
	SourceRef string
	Target    string
}

type Handle struct {
	SnapshotKey  string
	SourceRef    string
	SourceDigest string
	Parent       string
	Target       string
}

type Backend interface {
	EnsureImage(ctx context.Context, sourceRef string) (ResolvedImage, error)
	Prepare(ctx context.Context, key string, parent string, labels map[string]string) ([]Mount, error)
	Mounts(ctx context.Context, key string) ([]Mount, error)
	Remove(ctx context.Context, key string) error
	Close() error
}

type Mounter interface {
	Mount(mounts []Mount, target string) error
	Unmount(target string) error
}

type Provider struct {
	backend Backend
	mounter Mounter
}

func NewProvider(backend Backend, mounter Mounter) *Provider {
	return &Provider{backend: backend, mounter: mounter}
}

func SnapshotKey(sandboxID string) string {
	return "novitabox-sandbox-" + sandboxID
}

func (p *Provider) Prepare(ctx context.Context, req PrepareRequest) (Handle, error) {
	if req.SandboxID == "" || req.SourceRef == "" || req.Target == "" {
		return Handle{}, fmt.Errorf("sandbox id, source ref, and target are required")
	}
	resolved, err := p.backend.EnsureImage(ctx, req.SourceRef)
	if err != nil {
		return Handle{}, fmt.Errorf("ensure overlaybd image %q: %w", req.SourceRef, err)
	}
	key := SnapshotKey(req.SandboxID)
	mounts, err := p.backend.Prepare(ctx, key, resolved.Parent, map[string]string{
		"novitabox.io/sandbox-id": req.SandboxID,
	})
	if err != nil {
		return Handle{}, fmt.Errorf("prepare overlaybd snapshot %q: %w", key, err)
	}
	if err := p.mounter.Mount(mounts, req.Target); err != nil {
		_ = p.backend.Remove(context.Background(), key)
		return Handle{}, fmt.Errorf("mount overlaybd snapshot %q: %w", key, err)
	}
	return Handle{
		SnapshotKey:  key,
		SourceRef:    req.SourceRef,
		SourceDigest: resolved.Reference,
		Parent:       resolved.Parent,
		Target:       req.Target,
	}, nil
}

func (p *Provider) Mount(ctx context.Context, handle Handle) error {
	mounts, err := p.backend.Mounts(ctx, handle.SnapshotKey)
	if err != nil {
		return fmt.Errorf("get overlaybd snapshot %q mounts: %w", handle.SnapshotKey, err)
	}
	if err := p.mounter.Mount(mounts, handle.Target); err != nil {
		return fmt.Errorf("mount overlaybd snapshot %q: %w", handle.SnapshotKey, err)
	}
	return nil
}

func (p *Provider) Unmount(_ context.Context, handle Handle) error {
	if err := p.mounter.Unmount(handle.Target); err != nil {
		return fmt.Errorf("unmount overlaybd snapshot %q: %w", handle.SnapshotKey, err)
	}
	return nil
}

func (p *Provider) Remove(ctx context.Context, handle Handle) error {
	if err := p.Unmount(ctx, handle); err != nil {
		return err
	}
	if err := p.backend.Remove(ctx, handle.SnapshotKey); err != nil {
		return fmt.Errorf("remove overlaybd snapshot %q: %w", handle.SnapshotKey, err)
	}
	return nil
}

func (p *Provider) Close() error {
	return p.backend.Close()
}
