package rpc

import (
	"encoding/json"

	"ksfassistant/core/internal/service"
)

func (server *Server) dispatchToolchain(method string, params json.RawMessage) (any, error) {
	if method == "toolchain/status" {
		if err := decodeDesktopControlParams(params, &struct{}{}); err != nil {
			return nil, err
		}
		return server.service.ToolchainStatus()
	}
	var input service.ToolchainInstallRequest
	if err := decodeDesktopControlParams(params, &input); err != nil {
		return nil, err
	}
	return server.service.InstallToolchain(input)
}
