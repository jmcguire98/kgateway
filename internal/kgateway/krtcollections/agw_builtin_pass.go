package krtcollections

import (
	"github.com/agentgateway/agentgateway/go/api"
	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/protobuf/types/known/durationpb"

	agwir "github.com/kgateway-dev/kgateway/v2/pkg/agentgateway/ir"
	pluginsdkir "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/reports"
)

// Agent Gateway translation pass for the builtin policy
type builtinPluginAgwPass struct{}

func NewBuiltinAgentGatewayPass(_ reports.Reporter) agwir.AgentGatewayTranslationPass { // reporter unused for now
	return &builtinPluginAgwPass{}
}

func (p *builtinPluginAgwPass) ApplyForBackend(_ *agwir.AgentGatewayTranslationBackendContext, _ *api.Backend) error {
	return nil
}
func (p *builtinPluginAgwPass) ApplyForRouteBackend(_ pluginsdkir.PolicyIR, _ *agwir.AgentGatewayTranslationBackendContext) error {
	return nil
}

// ApplyForRoute reads the builtin policy IR (same as Envoy) via RouteContext.Policy and applies to api.Route
func (p *builtinPluginAgwPass) ApplyForRoute(pctx *pluginsdkir.RouteContext, route *api.Route) error {
	pol, ok := pctx.Policy.(*builtinPlugin)
	if !ok || pol == nil {
		return nil
	}
	applyTimeoutsToAgwRoute(route, pol.rule.timeouts)
	applyRetryToAgwRoute(route, pol.rule.retry)
	return nil
}

func ensureTrafficPolicy(route *api.Route) *api.TrafficPolicy {
	if route.TrafficPolicy == nil {
		route.TrafficPolicy = &api.TrafficPolicy{}
	}
	return route.TrafficPolicy
}

func applyTimeoutsToAgwRoute(route *api.Route, t *timeouts) {
	if t == nil {
		return
	}
	tp := ensureTrafficPolicy(route)
	if t.requestTimeout != nil {
		tp.RequestTimeout = durationpb.New(t.requestTimeout.AsDuration())
	}
	if t.backendRequestTimeout != nil {
		tp.BackendRequestTimeout = durationpb.New(t.backendRequestTimeout.AsDuration())
	}
}

func applyRetryToAgwRoute(route *api.Route, r *envoyroutev3.RetryPolicy) {
	if r == nil {
		return
	}
	tp := ensureTrafficPolicy(route)
	if tp.Retry == nil {
		tp.Retry = &api.Retry{}
	}
	if r.NumRetries != nil {
		tp.Retry.Attempts = int32(r.NumRetries.GetValue())
	}
	if len(r.RetriableStatusCodes) > 0 {
		for _, c := range r.RetriableStatusCodes {
			tp.Retry.RetryStatusCodes = append(tp.Retry.RetryStatusCodes, int32(c))
		}
	}
	if bo := r.RetryBackOff; bo != nil && bo.BaseInterval != nil {
		tp.Retry.Backoff = durationpb.New(bo.BaseInterval.AsDuration())
	}
}
