package translator

import (
	"errors"

	"github.com/agentgateway/agentgateway/go/api"
	"istio.io/istio/pkg/kube/krt"
	"k8s.io/apimachinery/pkg/runtime/schema"

	extensionsplug "github.com/kgateway-dev/kgateway/v2/internal/kgateway/extensions2/plugin"
	pluginsdkir "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
)

// AgentGatewayRouteTranslator handles translation of route IR to agent gateway route resources
type AgentGatewayRouteTranslator struct {
	// ContributedPolicies maps policy GKs to agent-gateway translation passes
	ContributedPolicies map[schema.GroupKind]extensionsplug.PolicyPlugin
}

// NewAgentGatewayRouteTranslator creates a new AgentGatewayRouteTranslator
func NewAgentGatewayRouteTranslator(extensions extensionsplug.Plugin) *AgentGatewayRouteTranslator {
	return &AgentGatewayRouteTranslator{
		ContributedPolicies: extensions.ContributesPolicies,
	}
}

// TranslateHttpLikeRoute translates an HttpRouteIR (including GRPC-as-HTTP) to agent gateway Route resources.
// Implementation will be added in subsequent edits as we wire RouteIndex into the syncer.
func (t *AgentGatewayRouteTranslator) TranslateHttpLikeRoute(
	_ krt.HandlerContext,
	_ pluginsdkir.HttpRouteIR,
) ([]*api.Route, []*api.Policy, error) {
	return nil, nil, errors.New("AgentGatewayRouteTranslator.TranslateHttpLikeRoute not implemented yet")
}

// TranslateTcpRoute translates a TcpRouteIR to agent gateway TCPRoute resources.
// Implementation will be added in subsequent edits as we wire RouteIndex into the syncer.
func (t *AgentGatewayRouteTranslator) TranslateTcpRoute(
	_ krt.HandlerContext,
	_ pluginsdkir.TcpRouteIR,
) ([]*api.TCPRoute, []*api.Policy, error) {
	return nil, nil, errors.New("AgentGatewayRouteTranslator.TranslateTcpRoute not implemented yet")
}

// TranslateTlsRoute translates a TlsRouteIR to agent gateway TCPRoute resources (TLS is TCP-level here).
// Implementation will be added in subsequent edits as we wire RouteIndex into the syncer.
func (t *AgentGatewayRouteTranslator) TranslateTlsRoute(
	_ krt.HandlerContext,
	_ pluginsdkir.TlsRouteIR,
) ([]*api.TCPRoute, []*api.Policy, error) {
	return nil, nil, errors.New("AgentGatewayRouteTranslator.TranslateTlsRoute not implemented yet")
}
