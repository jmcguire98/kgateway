package builtin

import (
	"context"
	"time"

	"github.com/agentgateway/agentgateway/go/api"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Pass handles the translation of built-in Gateway API fields for AgentGateway.
type Pass struct{}

var _ pluginsdk.AgentGatewayPass = &Pass{}

// NewPass creates a new instance of the builtin pass for AgentGateway.
func NewPass() pluginsdk.AgentGatewayPass {
	return &Pass{}
}

func (p *Pass) ApplyForRoute(ctx context.Context, pctx *pluginsdk.AgentGatewayRouteContext, route *api.Route) error {
	// 1. Handle Timeouts
	if pctx.Rule.Timeouts != nil && pctx.Rule.Timeouts.Request != nil {
		if d, err := time.ParseDuration(string(*pctx.Rule.Timeouts.Request)); err == nil {
			if route.TrafficPolicy == nil {
				route.TrafficPolicy = &api.TrafficPolicy{}
			}
			route.TrafficPolicy.RequestTimeout = durationpb.New(d)
		}
	}

	return nil
}
