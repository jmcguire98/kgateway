package translator

import (
	"errors"
	"fmt"
	"strings"

	"github.com/agentgateway/agentgateway/go/api"
	"k8s.io/apimachinery/pkg/runtime/schema"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	extensionsplug "github.com/kgateway-dev/kgateway/v2/internal/kgateway/extensions2/plugin"
	agwir "github.com/kgateway-dev/kgateway/v2/pkg/agentgateway/ir"
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
	routeIR pluginsdkir.HttpRouteIR,
) ([]*api.Route, []*api.Policy, error) {
	var routesOut []*api.Route
	var polsOut []*api.Policy

	for _, rule := range routeIR.Rules {
		for _, match := range rule.Matches {
			r := &api.Route{
				RouteName: fmt.Sprintf("%s/%s", routeIR.Namespace, routeIR.Name),
				RuleName:  rule.Name,
				Hostnames: routeIR.GetHostnames(),
			}
			if pm, err := buildPathMatchFromGW(match); err != nil {
				return nil, nil, err
			} else if pm != nil {
				r.Matches = append(r.Matches, &api.RouteMatch{Path: pm})
			}
			if hm, err := buildHeadersMatchFromGW(match); err != nil {
				return nil, nil, err
			} else if len(hm) > 0 {
				if len(r.Matches) == 0 {
					r.Matches = append(r.Matches, &api.RouteMatch{})
				}
				r.Matches[0].Headers = hm
			}
			if mm, err := buildMethodMatchFromGW(match); err != nil {
				return nil, nil, err
			} else if mm != nil {
				if len(r.Matches) == 0 {
					r.Matches = append(r.Matches, &api.RouteMatch{})
				}
				r.Matches[0].Method = mm
			}
			if qm, err := buildQueryMatchFromGW(match); err != nil {
				return nil, nil, err
			} else if len(qm) > 0 {
				if len(r.Matches) == 0 {
					r.Matches = append(r.Matches, &api.RouteMatch{})
				}
				r.Matches[0].QueryParams = qm
			}
			for _, be := range rule.Backends {
				if be.Backend == nil || be.Backend.BackendObject == nil {
					continue
				}
				rb := &api.RouteBackend{
					Weight:  int32(be.Backend.Weight),
					Backend: &api.BackendReference{Kind: &api.BackendReference_Backend{Backend: be.Backend.BackendObject.Namespace + "/" + be.Backend.BackendObject.Name}},
				}
				r.Backends = append(r.Backends, rb)
				beCtx := &agwir.AgentGatewayTranslationBackendContext{Backend: be.Backend.BackendObject}
				// Construct a minimal match IR for policy execution ordering
				matchIR := pluginsdkir.HttpRouteRuleMatchIR{
					ExtensionRefs:    rule.ExtensionRefs,
					AttachedPolicies: rule.AttachedPolicies,
					Parent:           &routeIR,
					Match:            match,
				}
				if err := t.runRouteBackendPolicies(&matchIR, beCtx); err != nil {
					return nil, nil, err
				}
			}
			matchIR := pluginsdkir.HttpRouteRuleMatchIR{
				ExtensionRefs:    rule.ExtensionRefs,
				AttachedPolicies: rule.AttachedPolicies,
				Parent:           &routeIR,
				Match:            match,
			}
			if err := t.runRoutePolicies(&matchIR, r); err != nil {
				return nil, nil, err
			}
			routesOut = append(routesOut, r)
		}
	}
	return routesOut, polsOut, nil
}

// TranslateTcpRoute translates a TcpRouteIR to agent gateway TCPRoute resources.
// Implementation will be added in subsequent edits as we wire RouteIndex into the syncer.
func (t *AgentGatewayRouteTranslator) TranslateTcpRoute(
	routeIR pluginsdkir.TcpRouteIR,
) ([]*api.TCPRoute, []*api.Policy, error) {
	var out []*api.TCPRoute
	var polsOut []*api.Policy
	r := &api.TCPRoute{
		RouteName: fmt.Sprintf("%s/%s", routeIR.Namespace, routeIR.Name),
		RuleName:  "",
	}
	for _, be := range routeIR.Backends {
		if be.BackendObject == nil {
			continue
		}
		r.Backends = append(r.Backends, &api.RouteBackend{
			Weight:  int32(be.Weight),
			Backend: &api.BackendReference{Kind: &api.BackendReference_Backend{Backend: be.BackendObject.Namespace + "/" + be.BackendObject.Name}},
		})
	}
	out = append(out, r)
	return out, polsOut, nil
}

// TranslateTlsRoute translates a TlsRouteIR to agent gateway TCPRoute resources (TLS is TCP-level here).
// Implementation will be added in subsequent edits as we wire RouteIndex into the syncer.
func (t *AgentGatewayRouteTranslator) TranslateTlsRoute(
	routeIR pluginsdkir.TlsRouteIR,
) ([]*api.TCPRoute, []*api.Policy, error) {
	var out []*api.TCPRoute
	var polsOut []*api.Policy
	r := &api.TCPRoute{
		RouteName: fmt.Sprintf("%s/%s", routeIR.Namespace, routeIR.Name),
		RuleName:  "",
		Hostnames: routeIR.GetHostnames(),
	}
	for _, be := range routeIR.Backends {
		if be.BackendObject == nil {
			continue
		}
		r.Backends = append(r.Backends, &api.RouteBackend{
			Weight:  int32(be.Weight),
			Backend: &api.BackendReference{Kind: &api.BackendReference_Backend{Backend: be.BackendObject.Namespace + "/" + be.BackendObject.Name}},
		})
	}
	out = append(out, r)
	return out, polsOut, nil
}

