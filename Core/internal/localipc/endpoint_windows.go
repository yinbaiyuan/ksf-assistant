//go:build windows

package localipc

import (
	"context"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func normalizeRoot(root string) (string, error) { return root, nil }

func normalizeIdentity(root string) string { return strings.ToLower(root) }

func pipeIdentity(root string) (string, string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", "", err
	}
	sid := user.User.Sid.String()
	return `\\.\pipe\ksfassistant-` + rootHash(sid+"\x00"+root), "D:P(A;;GA;;;" + sid + ")", nil
}

func listenEndpoint(root string) (net.Listener, func() error, error) {
	path, descriptor, err := pipeIdentity(root)
	if err != nil {
		return nil, nil, err
	}
	listener, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: descriptor, InputBufferSize: 65536, OutputBufferSize: 65536})
	return listener, func() error { return nil }, err
}

func dialEndpoint(ctx context.Context, root string) (net.Conn, error) {
	path, _, err := pipeIdentity(root)
	if err != nil {
		return nil, err
	}
	return winio.DialPipeContext(ctx, path)
}
