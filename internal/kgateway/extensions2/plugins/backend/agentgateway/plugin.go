// Package backend contains the backend plugin implementation.
// This file contains agent gateway specific backend processing logic,
// separated from the main envoy backend plugin implementation in plugin.go.
package agentgatewaybackend

import (
	"fmt"

	"github.com/agentgateway/agentgateway/go/api"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
	agwir "github.com/kgateway-dev/kgateway/v2/pkg/agentgateway/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/logging"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/reports"
)

var logger = logging.New("plugin/backend/agentgateway_backend")

// agentGatewayBackendPlugin implements agent gateway specific backend processing
type agentGatewayBackendPlugin struct {
	agwir.UnimplementedAgentGatewayTranslationPass
}

var _ agwir.AgentGatewayTranslationPass = &agentGatewayBackendPlugin{}

// NewAgentGatewayPlug creates a new agent gateway backend plugin instance
func NewAgentGatewayPlug(reporter reports.Reporter) agwir.AgentGatewayTranslationPass {
	return &agentGatewayBackendPlugin{}
}

// ApplyForBackend processes backend-level configuration for agent gateway
func (p *agentGatewayBackendPlugin) ApplyForBackend(pCtx *agwir.AgentGatewayTranslationBackendContext, out *api.Backend) error {
	// Apply backend-specific configuration for agent gateway
	logger.Debug("agent gateway backend plugin processed backend", "backend", out.Name)
	return nil
}

// processBackendForAgentGateway handles the main backend processing logic for agent gateway
func ProcessBackendForAgentGateway(ctx *ir.AgentGatewayBackendContext, in ir.BackendObjectIR) ([]*api.Backend, []*api.Policy, error) {
	be, ok := in.Obj.(*v1alpha1.Backend)
	if !ok {
		return nil, nil, fmt.Errorf("expected *v1alpha1.Backend, got %T", in.Obj)
	}

	spec := be.Spec
	switch spec.Type {
	case v1alpha1.BackendTypeStatic:
		return processStaticBackendForAgentGateway(be)
	case v1alpha1.BackendTypeAI:
		return processAIBackendForAgentGateway(ctx, in)
	case v1alpha1.BackendTypeMCP:
		return processMCPBackendForAgentGateway(ctx, in)
	default:
		return nil, nil, fmt.Errorf("backend of type %s is not supported for agent gateway", spec.Type)
	}
}
