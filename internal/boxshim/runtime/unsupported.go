package runtime

import (
	"context"
	"errors"
	"time"

	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
)

type UnsupportedDriver struct {
	err error
}

func NewUnsupportedDriver(message string) *UnsupportedDriver {
	return &UnsupportedDriver{err: errors.New(message)}
}

func (d *UnsupportedDriver) Create(context.Context, *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) Pause(context.Context, string) (*novitaboxv1.RuntimeInfo, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) Resume(context.Context, *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) Kill(context.Context, string) error {
	return d.err
}

func (d *UnsupportedDriver) Start(context.Context, *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) Stop(context.Context, string, time.Duration) (*novitaboxv1.RuntimeInfo, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) Reboot(context.Context, string, time.Duration) (*novitaboxv1.RuntimeInfo, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) Status(context.Context, string) (*novitaboxv1.RuntimeInfo, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) Capabilities(context.Context, novitaboxv1.RuntimeType) (*novitaboxv1.RuntimeCapabilities, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) UpdateBalloon(context.Context, string, uint32) (*novitaboxv1.BalloonConfig, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) GetBalloon(context.Context, string) (*novitaboxv1.BalloonConfig, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) GetBalloonStats(context.Context, string) (*novitaboxv1.BalloonStats, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) UpdateBalloonStats(context.Context, string, uint32) (*novitaboxv1.BalloonConfig, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) StartBalloonHinting(context.Context, string, bool) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) StopBalloonHinting(context.Context, string) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, d.err
}

func (d *UnsupportedDriver) GetBalloonHinting(context.Context, string) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, d.err
}
