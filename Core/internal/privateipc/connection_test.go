package privateipc

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestConnectionContextStableAcrossRequestsAndCancelledOnEOF(t *testing.T) {
	owners := make(chan context.Context, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var previous context.Context
	for index := 0; index < 2; index++ {
		local, remote := net.Pipe()
		client := NewPeer(local, local, nil)
		server := NewPeer(remote, remote, HandlerFunc(func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			owner, ok := ConnectionContext(ctx)
			if !ok || owner.Done() == nil {
				return nil, NewError(-32000, "missing connection owner")
			}
			owners <- owner
			return true, nil
		}))
		defer client.Close()
		defer server.Close()
		go client.Serve(ctx)
		go server.Serve(ctx)
		if err := client.Call(ctx, "first", nil, nil); err != nil {
			t.Fatal(err)
		}
		first := receiveWithin(t, owners)
		if err := client.Call(ctx, "second", nil, nil); err != nil {
			t.Fatal(err)
		}
		second := receiveWithin(t, owners)
		if first.Done() != second.Done() || first.Err() != nil {
			t.Fatal("request completion changed connection owner")
		}
		if previous != nil && previous.Done() == first.Done() {
			t.Fatal("different connections share owner")
		}
		previous = first
		_ = client.Close()
		select {
		case <-first.Done():
		case <-ctx.Done():
			t.Fatal("EOF did not cancel connection owner")
		}
	}
	for _, unbound := range []context.Context{nil, context.Background(), ctx} {
		if owner, ok := ConnectionContext(unbound); ok || owner != nil {
			t.Fatal("unbound context supplied connection owner")
		}
	}
}
