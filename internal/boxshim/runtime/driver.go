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
	UpdateBalloon(ctx context.Context, sandboxID string, amountMiB uint32) (*novitaboxv1.BalloonConfig, error)
	GetBalloon(ctx context.Context, sandboxID string) (*novitaboxv1.BalloonConfig, error)
	GetBalloonStats(ctx context.Context, sandboxID string) (*novitaboxv1.BalloonStats, error)
	UpdateBalloonStats(ctx context.Context, sandboxID string, intervalSeconds uint32) (*novitaboxv1.BalloonConfig, error)
	StartBalloonHinting(ctx context.Context, sandboxID string, acknowledgeOnStop bool) (*novitaboxv1.BalloonHintingStatus, error)
	StopBalloonHinting(ctx context.Context, sandboxID string) (*novitaboxv1.BalloonHintingStatus, error)
	GetBalloonHinting(ctx context.Context, sandboxID string) (*novitaboxv1.BalloonHintingStatus, error)
}
