package ir

import (
	pluginsdkir "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
)

// Deprecated: use pluginsdkir.RouteContext directly

// AgentGatewayTranslationBackendContext provides context for backend translations
type AgentGatewayTranslationBackendContext struct {
	Backend        *pluginsdkir.BackendObjectIR
	GatewayContext pluginsdkir.GatewayContext
}
