package plugin

import (
	"context"

	"github.com/agentgateway/agentgateway/go/api"
	"k8s.io/apimachinery/pkg/runtime/schema"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type RouteContext struct {
	Rule *gwv1.HTTPRouteRule
}

type Pass interface {
	ApplyForRoute(ctx context.Context, pctx *RouteContext, route *api.Route) error
}

type NewPassFunc func() Pass

type Plugin struct {
	NewPass NewPassFunc
}

type Registry map[schema.GroupKind]Plugin
