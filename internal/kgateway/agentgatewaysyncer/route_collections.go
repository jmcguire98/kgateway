package agentgatewaysyncer

import (
	"strings"

	"context"

	"github.com/agentgateway/agentgateway/go/api"
	networkingclient "istio.io/client-go/pkg/apis/networking/v1"
	"istio.io/istio/pkg/config"
	"istio.io/istio/pkg/kube/krt"
	isptr "istio.io/istio/pkg/ptr"
	"istio.io/istio/pkg/slices"
	"istio.io/istio/pkg/util/protomarshal"
	"istio.io/istio/pkg/util/sets"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	inf "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"errors"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/krtcollections"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/query"
	httproute "github.com/kgateway-dev/kgateway/v2/internal/kgateway/translator/httproute"
	krtinternal "github.com/kgateway-dev/kgateway/v2/internal/kgateway/utils/krtutil"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	agwir "github.com/kgateway-dev/kgateway/v2/pkg/agentgateway/ir"
	agwtranslator "github.com/kgateway-dev/kgateway/v2/pkg/agentgateway/translator"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk"
	pluginsdkir "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/reporter"
	"github.com/kgateway-dev/kgateway/v2/pkg/reports"
)

func translateHttpIRToADP(krtctx krt.HandlerContext, inputs RouteContextInputs, routeTranslator *agwtranslator.AgentGatewayRouteTranslator, httpIR pluginsdkir.HttpRouteIR) []ADPResourcesForGateway {
	ctx := inputs.WithCtx(krtctx)
	rm := reports.NewReportMap()
	rep := reports.NewReporter(&rm)
	routeReporter := rep.Route(httpIR.SourceObject)

	routeKind := wellknown.HTTPRouteGVK
	// Use GRPCRoute kind when translating normalized GRPC-as-HTTP IRs to satisfy AllowedKinds checks
	if httpIR.ObjectSource.Kind == wellknown.GRPCRouteKind {
		routeKind = wellknown.GRPCRouteGVK
	}

	parentRefs := buildParentReferencesForIR(ctx, httpIR.ParentRefs, toGWHostnames(httpIR.Hostnames), httpIR.Namespace, routeKind)
	passes := newAgentGatewayPasses(inputs.Plugins, rep, httpIR.AttachedPolicies)
	translationCtx := context.WithoutCancel(context.Background())
	if len(parentRefs) == 0 {
		return nil
	}
	routeInfo := inputs.Queries.GetRouteChain(krtctx, translationCtx, &httpIR, httpIR.GetHostnames(), parentRefs[0].OriginalReference)
	if routeInfo == nil {
		return nil
	}
	matches := httproute.TranslateGatewayHTTPRouteRules(translationCtx, routeInfo, routeReporter.ParentRef(&parentRefs[0].OriginalReference), rep)
	// Inspect IR for extension-ref resolution errors (e.g., missing DirectResponse)
	if cond := acceptanceConditionFromIR(httpIR); cond != nil {
		// seed failure early; still attempt translation for consistency with previous behavior
		// downstream will set condition on parent ref
	}

	routesOut, _, err := routeTranslator.TranslateHttpMatches(httpIR, matches, passes)
	var gwResult conversionResult[ADPRoute]
	if err != nil {
		if errors.Is(err, agwtranslator.ErrIncompatibleTerminalFilters) {
			// Keep routes, but mark Accepted=False with IncompatibleFilters
			for _, r := range routesOut {
				gwResult.routes = append(gwResult.routes, ADPRoute{Route: r})
			}
			gwResult.error = &reporter.RouteCondition{Type: gwv1.RouteConditionAccepted, Status: metav1.ConditionFalse, Reason: gwv1.RouteReasonIncompatibleFilters, Message: "terminal filter"}
		} else {
			gwResult.error = &reporter.RouteCondition{Type: gwv1.RouteConditionAccepted, Status: metav1.ConditionFalse, Reason: reporter.RouteRuleDroppedReason}
		}
	} else {
		for _, r := range routesOut {
			gwResult.routes = append(gwResult.routes, ADPRoute{Route: r})
		}
	}
	// If translation succeeded but IR indicated a missing DirectResponse, surface Accepted=False
	if gwResult.error == nil {
		if cond := acceptanceConditionFromIR(httpIR); cond != nil {
			gwResult.error = cond
		}
	}
	attachedRoutes := buildAttachedRoutesMap(parentRefs)
	resourcesPerGateway := processParentReferences(parentRefs, gwResult, httpIR.GetName(), routeReporter, func(e ADPRoute, parent routeParentReference) *api.Resource {
		inner := protomarshal.Clone(e.Route)
		_, name, _ := strings.Cut(parent.InternalName, "/")
		inner.ListenerKey = name
		suffix := "." + string(parent.ParentSection)
		if !strings.HasSuffix(inner.GetKey(), suffix) {
			inner.Key = inner.GetKey() + suffix
		}
		return toADPResource(ADPRoute{Route: inner})
	})
	var results []ADPResourcesForGateway
	for gw, res := range resourcesPerGateway {
		results = append(results, toResourceWithRoutes(gw, res, attachedRoutes[gw], rm))
	}
	return results
}

