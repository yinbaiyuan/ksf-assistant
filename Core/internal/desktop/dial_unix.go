//go:build !windows

package desktop

import (
	"context"
	"net"
)

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
}
