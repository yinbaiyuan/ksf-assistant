package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

type WakeServer struct {
	server   *http.Server
	listener net.Listener
	dataRoot string
	kinds    []string
}

func StartWakeServer(dataRoot string, handlers map[string]func()) (*WakeServer, error) {
	if len(handlers) == 0 {
		return nil, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	kinds := []string{}
	for kind, handler := range handlers {
		kind, handler := kind, handler
		if !contains([]string{"outbox", "docbox", "actionbox"}, kind) || handler == nil {
			_ = listener.Close()
			return nil, errors.New("invalid wake handler")
		}
		kinds = append(kinds, kind)
		mux.HandleFunc("/internal/"+kind+"/wake", func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodPost {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			go handler()
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"ok":true,"status":"wake_accepted"}`))
		})
	}
	server := &WakeServer{server: &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}, listener: listener, dataRoot: dataRoot, kinds: kinds}
	port := listener.Addr().(*net.TCPAddr).Port
	for _, kind := range kinds {
		if err := updateQueueWake(dataRoot, kind, true, &port); err != nil {
			_ = listener.Close()
			return nil, err
		}
	}
	go func() { _ = server.server.Serve(listener) }()
	return server, nil
}

func (server *WakeServer) Close(ctx context.Context) error {
	if server == nil {
		return nil
	}
	for _, kind := range server.kinds {
		_ = updateQueueWake(server.dataRoot, kind, false, nil)
	}
	return server.server.Shutdown(ctx)
}

func updateQueueWake(dataRoot, kind string, enabled bool, port *int) error {
	path := filepath.Join(dataRoot, "logs", kind+"-state.json")
	return withProcessFileLock(path+".lock", func() error {
		state := defaultQueueState()
		if missing, err := readPrivateJSON(path, &state); err != nil && !missing {
			return err
		}
		normalizeQueueState(&state)
		state.Wake.Enabled, state.Wake.Host, state.Wake.ActualPort = enabled, "127.0.0.1", port
		return writePrivateJSON(path, state)
	})
}

func WakeQueue(dataRoot, kind string) error {
	path := filepath.Join(dataRoot, "logs", kind+"-state.json")
	state := defaultQueueState()
	missing, err := readPrivateJSON(path, &state)
	if err != nil || missing || !state.Wake.Enabled || state.Wake.Host != "127.0.0.1" || state.Wake.ActualPort == nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/internal/%s/wake", *state.Wake.ActualPort, kind), nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2500 * time.Millisecond}
	response, err := client.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("wake rejected: %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(&map[string]any{})
}