// acceptanceConditionFromIR inspects IR rule errors to produce an Accepted=False condition
// for missing DirectResponse extension references. The error string from resolution looks like:
// "gateway.kgateway.dev/DirectResponse/<ns>/<name>: policy not found"
func acceptanceConditionFromIR(httpIR pluginsdkir.HttpRouteIR) *reporter.RouteCondition {
	for _, rule := range httpIR.Rules {
		if rule.Err == nil {
			continue
		}
		msg := rule.Err.Error()
		if strings.Contains(msg, "DirectResponse") && strings.Contains(msg, "policy not found") {
			// Extract <ns>/<name>
			// Split on '/' and take the last two elements before the trailing text
			parts := strings.Split(msg, "/")
			ref := ""
			if len(parts) >= 2 {
				tail := parts[len(parts)-1]
				// tail is "<name>: policy not found"
				name := strings.SplitN(tail, ":", 2)[0]
				ns := parts[len(parts)-2]
				ref = ns + "/" + name
			}
			// Fallback to namespace from IR if we failed to parse
			if ref == "" {
				ref = httpIR.Namespace + "/"
			}
			return &reporter.RouteCondition{
				Type:    gwv1.RouteConditionAccepted,
				Status:  metav1.ConditionFalse,
				Reason:  gwv1.RouteReasonBackendNotFound,
				Message: "DirectResponse " + ref + " not found",
			}
		}
	}
	return nil
}

func translateTcpIRToADP(krtctx krt.HandlerContext, inputs RouteContextInputs, routeTranslator *agwtranslator.AgentGatewayRouteTranslator, tcp pluginsdkir.TcpRouteIR) []ADPResourcesForGateway {
	ctx := inputs.WithCtx(krtctx)
	rm := reports.NewReportMap()
	rep := reports.NewReporter(&rm)
	routeReporter := rep.Route(tcp.SourceObject)
	parentRefs := buildParentReferencesForIR(ctx, tcp.ParentRefs, nil, tcp.Namespace, wellknown.TCPRouteGVK)
	passes := newAgentGatewayPasses(inputs.Plugins, rep, tcp.AttachedPolicies)
	tcpOut, _, err := routeTranslator.TranslateTcpRoute(tcp, passes)
	var gwResult conversionResult[ADPTCPRoute]
	if err != nil {
		gwResult.error = &reporter.RouteCondition{Type: gwv1.RouteConditionAccepted, Status: metav1.ConditionFalse, Reason: reporter.RouteRuleDroppedReason}
	} else {
		for _, r := range tcpOut {
			gwResult.routes = append(gwResult.routes, ADPTCPRoute{TCPRoute: r})
		}
	}
	attachedRoutes := buildAttachedRoutesMap(parentRefs)
	resourcesPerGateway := processParentReferences(parentRefs, gwResult, tcp.GetName(), routeReporter, func(e ADPTCPRoute, parent routeParentReference) *api.Resource {
		inner := protomarshal.Clone(e.TCPRoute)
		_, name, _ := strings.Cut(parent.InternalName, "/")
		inner.ListenerKey = name
		inner.Key = inner.GetKey() + "." + string(parent.ParentSection)
		return toADPResource(ADPTCPRoute{TCPRoute: inner})
	})
	var results []ADPResourcesForGateway
	for gw, res := range resourcesPerGateway {
		results = append(results, toResourceWithRoutes(gw, res, attachedRoutes[gw], rm))
	}
	return results
}

