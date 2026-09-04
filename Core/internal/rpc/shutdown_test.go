package rpc

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"codexusagebar/core/internal/service"
)

func TestShutdownReturnsWithoutWaitingForStdinEOF(t *testing.T) {
	t.Setenv("FEISHU_BRIDGE_DATA_DIR", t.TempDir())
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output bytes.Buffer
	server := New(service.New(), reader, &output)
	done := make(chan error, 1)
	go func() { done <- server.Serve(context.Background()) }()
	if _, err := io.WriteString(writer, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"shutdown\"}\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), `"stopped":true`) {
			t.Fatalf("missing shutdown acknowledgement: %s", output.String())
		}
	case <-time.After(time.Second):
		writer.Close()
		<-done
		t.Fatal("shutdown acknowledged but server waited for another input/EOF")
	}
}
