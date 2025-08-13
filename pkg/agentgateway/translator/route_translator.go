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
	GatewayClassName    string
}

// NewAgentGatewayRouteTranslator creates a new AgentGatewayRouteTranslator
func NewAgentGatewayRouteTranslator(extensions extensionsplug.Plugin) *AgentGatewayRouteTranslator {
	return &AgentGatewayRouteTranslator{
		ContributedPolicies: extensions.ContributesPolicies,
	}
}

// TranslateHttpMatches translates a list of precomputed HttpRouteRuleMatchIRs (ideally flattened with delegation)
// into agent gateway Route resources.
func (t *AgentGatewayRouteTranslator) TranslateHttpMatches(
	routeIR pluginsdkir.HttpRouteIR,
	matches []pluginsdkir.HttpRouteRuleMatchIR,
	passes map[schema.GroupKind]agwir.AgentGatewayTranslationPass,
) ([]*api.Route, []*api.Policy, error) {
	var routesOut []*api.Route
	var polsOut []*api.Policy
	var acceptanceErrs []error
	for _, matchIR := range matches {
		if matchIR.RouteAcceptanceError != nil {
			acceptanceErrs = append(acceptanceErrs, matchIR.RouteAcceptanceError)
			continue
		}
		r := &api.Route{
			RouteName: fmt.Sprintf("%s/%s", routeIR.Namespace, routeIR.Name),
			RuleName:  matchIR.Name,
			Hostnames: routeIR.GetHostnames(),
		}
		if rm, err := buildRouteMatchFromGW(matchIR.Match); err != nil {
			return nil, nil, err
		} else if rm != nil {
			r.Matches = append(r.Matches, rm)
		}
		for _, be := range matchIR.Backends {
			if be.Backend.BackendObject == nil {
				continue
			}
			rb := &api.RouteBackend{
				Weight:  int32(be.Backend.Weight),
				Backend: &api.BackendReference{Kind: &api.BackendReference_Backend{Backend: be.Backend.BackendObject.Namespace + "/" + be.Backend.BackendObject.Name}},
			}
			r.Backends = append(r.Backends, rb)
			beCtx := &agwir.AgentGatewayTranslationBackendContext{Backend: be.Backend.BackendObject}
			backendMatchIR := matchIR
			if be.AttachedPolicies.Policies != nil {
				backendMatchIR.AttachedPolicies = be.AttachedPolicies
			} else {
				backendMatchIR.AttachedPolicies = pluginsdkir.AttachedPolicies{Policies: map[schema.GroupKind][]pluginsdkir.PolicyAtt{}}
			}
			if err := t.runRouteBackendPolicies(passes, &backendMatchIR, beCtx); err != nil {
				return nil, nil, err
			}
		}
		if err := t.runRoutePolicies(passes, &matchIR, r); err != nil {
			return nil, nil, err
		}
		routesOut = append(routesOut, r)
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
	return t.translateTcpLike(
		fmt.Sprintf("%s/%s", routeIR.Namespace, routeIR.Name),
		nil,
		routeIR.AttachedPolicies,
		routeIR.Backends,
		passes,
	)
}

// TranslateTlsRoute translates a TlsRouteIR to agent gateway TCPRoute resources (TLS is TCP-level here).
func (t *AgentGatewayRouteTranslator) TranslateTlsRoute(
	routeIR pluginsdkir.TlsRouteIR,
	passes map[schema.GroupKind]agwir.AgentGatewayTranslationPass,
) ([]*api.TCPRoute, []*api.Policy, error) {
	return t.translateTcpLike(
		fmt.Sprintf("%s/%s", routeIR.Namespace, routeIR.Name),
		routeIR.GetHostnames(),
		routeIR.AttachedPolicies,
		routeIR.Backends,
		passes,
	)
}

// translateTcpLike is a shared helper for TCP/TLS route translation.
// hostnames may be nil for pure TCP; non-nil (SNI) for TLS.
func (t *AgentGatewayRouteTranslator) translateTcpLike(
	routeName string,
	hostnames []string,
	attached pluginsdkir.AttachedPolicies,
	backends []pluginsdkir.BackendRefIR,
	passes map[schema.GroupKind]agwir.AgentGatewayTranslationPass,
) ([]*api.TCPRoute, []*api.Policy, error) {
	var out []*api.TCPRoute
	var polsOut []*api.Policy
	r := &api.TCPRoute{
		RouteName: routeName,
		RuleName:  "",
		Hostnames: hostnames,
	}
	for _, be := range backends {
		if be.BackendObject == nil {
			continue
		}
		rb := &api.RouteBackend{
			Weight:  int32(be.Weight),
			Backend: &api.BackendReference{Kind: &api.BackendReference_Backend{Backend: be.BackendObject.Namespace + "/" + be.BackendObject.Name}},
		}
		r.Backends = append(r.Backends, rb)
		beCtx := &agwir.AgentGatewayTranslationBackendContext{Backend: be.BackendObject}
		matchIR := pluginsdkir.HttpRouteRuleMatchIR{AttachedPolicies: attached}
		if err := t.runRouteBackendPolicies(passes, &matchIR, beCtx); err != nil {
			return nil, nil, err
		}
	}
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

// buildHttpMatchIR creates a clean HttpRouteRuleMatchIR for a single match
func buildHttpMatchIR(routeIR pluginsdkir.HttpRouteIR, rule pluginsdkir.HttpRouteRuleIR, match gwv1.HTTPRouteMatch) pluginsdkir.HttpRouteRuleMatchIR {
	return pluginsdkir.HttpRouteRuleMatchIR{
		ExtensionRefs:    rule.ExtensionRefs,
		AttachedPolicies: rule.AttachedPolicies,
		Parent:           &routeIR,
		Match:            match,
	}
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

// buildRouteMatchFromGW builds a single RouteMatch from the HTTPRouteMatch fields,
// creating the struct once and populating optional sections if present.
func buildRouteMatchFromGW(match gwv1.HTTPRouteMatch) (*api.RouteMatch, error) {
	var out api.RouteMatch
	if pm, err := buildPathMatchFromGW(match); err != nil {
		return nil, err
	} else if pm != nil {
		out.Path = pm
	}
	if hm, err := buildHeadersMatchFromGW(match); err != nil {
		return nil, err
	} else if len(hm) > 0 {
		out.Headers = hm
	}
	if mm, err := buildMethodMatchFromGW(match); err != nil {
		return nil, err
	} else if mm != nil {
		out.Method = mm
	}
	if qm, err := buildQueryMatchFromGW(match); err != nil {
		return nil, err
	} else if len(qm) > 0 {
		out.QueryParams = qm
	}
	// If nothing set, return nil to indicate no match block was created
	if out.Path == nil && out.Method == nil && len(out.Headers) == 0 && len(out.QueryParams) == 0 {
		return nil, nil
	}
	return &out, nil
}
