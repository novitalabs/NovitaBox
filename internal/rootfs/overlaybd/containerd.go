package overlaybd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	containerdclient "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/leases"
	containerdmount "github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/opencontainers/image-spec/identity"
)

type containerdBackend struct {
	client      *containerdclient.Client
	snapshotter snapshots.Snapshotter
	leases      leases.Manager
	namespace   string
	puller      *CtrPuller
}

func NewContainerdProvider(cfg config.OverlayBDConfig) (*Provider, error) {
	if cfg.SnapshotterSocket != "" {
		if _, err := os.Stat(cfg.SnapshotterSocket); err != nil {
			return nil, fmt.Errorf("stat overlaybd snapshotter socket %q: %w", cfg.SnapshotterSocket, err)
		}
	}
	client, err := containerdclient.New(cfg.ContainerdAddress)
	if err != nil {
		return nil, fmt.Errorf("connect containerd %q: %w", cfg.ContainerdAddress, err)
	}
	backend := &containerdBackend{
		client:      client,
		snapshotter: client.SnapshotService(cfg.Snapshotter),
		leases:      client.LeasesService(),
		namespace:   cfg.Namespace,
		puller: NewCtrPuller(CtrPullerConfig{
			BinaryPath:        cfg.CtrBinaryPath,
			ContainerdAddress: cfg.ContainerdAddress,
			Namespace:         cfg.Namespace,
			Snapshotter:       cfg.Snapshotter,
		}, nil),
	}
	return NewProvider(backend, containerdMounter{}), nil
}

func (b *containerdBackend) EnsureImage(ctx context.Context, sourceRef string) (ResolvedImage, error) {
	ctx = namespaces.WithNamespace(ctx, b.namespace)
	resolved, err := b.resolveImage(ctx, sourceRef)
	if err == nil {
		if _, statErr := b.snapshotter.Stat(ctx, resolved.Parent); statErr == nil {
			return resolved, nil
		} else if !errdefs.IsNotFound(statErr) {
			return ResolvedImage{}, fmt.Errorf("stat overlaybd image snapshot %q: %w", resolved.Parent, statErr)
		}
	} else if !errdefs.IsNotFound(err) {
		return ResolvedImage{}, err
	}
	if err := b.puller.Pull(ctx, sourceRef); err != nil {
		return ResolvedImage{}, err
	}
	return b.resolveImage(ctx, sourceRef)
}

func (b *containerdBackend) resolveImage(ctx context.Context, sourceRef string) (ResolvedImage, error) {
	image, err := b.client.GetImage(ctx, sourceRef)
	if err != nil {
		return ResolvedImage{}, err
	}
	diffIDs, err := image.RootFS(ctx)
	if err != nil {
		return ResolvedImage{}, fmt.Errorf("resolve image rootfs: %w", err)
	}
	if len(diffIDs) == 0 {
		return ResolvedImage{}, fmt.Errorf("image %q has an empty rootfs", sourceRef)
	}
	return ResolvedImage{
		Reference: image.Target().Digest.String(),
		Parent:    identity.ChainID(diffIDs).String(),
	}, nil
}

func (b *containerdBackend) Prepare(ctx context.Context, key string, parent string, labels map[string]string) ([]Mount, error) {
	ctx = namespaces.WithNamespace(ctx, b.namespace)
	lease, err := b.leases.Create(ctx, leases.WithID(key), leases.WithLabel("novitabox.io/sandbox", key))
	if err != nil && !errdefs.IsAlreadyExists(err) {
		return nil, fmt.Errorf("create lease %q: %w", key, err)
	}
	ctx = leases.WithLease(ctx, key)
	mounts, err := b.snapshotter.Prepare(ctx, key, parent, snapshots.WithLabels(labels))
	if err != nil {
		if lease.ID != "" {
			cleanupCtx := namespaces.WithNamespace(context.Background(), b.namespace)
			_ = b.leases.Delete(cleanupCtx, lease)
		}
		return nil, err
	}
	return fromContainerdMounts(mounts), nil
}

func (b *containerdBackend) Mounts(ctx context.Context, key string) ([]Mount, error) {
	ctx = namespaces.WithNamespace(ctx, b.namespace)
	mounts, err := b.snapshotter.Mounts(ctx, key)
	if err != nil {
		return nil, err
	}
	return fromContainerdMounts(mounts), nil
}

func (b *containerdBackend) Remove(ctx context.Context, key string) error {
	ctx = namespaces.WithNamespace(ctx, b.namespace)
	if err := b.snapshotter.Remove(ctx, key); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	if err := b.leases.Delete(ctx, leases.Lease{ID: key}); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("delete lease %q: %w", key, err)
	}
	return nil
}

func (b *containerdBackend) Close() error {
	return b.client.Close()
}

type containerdMounter struct{}

func (containerdMounter) Mount(mounts []Mount, target string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	return containerdmount.All(toContainerdMounts(mounts), target)
}

func (containerdMounter) Unmount(target string) error {
	err := containerdmount.UnmountAll(target, 0)
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOENT) {
		return nil
	}
	return err
}

func fromContainerdMounts(mounts []containerdmount.Mount) []Mount {
	result := make([]Mount, 0, len(mounts))
	for _, item := range mounts {
		result = append(result, Mount{Type: item.Type, Source: item.Source, Options: append([]string(nil), item.Options...)})
	}
	return result
}

func toContainerdMounts(mounts []Mount) []containerdmount.Mount {
	result := make([]containerdmount.Mount, 0, len(mounts))
	for _, item := range mounts {
		result = append(result, containerdmount.Mount{Type: item.Type, Source: item.Source, Options: append([]string(nil), item.Options...)})
	}
	return result
}