func translateTlsIRToADP(krtctx krt.HandlerContext, inputs RouteContextInputs, routeTranslator *agwtranslator.AgentGatewayRouteTranslator, tls pluginsdkir.TlsRouteIR) []ADPResourcesForGateway {
	ctx := inputs.WithCtx(krtctx)
	rm := reports.NewReportMap()
	rep := reports.NewReporter(&rm)
	routeReporter := rep.Route(tls.SourceObject)
	parentRefs := buildParentReferencesForIR(ctx, tls.ParentRefs, toGWHostnames(tls.Hostnames), tls.Namespace, wellknown.TLSRouteGVK)
	passes := newAgentGatewayPasses(inputs.Plugins, rep, tls.AttachedPolicies)
	tlsOut, _, err := routeTranslator.TranslateTlsRoute(tls, passes)
	var gwResult conversionResult[ADPTCPRoute]
	if err != nil {
		gwResult.error = &reporter.RouteCondition{Type: gwv1.RouteConditionAccepted, Status: metav1.ConditionFalse, Reason: reporter.RouteRuleDroppedReason}
	} else {
		for _, r := range tlsOut {
			gwResult.routes = append(gwResult.routes, ADPTCPRoute{TCPRoute: r})
		}
	}
	attachedRoutes := buildAttachedRoutesMap(parentRefs)
	resourcesPerGateway := processParentReferences(parentRefs, gwResult, tls.GetName(), routeReporter, func(e ADPTCPRoute, parent routeParentReference) *api.Resource {
		inner := protomarshal.Clone(e.TCPRoute)
		_, name, _ := strings.Cut(parent.InternalName, "/")
		inner.ListenerKey = name
		inner.Key = inner.GetKey() + "." + string(parent.ParentSection)
		return toADPResource(ADPTCPRoute{TCPRoute: inner})
	})
	var results []ADPResourcesForGateway
	for gw, res := range resourcesPerGateway {
		results = append(results, toResourceWithRoutes(gw, res, attachedRoutes[gw], rm))
	}
	return results
}

// ADPRouteCollectionFromRoutesIndex creates the collection of translated routes using the shared RoutesIndex IR
func ADPRouteCollectionFromRoutesIndex(
	routes *krtcollections.RoutesIndex,
	inputs RouteContextInputs,
	krtopts krtinternal.KrtOptions,
	routeTranslator *agwtranslator.AgentGatewayRouteTranslator,
) krt.Collection[ADPResourcesForGateway] {
	httpRoutes := krt.NewManyCollection(routes.HTTPRoutes(), func(krtctx krt.HandlerContext, httpIR pluginsdkir.HttpRouteIR) []ADPResourcesForGateway {
		return translateHttpIRToADP(krtctx, inputs, routeTranslator, httpIR)
	}, krtopts.ToOptions("ADPHTTPRoutesFromIR")...)

	grpcRoutes := krt.NewManyCollection(routes.GRPCRoutesAsHTTP(), func(krtctx krt.HandlerContext, httpIR pluginsdkir.HttpRouteIR) []ADPResourcesForGateway {
		return translateHttpIRToADP(krtctx, inputs, routeTranslator, httpIR)
	}, krtopts.ToOptions("ADPHttpLikeRoutesFromIR")...)

	tcpRoutes := krt.NewManyCollection(routes.TCPRoutes(), func(krtctx krt.HandlerContext, tcp pluginsdkir.TcpRouteIR) []ADPResourcesForGateway {
		return translateTcpIRToADP(krtctx, inputs, routeTranslator, tcp)
	}, krtopts.ToOptions("ADPTCPRoutesFromIR")...)

	tlsRoutes := krt.NewManyCollection(routes.TLSRoutes(), func(krtctx krt.HandlerContext, tls pluginsdkir.TlsRouteIR) []ADPResourcesForGateway {
		return translateTlsIRToADP(krtctx, inputs, routeTranslator, tls)
	}, krtopts.ToOptions("ADPTLSRoutesFromIR")...)

	return krt.JoinCollection([]krt.Collection[ADPResourcesForGateway]{httpRoutes, grpcRoutes, tcpRoutes, tlsRoutes}, krtopts.ToOptions("ADPRoutesFromIR")...)
}

