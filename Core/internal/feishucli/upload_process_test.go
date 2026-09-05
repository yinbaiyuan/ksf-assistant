package feishucli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"ksfassistant/core/internal/localipc"
	"ksfassistant/core/internal/privateipc"
)

func TestThirtyMiBThroughGatewayAndDaemonProcessBoundaries(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUploadChildProcess$")
	command.Env = append(os.Environ(), "KSF_CLI_UPLOAD_HELPER=gateway", "KSF_CLI_UPLOAD_ROOT="+root)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if err := command.Wait(); err != nil {
			t.Errorf("gateway test process: %v: %s", err, stderr.String())
		}
	})
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || ready != "READY\n" {
		t.Fatalf("gateway readiness=%q err=%v", ready, err)
	}
	session, err := localipc.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	request := Request{Command: "send", Options: map[string]string{"target": "alias", "format": "file"}, Payloads: map[string][]byte{"media-file": bytes.Repeat([]byte("z"), MaxMediaBytes)}}
	expected := sha256.Sum256(request.Payloads["media-file"])
	var result struct {
		Status string `json:"status"`
		Bytes  int    `json:"bytes"`
		Digest string `json:"digest"`
	}
	if err := CallRequest(ctx, session.Call, MethodExecute, request, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || result.Bytes != MaxMediaBytes || result.Digest != hex.EncodeToString(expected[:]) {
		t.Fatalf("two-hop response=%#v", result)
	}
	assertUploadBudgetEmpty(t)
}

func TestUploadChildProcess(t *testing.T) {
	mode := os.Getenv("KSF_CLI_UPLOAD_HELPER")
	if mode == "" {
		return
	}
	var err error
	switch mode {
	case "daemon":
		err = runUploadDaemonTestProcess()
	case "gateway":
		err = runUploadGatewayTestProcess()
	default:
		err = errors.New("unsupported upload test process")
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runUploadDaemonTestProcess() error {
	var executions int
	next := privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method != "bridge/client/execute" {
			return nil, privateipc.ErrMethodNotFound
		}
		request, err := DecodeRequest(params)
		if err != nil {
			return nil, err
		}
		executions++
		if executions != 1 || len(request.Payloads["media-file"]) != MaxMediaBytes {
			return nil, errors.New("invalid daemon upload execution")
		}
		digest := sha256.Sum256(request.Payloads["media-file"])
		return map[string]any{"status": "ok", "bytes": len(request.Payloads["media-file"]), "digest": hex.EncodeToString(digest[:])}, nil
	})
	peer := privateipc.NewPeer(os.Stdin, os.Stdout, NewUploadHandler(next, "bridge/client/execute"))
	err := peer.Serve(context.Background())
	_ = peer.Close()
	if err != nil {
		return err
	}
	return checkProcessUploadBudgetReleased()
}

func runUploadGatewayTestProcess() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUploadChildProcess$")
	command.Env = append(os.Environ(), "KSF_CLI_UPLOAD_HELPER=daemon")
	command.Stderr = os.Stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	peer := privateipc.NewPeer(stdout, stdin, nil)
	served := make(chan error, 1)
	go func() { served <- peer.Serve(ctx) }()
	next := privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method != MethodExecute {
			return nil, privateipc.ErrMethodNotFound
		}
		request, err := DecodeRequest(params)
		if err != nil {
			return nil, err
		}
		var result json.RawMessage
		err = CallRequest(ctx, peer.Call, "bridge/client/execute", request, &result)
		return result, err
	})
	server, err := localipc.Listen(os.Getenv("KSF_CLI_UPLOAD_ROOT"), NewUploadHandler(next, MethodExecute))
	if err != nil {
		_ = peer.Close()
		_ = command.Wait()
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, "READY")
	if err == nil {
		_, err = io.Copy(io.Discard, os.Stdin)
	}
	closeErr := server.Close()
	_ = peer.Close()
	<-served
	childErr := command.Wait()
	return errors.Join(err, closeErr, childErr, checkProcessUploadBudgetReleased())
}

func checkProcessUploadBudgetReleased() error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		processUploads.mu.Lock()
		used, transfers := processUploads.bytes, processUploads.transfers
		processUploads.mu.Unlock()
		if used == 0 && transfers == 0 {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return errors.New("test process retained upload memory")
}
