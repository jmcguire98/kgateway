package translator

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentgateway/agentgateway/go/api"
	"k8s.io/apimachinery/pkg/runtime/schema"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"google.golang.org/protobuf/types/known/durationpb"

	extensionsplug "github.com/kgateway-dev/kgateway/v2/internal/kgateway/extensions2/plugin"
	agwir "github.com/kgateway-dev/kgateway/v2/pkg/agentgateway/ir"
	pluginsdkir "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
)

// AgentGatewayRouteTranslator handles translation of route IR to agent gateway route resources
type AgentGatewayRouteTranslator struct {
	// ContributedPolicies maps policy GKs to agent-gateway translation passes
	ContributedPolicies map[schema.GroupKind]extensionsplug.PolicyPlugin
	GatewayClassName    string
}

// NewAgentGatewayRouteTranslator creates a new AgentGatewayRouteTranslator
func NewAgentGatewayRouteTranslator(extensions extensionsplug.Plugin) *AgentGatewayRouteTranslator {
	return &AgentGatewayRouteTranslator{
		ContributedPolicies: extensions.ContributesPolicies,
	}
}

// TranslateHttpLikeRoute translates an HttpRouteIR (including GRPC-as-HTTP) to agent gateway Route resources.
func (t *AgentGatewayRouteTranslator) TranslateHttpLikeRoute(
	routeIR pluginsdkir.HttpRouteIR,
	passes map[schema.GroupKind]agwir.AgentGatewayTranslationPass,
) ([]*api.Route, []*api.Policy, error) {
	var routesOut []*api.Route
	var polsOut []*api.Policy
	var acceptanceErrs []error

	for _, rule := range routeIR.Rules {
		// If the rule has an acceptance error, skip generating routes for this rule
		if rule.Err != nil {
			acceptanceErrs = append(acceptanceErrs, rule.Err)
			continue
		}
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
				if err := t.runRouteBackendPolicies(passes, &matchIR, beCtx); err != nil {
					return nil, nil, err
				}
			}
			matchIR := pluginsdkir.HttpRouteRuleMatchIR{
				ExtensionRefs:    rule.ExtensionRefs,
				AttachedPolicies: rule.AttachedPolicies,
				Parent:           &routeIR,
				Match:            match,
			}
			if err := t.runRoutePolicies(passes, &matchIR, r); err != nil {
				return nil, nil, err
			}
			routesOut = append(routesOut, r)
		}
	}
	if len(routesOut) == 0 && len(acceptanceErrs) > 0 {
		return nil, nil, errors.Join(acceptanceErrs...)
	}
	return routesOut, polsOut, nil
}

// TranslateTcpRoute translates a TcpRouteIR to agent gateway TCPRoute resources.
func (t *AgentGatewayRouteTranslator) TranslateTcpRoute(
	routeIR pluginsdkir.TcpRouteIR,
	passes map[schema.GroupKind]agwir.AgentGatewayTranslationPass,
) ([]*api.TCPRoute, []*api.Policy, error) {
	var out []*api.TCPRoute
	var polsOut []*api.Policy
	r := &api.TCPRoute{
		RouteName: fmt.Sprintf("%s/%s", routeIR.Namespace, routeIR.Name),
		RuleName:  "",
	}
	// Backend-level policy application mirrors HTTP backend policy order
	for _, be := range routeIR.Backends {
		if be.BackendObject == nil {
			continue
		}
		rb := &api.RouteBackend{
			Weight:  int32(be.Weight),
			Backend: &api.BackendReference{Kind: &api.BackendReference_Backend{Backend: be.BackendObject.Namespace + "/" + be.BackendObject.Name}},
		}
		r.Backends = append(r.Backends, rb)
		// For TCP, we only have route-level AttachedPolicies on the route itself (no per-match),
		// so run any route-backend policies using a minimal context.
		beCtx := &agwir.AgentGatewayTranslationBackendContext{Backend: be.BackendObject}
		// Construct a minimal HttpRouteRuleMatchIR equivalent for ordering (no ExtensionRefs/Match)
		matchIR := pluginsdkir.HttpRouteRuleMatchIR{
			AttachedPolicies: routeIR.AttachedPolicies,
		}
		if err := t.runRouteBackendPolicies(passes, &matchIR, beCtx); err != nil {
			return nil, nil, err
		}
	}
	// If we decide to support TCP route-level policies at some point, they would go here
	out = append(out, r)
	return out, polsOut, nil
}

