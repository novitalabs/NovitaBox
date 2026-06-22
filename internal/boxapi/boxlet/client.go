package boxlet

import (
	"context"
	"fmt"

	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn     *grpc.ClientConn
	Sandbox  novitaboxv1.BoxletSandboxServiceClient
	Artifact novitaboxv1.BoxletArtifactServiceClient
}

func Dial(ctx context.Context, addr string) (*Client, error) {
	_ = ctx

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("create boxlet client: %w", err)
	}

	return &Client{
		conn:     conn,
		Sandbox:  novitaboxv1.NewBoxletSandboxServiceClient(conn),
		Artifact: novitaboxv1.NewBoxletArtifactServiceClient(conn),
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}
