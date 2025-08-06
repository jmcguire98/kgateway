package agentgatewaybackend

import (
	"github.com/agentgateway/agentgateway/go/api"
	corev1 "k8s.io/api/core/v1"
)

// AgentGatewayBackendIr is the internal representation of an agent gateway backend.
// This mirrors the envoy BackendIr pattern by pre-resolving all dependencies
// during collection building rather than at translation time.
type AgentGatewayBackendIr struct {
	StaticIr *StaticIr
	AIIr     *AIIr
	MCPIr    *MCPIr
	Errors   []error
}

func (u *AgentGatewayBackendIr) Equals(other any) bool {
	otherBackend, ok := other.(*AgentGatewayBackendIr)
	if !ok {
		return false
	}

	// Compare Static IR
	if !u.StaticIr.Equals(otherBackend.StaticIr) {
		return false
	}

	// Compare AI IR
	if !u.AIIr.Equals(otherBackend.AIIr) {
		return false
	}

	// Compare MCP IR
	if !u.MCPIr.Equals(otherBackend.MCPIr) {
		return false
	}

	return true
}

// StaticIr contains pre-resolved data for static backends
type StaticIr struct {
	// Pre-resolved static backend configuration
	Backend *api.Backend
}

func (s *StaticIr) Equals(other *StaticIr) bool {
	if s == nil && other == nil {
		return true
	}
	if s == nil || other == nil {
		return false
	}
	// For now, just check if both are non-nil
	// TODO: implement proper equality check for api.Backend
	return s.Backend != nil && other.Backend != nil
}

// AIIr contains pre-resolved data for AI backends
type AIIr struct {
	// Pre-resolved AI backend and auth policy
	Name       string
	Backend    *api.AIBackend
	AuthPolicy *api.BackendAuthPolicy
}

func (a *AIIr) Equals(other *AIIr) bool {
	if a == nil && other == nil {
		return true
	}
	if a == nil || other == nil {
		return false
	}
	// For now, just check if both are non-nil
	// TODO: implement proper equality check for api.Backend and api.Policy
	return a.Backend != nil && other.Backend != nil
}

// MCPIr contains pre-resolved data for MCP backends
type MCPIr struct {
	// Pre-resolved MCP backend and any static backends it references
	Backends []*api.Backend
	// Pre-resolved service endpoints
	ServiceEndpoints map[string]*ServiceEndpoint
}

func (m *MCPIr) Equals(other *MCPIr) bool {
	if m == nil && other == nil {
		return true
	}
	if m == nil || other == nil {
		return false
	}
	// For now, just check if both are non-nil
	// TODO: implement proper equality check
	return len(m.Backends) == len(other.Backends)
}

// ServiceEndpoint represents a resolved service endpoint
type ServiceEndpoint struct {
	Host string
	Port int32
	// Additional service metadata if needed
	Service   *corev1.Service
	Namespace string
}
