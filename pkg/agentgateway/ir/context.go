package ir

import (
	"github.com/agentgateway/agentgateway/go/api"

	pluginsdkir "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
)

// AgentGatewayTranslationBackendContext provides context for backend translations
type AgentGatewayTranslationBackendContext struct {
	Backend        *pluginsdkir.BackendObjectIR
	GatewayContext pluginsdkir.GatewayContext
	// BackendFilters are Agent Gateway filters to be applied to the specific
	// backend reference on a route. Translation passes can append here during
	// ApplyForRouteBackend, and the translator will attach them to the
	// corresponding api.RouteBackend.
	BackendFilters []*api.RouteFilter
}