// buildParentReferencesForIR builds parent references for a routeIR
func buildParentReferencesForIR(ctx RouteContext, routeRefs []gwv1.ParentReference, hostnames []gwv1.Hostname, localNamespace string, kind schema.GroupVersionKind) []routeParentReference {
	var parentRefs []routeParentReference
	for _, ref := range routeRefs {
		irKey, err := toInternalParentReference(ref, localNamespace)
		if err != nil {
			// Cannot handle the reference. Maybe it is for another controller, so we just ignore it
			continue
		}
		pk := parentReference{
			parentKey:   irKey,
			SectionName: isptr.OrEmpty(ref.SectionName),
			Port:        isptr.OrEmpty(ref.Port),
		}
		currentParents := ctx.RouteParents.fetch(ctx.Krt, irKey)
		appendParent := func(pr *parentInfo, pk parentReference) {
			bannedHostnames := sets.New[string]()
			for _, gw := range currentParents {
				if gw == pr {
					continue
				}
				if gw.Port != pr.Port {
					continue
				}
				if gw.Protocol != pr.Protocol {
					continue
				}
				bannedHostnames.Insert(gw.OriginalHostname)
			}
			deniedReason := referenceAllowed(ctx, pr, kind, pk, hostnames, localNamespace)
			rpi := routeParentReference{
				InternalName:      pr.InternalName,
				InternalKind:      irKey.Kind,
				Hostname:          pr.OriginalHostname,
				DeniedReason:      deniedReason,
				OriginalReference: ref,
				BannedHostnames:   bannedHostnames,
				ParentKey:         irKey,
				ParentSection:     pr.SectionName,
			}
			parentRefs = append(parentRefs, rpi)
		}
		for _, gw := range currentParents {
			appendParent(gw, pk)
		}
	}
	slices.SortBy(parentRefs, func(a routeParentReference) string { return parentRefString(a.OriginalReference) })
	return parentRefs
}

// toGWHostnames converts a list of strings to a list of gwv1.Hostname
func toGWHostnames(in []string) []gwv1.Hostname {
	if len(in) == 0 {
		return nil
	}
	out := make([]gwv1.Hostname, len(in))
	for i, s := range in {
		out[i] = gwv1.Hostname(s)
	}
	return out
}

// buildAttachedRoutesMap builds a map of gateway -> section name -> route count
func buildAttachedRoutesMap(parentRefs []routeParentReference) map[types.NamespacedName]map[string]uint {
	attachedRoutes := make(map[types.NamespacedName]map[string]uint)
	for _, parent := range filteredReferences(parentRefs) {
		if parent.ParentKey.Kind != wellknown.GatewayGVK {
			continue
		}
		parentGw := types.NamespacedName{
			Namespace: parent.ParentKey.Namespace,
			Name:      parent.ParentKey.Name,
		}
		if attachedRoutes[parentGw] == nil {
			attachedRoutes[parentGw] = make(map[string]uint)
		}
		attachedRoutes[parentGw][string(parent.ParentSection)]++
	}
	return attachedRoutes
}

