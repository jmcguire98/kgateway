package timeout

import (
	"context"
	"time"

	"github.com/agentgateway/agentgateway/go/api"
	"google.golang.org/protobuf/types/known/durationpb"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/agentgatewaysyncer/plugin"
)

var VirtualGVK = schema.GroupVersionKind{
	Group: "agentgateway.kgateway.dev",
	Kind:  "TimeoutPolicy",
}

func NewPlugin() plugin.Plugin {
	return plugin.Plugin{
		NewPass: func() plugin.Pass {
			return &Pass{}
		},
	}
}

type Pass struct{}

var _ plugin.Pass = &Pass{}

func (p *Pass) ApplyForRoute(ctx context.Context, pctx *plugin.RouteContext, route *api.Route) error {
	timeouts := pctx.Rule.Timeouts
	if timeouts == nil {
		return nil
	}

	if route.TrafficPolicy == nil {
		route.TrafficPolicy = &api.TrafficPolicy{}
	}

	if timeouts.Request != nil {
		if parsed, err := time.ParseDuration(string(*timeouts.Request)); err == nil {
			route.TrafficPolicy.RequestTimeout = durationpb.New(parsed)
		}
	}

	if timeouts.BackendRequest != nil {
		if parsed, err := time.ParseDuration(string(*timeouts.BackendRequest)); err == nil {
			route.TrafficPolicy.BackendRequestTimeout = durationpb.New(parsed)
		}
	}

	return nil
}