// TranslateTlsRoute translates a TlsRouteIR to agent gateway TCPRoute resources (TLS is TCP-level here).
func (t *AgentGatewayRouteTranslator) TranslateTlsRoute(
	routeIR pluginsdkir.TlsRouteIR,
	passes map[schema.GroupKind]agwir.AgentGatewayTranslationPass,
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
		rb := &api.RouteBackend{
			Weight:  int32(be.Weight),
			Backend: &api.BackendReference{Kind: &api.BackendReference_Backend{Backend: be.BackendObject.Namespace + "/" + be.BackendObject.Name}},
		}
		r.Backends = append(r.Backends, rb)
		beCtx := &agwir.AgentGatewayTranslationBackendContext{Backend: be.BackendObject}
		matchIR := pluginsdkir.HttpRouteRuleMatchIR{
			AttachedPolicies: routeIR.AttachedPolicies,
		}
		if err := t.runRouteBackendPolicies(passes, &matchIR, beCtx); err != nil {
			return nil, nil, err
		}
	}
	// TLS route-level policies (non-backend) would be applied here if/when defined for agent-gateway
	out = append(out, r)
	return out, polsOut, nil
}

// runRoutePlugins applies policy passes to a single HttpRouteRuleMatchIR producing agentgateway route fields.
func (t *AgentGatewayRouteTranslator) runRoutePolicies(passes map[schema.GroupKind]agwir.AgentGatewayTranslationPass, in *pluginsdkir.HttpRouteRuleMatchIR, out *api.Route) error {
	var orderedAttachedPolicies pluginsdkir.AttachedPolicies

	// rule-level policies in priority order (high to low)
	orderedAttachedPolicies.Append(in.ExtensionRefs, in.AttachedPolicies)

	// route-level policy
	if in.Parent != nil {
		orderedAttachedPolicies.Append(in.Parent.AttachedPolicies)
	}

	// delegation-level policies in priority order (high to low)
	hierarchicalPriority := 0
	delegatingParent := in.DelegatingParent
	for delegatingParent != nil {
		// parent policies are lower in priority by default, so mark them with their relative priority
		hierarchicalPriority--
		orderedAttachedPolicies.AppendWithPriority(hierarchicalPriority,
			delegatingParent.ExtensionRefs, delegatingParent.AttachedPolicies, delegatingParent.Parent.AttachedPolicies)
		delegatingParent = delegatingParent.DelegatingParent
	}

	var errs []error
	for _, gk := range orderedAttachedPolicies.ApplyOrderedGroupKinds() {
		pols := orderedAttachedPolicies.Policies[gk]
		pass := passes[gk]

		plugin, ok := t.ContributedPolicies[gk]
		if !ok || pass == nil {
			continue
		}

		pctx := &pluginsdkir.RouteContext{
			In:     *in,
			Policy: pols[0].PolicyIr,
			GatewayContext: pluginsdkir.GatewayContext{
				GatewayClassName: t.GatewayClassName,
			},
		}

		mergedPols := mergePolicies(plugin, pols)
		for _, policyAtt := range mergedPols {
			pctx.InheritedPolicyPriority = policyAtt.InheritedPolicyPriority
			if len(policyAtt.Errors) > 0 {
				errs = append(errs, policyAtt.Errors...)
				continue
			}
			pctx.Policy = policyAtt.PolicyIr
			if err := pass.ApplyForRoute(pctx, out); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// runRouteBackendPolicies applies policies attached to a specific backend referenced by the route match.
func (t *AgentGatewayRouteTranslator) runRouteBackendPolicies(
	passes map[schema.GroupKind]agwir.AgentGatewayTranslationPass,
	in *pluginsdkir.HttpRouteRuleMatchIR,
	beCtx *agwir.AgentGatewayTranslationBackendContext,
) error {
	var errs []error
	for gk, pols := range in.AttachedPolicies.Policies {
		plugin, ok := t.ContributedPolicies[gk]
		if !ok {
			continue
		}

		pass := passes[gk]
		if pass == nil {
			continue
		}

		mergedPols := mergePolicies(plugin, pols)
		for _, pa := range mergedPols {
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

// if plugin.MergePolicies is nil, return pols; else return a single merged PolicyAtt slice
func mergePolicies(plugin extensionsplug.PolicyPlugin, pols []pluginsdkir.PolicyAtt) []pluginsdkir.PolicyAtt {
	if plugin.MergePolicies == nil {
		return pols
	}
	merged := plugin.MergePolicies(pols)
	return []pluginsdkir.PolicyAtt{merged}
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

func toDuration(s string) *durationpb.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil
	}
	return durationpb.New(d)
}
