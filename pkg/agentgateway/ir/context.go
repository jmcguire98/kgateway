package ir

import (
	"context"

	"istio.io/istio/pkg/kube/krt"
	corev1 "k8s.io/api/core/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
)

// AgentGatewayBackendContext provides access to Kubernetes resources during backend translation
type AgentGatewayBackendContext struct {
	context.Context
	KrtCtx     krt.HandlerContext
	Namespaces krt.Collection[*corev1.Namespace]
	Services   krt.Collection[*corev1.Service]
	Secrets    krt.Collection[*corev1.Secret]
}

// AgentGatewayRouteContext provides context for route-level translations
type AgentGatewayRouteContext struct {
	Rule *gwv1.HTTPRouteRule
}

// AgentGatewayTranslationBackendContext provides context for backend translations
type AgentGatewayTranslationBackendContext struct {
	Backend        *ir.BackendObjectIR
	GatewayContext ir.GatewayContext
}
