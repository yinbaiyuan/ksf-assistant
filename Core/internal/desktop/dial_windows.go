//go:build windows

package desktop

import (
	"context"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	timeout := 3 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	return winio.DialPipe(endpoint, &timeout)
}