// runRoutePolicies applies policy passes to a single HttpRouteRuleMatchIR producing agent route fields.
func (t *AgentGatewayRouteTranslator) runRoutePolicies(in *pluginsdkir.HttpRouteRuleMatchIR, out *api.Route) error {
	var errs []error
	var ordered pluginsdkir.AttachedPolicies
	ordered.Append(in.ExtensionRefs, in.AttachedPolicies)
	if in.Parent != nil {
		ordered.Append(in.Parent.AttachedPolicies)
	}
	hierarchicalPriority := 0
	delegatingParent := in.DelegatingParent
	for delegatingParent != nil {
		hierarchicalPriority--
		ordered.AppendWithPriority(hierarchicalPriority,
			delegatingParent.ExtensionRefs, delegatingParent.AttachedPolicies, delegatingParent.Parent.AttachedPolicies)
		delegatingParent = delegatingParent.DelegatingParent
	}
	for gk, pols := range ordered.Policies {
		plugin, ok := t.ContributedPolicies[gk]
		if !ok || plugin.NewAgentGatewayPass == nil {
			continue
		}
		pass := plugin.NewAgentGatewayPass(nil)
		if plugin.MergePolicies != nil {
			merged := plugin.MergePolicies(pols)
			if len(merged.Errors) > 0 {
				errs = append(errs, merged.Errors...)
			} else if err := pass.ApplyForRoute(&agwir.AgentGatewayRouteContext{Rule: nil}, out); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		for _, pa := range pols {
			if len(pa.Errors) > 0 {
				errs = append(errs, pa.Errors...)
				continue
			}
			if err := pass.ApplyForRoute(&agwir.AgentGatewayRouteContext{Rule: nil}, out); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// runRouteBackendPolicies applies policies attached to a specific backend referenced by the route match.
func (t *AgentGatewayRouteTranslator) runRouteBackendPolicies(in *pluginsdkir.HttpRouteRuleMatchIR, beCtx *agwir.AgentGatewayTranslationBackendContext) error {
	var errs []error
	for gk, pols := range in.AttachedPolicies.Policies {
		plugin, ok := t.ContributedPolicies[gk]
		if !ok || plugin.NewAgentGatewayPass == nil {
			continue
		}
		pass := plugin.NewAgentGatewayPass(nil)
		if plugin.MergePolicies != nil {
			merged := plugin.MergePolicies(pols)
			if len(merged.Errors) > 0 {
				errs = append(errs, merged.Errors...)
			} else if err := pass.ApplyForRouteBackend(merged.PolicyIr, beCtx); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		for _, pa := range pols {
			if len(pa.Errors) > 0 {
				errs = append(errs, pa.Errors...)
				continue
			}
			if err := pass.ApplyForRouteBackend(pa.PolicyIr, beCtx); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// buildHttpRouteMatch converts gwv1.HTTPRouteMatch to api.RouteMatch
// Helpers to build ADP matches from gw-api structures
func buildPathMatchFromGW(match gwv1.HTTPRouteMatch) (*api.PathMatch, error) {
	if match.Path == nil {
		return nil, nil
	}
	tp := gwv1.PathMatchPathPrefix
	if match.Path.Type != nil {
		tp = *match.Path.Type
	}
	dest := "/"
	if match.Path.Value != nil {
		dest = *match.Path.Value
	}
	switch tp {
	case gwv1.PathMatchPathPrefix:
		if dest != "/" {
			dest = strings.TrimSuffix(dest, "/")
		}
		return &api.PathMatch{Kind: &api.PathMatch_PathPrefix{PathPrefix: dest}}, nil
	case gwv1.PathMatchExact:
		return &api.PathMatch{Kind: &api.PathMatch_Exact{Exact: dest}}, nil
	case gwv1.PathMatchRegularExpression:
		return &api.PathMatch{Kind: &api.PathMatch_Regex{Regex: dest}}, nil
	default:
		return nil, fmt.Errorf("unsupported path match type")
	}
}

func buildHeadersMatchFromGW(match gwv1.HTTPRouteMatch) ([]*api.HeaderMatch, error) {
	var res []*api.HeaderMatch
	for _, header := range match.Headers {
		tp := gwv1.HeaderMatchExact
		if header.Type != nil {
			tp = *header.Type
		}
		switch tp {
		case gwv1.HeaderMatchExact:
			res = append(res, &api.HeaderMatch{Name: string(header.Name), Value: &api.HeaderMatch_Exact{Exact: header.Value}})
		case gwv1.HeaderMatchRegularExpression:
			res = append(res, &api.HeaderMatch{Name: string(header.Name), Value: &api.HeaderMatch_Regex{Regex: header.Value}})
		default:
			return nil, fmt.Errorf("unsupported header match type")
		}
	}
	return res, nil
}

func buildMethodMatchFromGW(match gwv1.HTTPRouteMatch) (*api.MethodMatch, error) {
	if match.Method == nil {
		return nil, nil
	}
	return &api.MethodMatch{Exact: string(*match.Method)}, nil
}

func buildQueryMatchFromGW(match gwv1.HTTPRouteMatch) ([]*api.QueryMatch, error) {
	var res []*api.QueryMatch
	for _, qp := range match.QueryParams {
		tp := gwv1.QueryParamMatchExact
		if qp.Type != nil {
			tp = *qp.Type
		}
		switch tp {
		case gwv1.QueryParamMatchExact:
			res = append(res, &api.QueryMatch{Name: string(qp.Name), Value: &api.QueryMatch_Exact{Exact: qp.Value}})
		case gwv1.QueryParamMatchRegularExpression:
			res = append(res, &api.QueryMatch{Name: string(qp.Name), Value: &api.QueryMatch_Regex{Regex: qp.Value}})
		default:
			return nil, fmt.Errorf("unsupported query match type")
		}
	}
	return res, nil
}
