package agentgatewaysyncer

import (
	"fmt"
	"time"

	"github.com/agentgateway/agentgateway/go/api"
	"google.golang.org/protobuf/types/known/durationpb"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// applyTimeouts copies Gateway API timeout settings from the rule onto the output agentgateway Route.
// It does not override fields that are already populated by higher-priority logic.
func applyTimeouts(rule *gwv1.HTTPRouteRule, route *api.Route) error {
	if rule == nil || rule.Timeouts == nil {
		return nil
	}
	if route.TrafficPolicy == nil {
		route.TrafficPolicy = &api.TrafficPolicy{}
	}
	if rule.Timeouts.Request != nil {
		d, err := time.ParseDuration(string(*rule.Timeouts.Request))
		if err != nil {
			return fmt.Errorf("failed to parse request timeout: %w", err)
		}
		route.TrafficPolicy.RequestTimeout = durationpb.New(d)
	}
	if rule.Timeouts.BackendRequest != nil {
		d, err := time.ParseDuration(string(*rule.Timeouts.BackendRequest))
		if err != nil {
			return fmt.Errorf("failed to parse backend request timeout: %w", err)
		}
		route.TrafficPolicy.BackendRequestTimeout = durationpb.New(d)
	}
	return nil
}

// applyRetries copies Gateway API retry settings onto the agentgateway Route.
func applyRetries(rule *gwv1.HTTPRouteRule, route *api.Route) error {
	if rule == nil || rule.Retry == nil {
		return nil
	}
	if route.TrafficPolicy == nil {
		route.TrafficPolicy = &api.TrafficPolicy{}
	}
	tpRetry := &api.Retry{}
	if rule.Retry.Codes != nil {
		for _, c := range rule.Retry.Codes {
			tpRetry.RetryStatusCodes = append(tpRetry.RetryStatusCodes, int32(c))
		}
	}
	if rule.Retry.Backoff != nil {
		if d, err := time.ParseDuration(string(*rule.Retry.Backoff)); err == nil {
			tpRetry.Backoff = durationpb.New(d)
		}
	}
	if rule.Retry.Attempts != nil {
		tpRetry.Attempts = int32(*rule.Retry.Attempts)
	}
	route.TrafficPolicy.Retry = tpRetry
	return nil
}
