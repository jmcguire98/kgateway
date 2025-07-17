package builtin

import (
	"context"
	"fmt"
	"time"

	"github.com/agentgateway/agentgateway/go/api"
	"google.golang.org/protobuf/types/known/durationpb"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/reports"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
)

func NewBuiltinPlugin() pluginsdk.Plugin {
	return pluginsdk.Plugin{
		ContributesPolicies: map[schema.GroupKind]pluginsdk.PolicyPlugin{
			ir.VirtualBuiltInGK: {
				NewAgentGatewayPass: func(reporter reports.Reporter) ir.AgentGatewayTranslationPass {
					return NewPass()
				},
			},
		},
	}
}

// Pass implements the ir.AgentGatewayTranslationPass interface.
type Pass struct{}

// NewPass creates a new Pass.
func NewPass() *Pass {
	return &Pass{}
}

// ApplyForRoute applies the builtin transformations for the given route.
func (p *Pass) ApplyForRoute(ctx context.Context, pctx *ir.AgentGatewayRouteContext, route *api.Route) error {
	err := applyTimeouts(pctx.Rule, route)
	return err
}

func applyTimeouts(rule *gwv1.HTTPRouteRule, route *api.Route) error {
	if rule.Timeouts != nil {
		if route.TrafficPolicy == nil {
			route.TrafficPolicy = &api.TrafficPolicy{}
		}
		if rule.Timeouts.Request != nil {
			if parsed, err := time.ParseDuration(string(*rule.Timeouts.Request)); err == nil {
				route.TrafficPolicy.RequestTimeout = durationpb.New(parsed)
			} else {
				return fmt.Errorf("failed to parse request timeout: %v", err)
			}
		}
		if rule.Timeouts.BackendRequest != nil {
			if parsed, err := time.ParseDuration(string(*rule.Timeouts.BackendRequest)); err == nil {
				route.TrafficPolicy.BackendRequestTimeout = durationpb.New(parsed)
			} else {
				return fmt.Errorf("failed to parse backend request timeout: %v", err)
			}
		}
	}
	return nil
}
