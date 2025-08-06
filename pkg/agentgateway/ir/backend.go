package ir

import (
	"github.com/agentgateway/agentgateway/go/api"

	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
)

// AgentBackendInit defines the initialization interface for agent gateway backends
type AgentBackendInit struct {
	// InitAgentBackend translates backend objects for the agent gateway data plane.
	// It takes a BackendObjectIR (which includes the backend and any attached policies)
	// and returns the corresponding agent gateway Backend and Policy resources.
	InitAgentBackend func(ctx *AgentGatewayBackendContext, in ir.BackendObjectIR) ([]*api.Backend, []*api.Policy, error)
}
