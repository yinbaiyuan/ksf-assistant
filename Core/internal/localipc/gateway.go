package localipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ksfassistant/core/internal/privateipc"
)

const Protocol = "ksfassistant-localipc-v1"
const MaxClients = 32
const handshakeMethod = "localipc/hello"

var ErrNotRunning = errors.New("KSFAssistant application is not running")
var ErrIdentity = privateipc.NewError(-32031, "local IPC data root identity mismatch")

type identity struct {
	Protocol string `json:"protocol"`
	DataRoot string `json:"dataRoot"`
}

type Server struct {
	listener net.Listener
	release  func() error
	handler  privateipc.Handler
	root     string
	mu       sync.Mutex
	peers    map[*privateipc.Peer]struct{}
	closed   bool
	once     sync.Once
	done     chan struct{}
	closeErr error
}

func canonicalRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("local IPC data root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	current := absolute
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			resolved, err = normalizeRoot(resolved)
			if err != nil {
				return "", err
			}
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return normalizeIdentity(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func rootHash(root string) string {
	digest := sha256.Sum256([]byte(root))
	return hex.EncodeToString(digest[:])
}

func Listen(root string, handler privateipc.Handler) (*Server, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	listener, release, err := listenEndpoint(root)
	if err != nil {
		return nil, err
	}
	server := &Server{listener: listener, release: release, handler: handler, root: root, peers: make(map[*privateipc.Peer]struct{}), done: make(chan struct{})}
	go server.accept()
	return server, nil
}

func (server *Server) accept() {
	defer close(server.done)
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			return
		}
		server.mu.Lock()
		if server.closed || len(server.peers) >= MaxClients {
			server.mu.Unlock()
			_ = connection.Close()
			continue
		}
		var identityMu sync.Mutex
		verified := false
		handler := privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			identityMu.Lock()
			if method == handshakeMethod {
				defer identityMu.Unlock()
				if verified {
					return nil, privateipc.NewError(-32600, "local IPC session is already authenticated")
				}
				var request identity
				if err := privateipc.DecodeStrict(params, &request, true); err != nil {
					return nil, privateipc.NewError(-32602, "invalid local IPC handshake")
				}
				if request.Protocol != Protocol || request.DataRoot != server.root {
					return nil, ErrIdentity
				}
				verified = true
				_ = connection.SetReadDeadline(time.Time{})
				return identity{Protocol: Protocol, DataRoot: server.root}, nil
			}
			allowed := verified
			identityMu.Unlock()
			if !allowed {
				return nil, ErrIdentity
			}
			if server.handler == nil {
				return nil, privateipc.ErrMethodNotFound
			}
			return server.handler.HandlePrivateRPC(ctx, method, params)
		})
		_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
		peer := privateipc.NewPeer(connection, connection, handler)
		server.peers[peer] = struct{}{}
		server.mu.Unlock()
		go func() {
			_ = peer.Serve(context.Background())
			_ = peer.Close()
			server.mu.Lock()
			delete(server.peers, peer)
			server.mu.Unlock()
		}()
	}
}

func (server *Server) Close() error {
	server.once.Do(func() {
		server.mu.Lock()
		server.closed = true
		peers := make([]*privateipc.Peer, 0, len(server.peers))
		for peer := range server.peers {
			peers = append(peers, peer)
		}
		server.mu.Unlock()
		server.closeErr = server.listener.Close()
		for _, peer := range peers {
			_ = peer.Close()
		}
		<-server.done
		server.closeErr = errors.Join(server.closeErr, server.release())
	})
	return server.closeErr
}

type Session struct {
	peer *privateipc.Peer
}

func Dial(ctx context.Context, root string) (*Session, error) {
	return Open(ctx, root)
}

func Open(ctx context.Context, root string) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	connection, err := dialEndpoint(ctx, root)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %w", ErrNotRunning, err)
	}
	peer := privateipc.NewPeer(connection, connection, nil)
	go func() { _ = peer.Serve(context.Background()) }()
	handshakeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	expected := identity{Protocol: Protocol, DataRoot: root}
	var response identity
	if err := peer.Call(handshakeCtx, handshakeMethod, expected, &response); err != nil {
		_ = peer.Close()
		return nil, err
	}
	if response != expected {
		_ = peer.Close()
		return nil, ErrIdentity
	}
	if err := ctx.Err(); err != nil {
		_ = peer.Close()
		return nil, err
	}
	return &Session{peer: peer}, nil
}

func (session *Session) Call(ctx context.Context, method string, params any, target any) error {
	if session == nil || session.peer == nil {
		return privateipc.ErrPeerClosed
	}
	if method == handshakeMethod {
		return privateipc.NewError(-32600, "local IPC session is already authenticated")
	}
	return session.peer.Call(ctx, method, params, target)
}

func (session *Session) Close() error {
	if session == nil || session.peer == nil {
		return nil
	}
	return session.peer.Close()
}

func Call(ctx context.Context, root string, method string, params any, target any) error {
	session, err := Open(ctx, root)
	if err != nil {
		return err
	}
	defer session.Close()
	return session.Call(ctx, method, params, target)
}