// processParentReferences processes filtered parent references and builds resources per gateway
func processParentReferences[T any](
	parentRefs []routeParentReference,
	gwResult conversionResult[T],
	objName string,
	routeReporter reporter.RouteReporter,
	resourceMapper func(T, routeParentReference) *api.Resource,
) map[types.NamespacedName][]*api.Resource {
	resourcesPerGateway := make(map[types.NamespacedName][]*api.Resource)

	for _, parent := range filteredReferences(parentRefs) {
		// Always create a route reporter entry for the parent ref
		parentRefReporter := routeReporter.ParentRef(&parent.OriginalReference)

		// for gwv1beta1 routes, build one VS per gwv1beta1+host
		routes := gwResult.routes
		if len(routes) == 0 {
			logger.Debug("no routes for parent", "route_name", objName, "parent", parent.ParentKey)
			continue
		}
		if gwResult.error != nil {
			parentRefReporter.SetCondition(*gwResult.error)
		}

		gw := types.NamespacedName{
			Namespace: parent.ParentKey.Namespace,
			Name:      parent.ParentKey.Name,
		}
		if resourcesPerGateway[gw] == nil {
			resourcesPerGateway[gw] = make([]*api.Resource, 0)
		}
		resourcesPerGateway[gw] = append(resourcesPerGateway[gw], slices.Map(routes, func(e T) *api.Resource {
			return resourceMapper(e, parent)
		})...)
	}
	return resourcesPerGateway
}

type conversionResult[O any] struct {
	error  *reporter.RouteCondition
	routes []O
}

func newAgentGatewayPasses(plugs pluginsdk.Plugin,
	rep reporter.Reporter,
	aps pluginsdkir.AttachedPolicies) map[schema.GroupKind]agwir.AgentGatewayTranslationPass {
	out := map[schema.GroupKind]agwir.AgentGatewayTranslationPass{}

	// Always include the builtin pass so rule-level builtins (eg timeouts/retries) are applied
	if plugin, ok := plugs.ContributesPolicies[pluginsdkir.VirtualBuiltInGK]; ok && plugin.NewAgentGatewayPass != nil {
		out[pluginsdkir.VirtualBuiltInGK] = plugin.NewAgentGatewayPass(rep)
	}
	for gk := range aps.Policies {
		plugin, ok := plugs.ContributesPolicies[gk]
		if !ok || plugin.NewAgentGatewayPass == nil {
			continue
		}
		// Instantiate pass for any GK that appears in attached policies and has defined a NewAgentGatewayPass
		out[gk] = plugin.NewAgentGatewayPass(rep)
	}

	return out
}

// RouteContext defines a common set of inputs to a route collection for agentgateway.
// This should be built once per route translation and not shared outside of that.
// The embedded RouteContextInputs is typically based into a collection, then translated to a RouteContext with RouteContextInputs.WithCtx().
type RouteContext struct {
	Krt krt.HandlerContext
	RouteContextInputs
	AttachedPolicies pluginsdkir.AttachedPolicies
}

type RouteContextInputs struct {
	Grants          ReferenceGrants
	RouteParents    RouteParents
	Services        krt.Collection[*corev1.Service]
	InferencePools  krt.Collection[*inf.InferencePool]
	Namespaces      krt.Collection[*corev1.Namespace]
	ServiceEntries  krt.Collection[*networkingclient.ServiceEntry]
	Backends        *krtcollections.BackendIndex
	Policies        *krtcollections.PolicyIndex
	Plugins         pluginsdk.Plugin
	DirectResponses krt.Collection[*v1alpha1.DirectResponse]
	Queries         query.GatewayQueries
}

func (i RouteContextInputs) WithCtx(krtctx krt.HandlerContext) RouteContext {
	return RouteContext{
		Krt:                krtctx,
		RouteContextInputs: i,
	}
}

type RouteWithKey struct {
	*Config
	Key string
}

func (r RouteWithKey) ResourceName() string {
	return config.NamespacedName(r.Config).String()
}

func (r RouteWithKey) Equals(o RouteWithKey) bool {
	return r.Config.Equals(o.Config)
}
