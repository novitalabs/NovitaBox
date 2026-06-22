package runtime

import (
	"context"
	"time"

	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
)

type Driver interface {
	Create(ctx context.Context, spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error)
	Pause(ctx context.Context, sandboxID string) (*novitaboxv1.RuntimeInfo, error)
	Resume(ctx context.Context, spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error)
	Kill(ctx context.Context, sandboxID string) error
	Start(ctx context.Context, spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error)
	Stop(ctx context.Context, sandboxID string, timeout time.Duration) (*novitaboxv1.RuntimeInfo, error)
	Reboot(ctx context.Context, sandboxID string, timeout time.Duration) (*novitaboxv1.RuntimeInfo, error)
	Status(ctx context.Context, sandboxID string) (*novitaboxv1.RuntimeInfo, error)
	Capabilities(ctx context.Context, runtimeType novitaboxv1.RuntimeType) (*novitaboxv1.RuntimeCapabilities, error)
}
