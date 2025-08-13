package agentgatewaysyncer

import (
	"iter"
	"strings"

	"github.com/agentgateway/agentgateway/go/api"
	networkingclient "istio.io/client-go/pkg/apis/networking/v1"
	"istio.io/istio/pkg/config"
	"istio.io/istio/pkg/kube/controllers"
	"istio.io/istio/pkg/kube/krt"
	isptr "istio.io/istio/pkg/ptr"
	"istio.io/istio/pkg/slices"
	"istio.io/istio/pkg/util/protomarshal"
	"istio.io/istio/pkg/util/sets"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	inf "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/krtcollections"
	krtinternal "github.com/kgateway-dev/kgateway/v2/internal/kgateway/utils/krtutil"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	agwir "github.com/kgateway-dev/kgateway/v2/pkg/agentgateway/ir"
	agwtranslator "github.com/kgateway-dev/kgateway/v2/pkg/agentgateway/translator"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk"
	pluginsdkir "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/reporter"
	"github.com/kgateway-dev/kgateway/v2/pkg/reports"
)

// ADPRouteCollectionFromRoutesIndex creates the collection of translated routes using the shared RoutesIndex IR
func ADPRouteCollectionFromRoutesIndex(
	routes *krtcollections.RoutesIndex,
	inputs RouteContextInputs,
	krtopts krtinternal.KrtOptions,
	routeTranslator *agwtranslator.AgentGatewayRouteTranslator,
) krt.Collection[ADPResourcesForGateway] {
	// HTTP and GRPC (normalized to HTTPRouteIR via RoutesIndex)
	httpRoutes := krt.NewManyCollection(routes.HTTPRoutes(), func(krtctx krt.HandlerContext, httpIR pluginsdkir.HttpRouteIR) []ADPResourcesForGateway {
		ctx := inputs.WithCtx(krtctx)
		rm := reports.NewReportMap()
		rep := reports.NewReporter(&rm)
		routeReporter := rep.Route(httpIR.SourceObject)

		// compute parent refs using existing logic
		// Build parent refs directly from IR to avoid raw object dependency
		parentRefs := buildParentReferencesForIR(ctx, httpIR.ParentRefs, toGWHostnames(httpIR.Hostnames), httpIR.Namespace, wellknown.HTTPRouteGVK)

		passes := newAgentGatewayPasses(inputs.Plugins, rep, httpIR.AttachedPolicies)
		routesOut, _, err := routeTranslator.TranslateHttpLikeRoute(httpIR, passes)
		var gwResult conversionResult[ADPRoute]
		if err != nil {
			gwResult.error = &reporter.RouteCondition{Type: gwv1.RouteConditionAccepted, Status: metav1.ConditionFalse, Reason: reporter.RouteRuleDroppedReason}
		} else {
			for _, r := range routesOut {
				gwResult.routes = append(gwResult.routes, ADPRoute{Route: r})
			}
		}

		attachedRoutes := buildAttachedRoutesMap(parentRefs)
		resourcesPerGateway := processParentReferences(
			parentRefs,
			gwResult,
			httpIR.GetName(),
			routeReporter,
			func(e ADPRoute, parent routeParentReference) *api.Resource {
				inner := protomarshal.Clone(e.Route)
				_, name, _ := strings.Cut(parent.InternalName, "/")
				inner.ListenerKey = name
				inner.Key = inner.GetKey() + "." + string(parent.ParentSection)
				return toADPResource(ADPRoute{Route: inner})
			},
		)

		var results []ADPResourcesForGateway
		for gw, res := range resourcesPerGateway {
			results = append(results, toResourceWithRoutes(gw, res, attachedRoutes[gw], rm))
		}
		return results
	}, krtopts.ToOptions("ADPHTTPRoutesFromIR")...)

	grpcRoutes := krt.NewManyCollection(routes.AllRoutes(), func(krtctx krt.HandlerContext, wrapper krtcollections.RouteWrapper) []ADPResourcesForGateway {
		httpIR, ok := wrapper.Route.(*pluginsdkir.HttpRouteIR)
		if !ok {
			return nil
		}
		ctx := inputs.WithCtx(krtctx)
		rm := reports.NewReportMap()
		rep := reports.NewReporter(&rm)
		routeReporter := rep.Route(httpIR.SourceObject)

		parentRefs := buildParentReferencesForIR(ctx, httpIR.ParentRefs, toGWHostnames(httpIR.Hostnames), httpIR.Namespace, wellknown.HTTPRouteGVK)
		passes := newAgentGatewayPasses(inputs.Plugins, rep, httpIR.AttachedPolicies)
		routesOut, _, err := routeTranslator.TranslateHttpLikeRoute(*httpIR, passes)
		var gwResult conversionResult[ADPRoute]
		if err != nil {
			gwResult.error = &reporter.RouteCondition{Type: gwv1.RouteConditionAccepted, Status: metav1.ConditionFalse, Reason: reporter.RouteRuleDroppedReason}
		} else {
			for _, r := range routesOut {
				gwResult.routes = append(gwResult.routes, ADPRoute{Route: r})
			}
		}

		attachedRoutes := buildAttachedRoutesMap(parentRefs)
		resourcesPerGateway := processParentReferences(
			parentRefs, gwResult, httpIR.GetName(), routeReporter,
			func(e ADPRoute, parent routeParentReference) *api.Resource {
				inner := protomarshal.Clone(e.Route)
				_, name, _ := strings.Cut(parent.InternalName, "/")
				inner.ListenerKey = name
				inner.Key = inner.GetKey() + "." + string(parent.ParentSection)
				return toADPResource(ADPRoute{Route: inner})
			},
		)
		var results []ADPResourcesForGateway
		for gw, res := range resourcesPerGateway {
			results = append(results, toResourceWithRoutes(gw, res, attachedRoutes[gw], rm))
		}
		return results
	}, krtopts.ToOptions("ADPHttpLikeRoutesFromIR")...)

	tcpRoutes := krt.NewManyCollection(routes.AllRoutes(), func(krtctx krt.HandlerContext, wrapper krtcollections.RouteWrapper) []ADPResourcesForGateway {
		tcpIR, ok := wrapper.Route.(*pluginsdkir.TcpRouteIR)
		if !ok {
			return nil
		}
		ctx := inputs.WithCtx(krtctx)
		rm := reports.NewReportMap()
		rep := reports.NewReporter(&rm)
		routeReporter := rep.Route(tcpIR.SourceObject)
		parentRefs := buildParentReferencesForIR(ctx, tcpIR.ParentRefs, nil, tcpIR.Namespace, wellknown.TCPRouteGVK)
		passes := newAgentGatewayPasses(inputs.Plugins, rep, tcpIR.AttachedPolicies)
		tcpOut, _, err := routeTranslator.TranslateTcpRoute(*tcpIR, passes)
		var gwResult conversionResult[ADPTCPRoute]
		if err != nil {
			gwResult.error = &reporter.RouteCondition{Type: gwv1.RouteConditionAccepted, Status: metav1.ConditionFalse, Reason: reporter.RouteRuleDroppedReason}
		} else {
			for _, r := range tcpOut {
				gwResult.routes = append(gwResult.routes, ADPTCPRoute{TCPRoute: r})
			}
		}
		attachedRoutes := buildAttachedRoutesMap(parentRefs)
		resourcesPerGateway := processParentReferences(parentRefs, gwResult, tcpIR.GetName(), routeReporter, func(e ADPTCPRoute, parent routeParentReference) *api.Resource {
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
	}, krtopts.ToOptions("ADPTCPRoutesFromIR")...)

	tlsRoutes := krt.NewManyCollection(routes.AllRoutes(), func(krtctx krt.HandlerContext, wrapper krtcollections.RouteWrapper) []ADPResourcesForGateway {
		tlsIR, ok := wrapper.Route.(*pluginsdkir.TlsRouteIR)
		if !ok {
			return nil
		}
		ctx := inputs.WithCtx(krtctx)
		rm := reports.NewReportMap()
		rep := reports.NewReporter(&rm)
		routeReporter := rep.Route(tlsIR.SourceObject)
		parentRefs := buildParentReferencesForIR(ctx, tlsIR.ParentRefs, toGWHostnames(tlsIR.Hostnames), tlsIR.Namespace, wellknown.TLSRouteGVK)
		passes := newAgentGatewayPasses(inputs.Plugins, rep, tlsIR.AttachedPolicies)
		tlsOut, _, err := routeTranslator.TranslateTlsRoute(*tlsIR, passes)
		var gwResult conversionResult[ADPTCPRoute]
		if err != nil {
			gwResult.error = &reporter.RouteCondition{Type: gwv1.RouteConditionAccepted, Status: metav1.ConditionFalse, Reason: reporter.RouteRuleDroppedReason}
		} else {
			for _, r := range tlsOut {
				gwResult.routes = append(gwResult.routes, ADPTCPRoute{TCPRoute: r})
			}
		}
		attachedRoutes := buildAttachedRoutesMap(parentRefs)
		resourcesPerGateway := processParentReferences(parentRefs, gwResult, tlsIR.GetName(), routeReporter, func(e ADPTCPRoute, parent routeParentReference) *api.Resource {
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
	}, krtopts.ToOptions("ADPTLSRoutesFromIR")...)

	return krt.JoinCollection([]krt.Collection[ADPResourcesForGateway]{httpRoutes, grpcRoutes, tcpRoutes, tlsRoutes}, krtopts.ToOptions("ADPRoutesFromIR")...)
}

// buildParentReferencesForIR mirrors extractParentReferenceInfo but takes IR inputs to avoid depending on controllers.Object
func buildParentReferencesForIR(ctx RouteContext, routeRefs []gwv1.ParentReference, hostnames []gwv1.Hostname, localNamespace string, kind schema.GroupVersionKind) []routeParentReference {
	var parentRefs []routeParentReference
	for _, ref := range routeRefs {
		irKey, err := toInternalParentReference(ref, localNamespace)
		if err != nil {
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
				BannedHostnames:   bannedHostnames, // uses k8s.io/apimachinery/pkg/util/sets like conversion.go
				ParentKey:         irKey,
				ParentSection:     pr.SectionName,
			}
			parentRefs = append(parentRefs, rpi)
		}
		for _, gw := range currentParents {
			appendParent(gw, pk)
		}
	}
	// Ensure stable order
	slices.SortBy(parentRefs, func(a routeParentReference) string { return parentRefString(a.OriginalReference) })
	return parentRefs
}

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

func defaultInt32(v *gwv1.PortNumber, def gwv1.PortNumber) gwv1.PortNumber {
	if v == nil {
		return def
	}
	return *v
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

// createRouteCollection is a generic helper function that creates a KRT collection for any route type
// by extracting the common logic shared between HTTP and GRPC route collections
func createRouteCollection[T controllers.Object](
	routeCol krt.Collection[T],
	inputs RouteContextInputs,
	krtopts krtinternal.KrtOptions,
	plugins pluginsdk.Plugin,
	collectionName string,
	translator func(ctx RouteContext, obj T, rep reporter.Reporter) (RouteContext, iter.Seq2[ADPRoute, *reporter.RouteCondition]),
) krt.Collection[ADPResourcesForGateway] {
	return krt.NewManyCollection(routeCol, func(krtctx krt.HandlerContext, obj T) []ADPResourcesForGateway {
		logger.Debug("translating route", "route_name", obj.GetName(), "resource_version", obj.GetResourceVersion())

		ctx := inputs.WithCtx(krtctx)
		rm := reports.NewReportMap()
		rep := reports.NewReporter(&rm)
		routeReporter := rep.Route(obj)

		// Apply route-specific preprocessing and get the translator
		ctx, translatorSeq := translator(ctx, obj, rep)

		parentRefs, gwResult := computeRoute(ctx, obj, func(obj T) iter.Seq2[ADPRoute, *reporter.RouteCondition] {
			return translatorSeq
		})

		// gateway -> section name -> route count
		attachedRoutes := buildAttachedRoutesMap(parentRefs)

		resourcesPerGateway := processParentReferences(
			parentRefs,
			gwResult,
			obj.GetName(),
			routeReporter,
			func(e ADPRoute, parent routeParentReference) *api.Resource {
				inner := protomarshal.Clone(e.Route)
				_, name, _ := strings.Cut(parent.InternalName, "/")
				inner.ListenerKey = name
				inner.Key = inner.GetKey() + "." + string(parent.ParentSection)
				return toADPResource(ADPRoute{Route: inner})
			},
		)

		var results []ADPResourcesForGateway
		for gw, res := range resourcesPerGateway {
			var attachedRoutesForGw map[string]uint
			if attachedRoutes[gw] != nil {
				attachedRoutesForGw = attachedRoutes[gw]
			}
			results = append(results, toResourceWithRoutes(gw, res, attachedRoutesForGw, rm))
		}
		return results
	}, krtopts.ToOptions(collectionName)...)
}

// createTCPRouteCollection is a generic helper function that creates a KRT collection for any route type
// by extracting the common logic shared between TCP and TLS route collections
func createTCPRouteCollection[T controllers.Object](
	routeCol krt.Collection[T],
	inputs RouteContextInputs,
	krtopts krtinternal.KrtOptions,
	plugins pluginsdk.Plugin,
	collectionName string,
	translator func(ctx RouteContext, obj T, rep reporter.Reporter) (RouteContext, iter.Seq2[ADPTCPRoute, *reporter.RouteCondition]),
) krt.Collection[ADPResourcesForGateway] {
	return krt.NewManyCollection(routeCol, func(krtctx krt.HandlerContext, obj T) []ADPResourcesForGateway {
		logger.Debug("translating route", "route_name", obj.GetName(), "resource_version", obj.GetResourceVersion())

		ctx := inputs.WithCtx(krtctx)
		rm := reports.NewReportMap()
		rep := reports.NewReporter(&rm)
		routeReporter := rep.Route(obj)

		// Apply route-specific preprocessing and get the translator
		ctx, translatorSeq := translator(ctx, obj, rep)

		parentRefs, gwResult := computeRoute(ctx, obj, func(obj T) iter.Seq2[ADPTCPRoute, *reporter.RouteCondition] {
			return translatorSeq
		})

		// gateway -> section name -> route count
		attachedRoutes := buildAttachedRoutesMap(parentRefs)

		resourcesPerGateway := processParentReferences(
			parentRefs,
			gwResult,
			obj.GetName(),
			routeReporter,
			func(e ADPTCPRoute, parent routeParentReference) *api.Resource {
				inner := protomarshal.Clone(e.TCPRoute)
				_, name, _ := strings.Cut(parent.InternalName, "/")
				inner.ListenerKey = name
				inner.Key = inner.GetKey() + "." + string(parent.ParentSection)
				return toADPResource(ADPTCPRoute{TCPRoute: inner})
			},
		)

		var results []ADPResourcesForGateway
		for gw, res := range resourcesPerGateway {
			var attachedRoutesForGw map[string]uint
			if attachedRoutes[gw] != nil {
				attachedRoutesForGw = attachedRoutes[gw]
			}
			results = append(results, toResourceWithRoutes(gw, res, attachedRoutesForGw, rm))
		}
		return results
	}, krtopts.ToOptions(collectionName)...)
}

type conversionResult[O any] struct {
	error  *reporter.RouteCondition
	routes []O
}

// IsNil works around comparing generic types
func IsNil[O comparable](o O) bool {
	var t O
	return o == t
}

func newAgentGatewayPasses(plugs pluginsdk.Plugin,
	rep reporter.Reporter,
	aps pluginsdkir.AttachedPolicies) map[schema.GroupKind]agwir.AgentGatewayTranslationPass {
	out := map[schema.GroupKind]agwir.AgentGatewayTranslationPass{}
	if len(aps.Policies) == 0 {
		return out
	}
	for gk, paList := range aps.Policies {
		plugin, ok := plugs.ContributesPolicies[gk]
		if !ok || plugin.NewAgentGatewayPass == nil {
			continue
		}
		if len(paList) == 0 && gk != pluginsdkir.VirtualBuiltInGK {
			continue
		}
		out[gk] = plugin.NewAgentGatewayPass(rep)
	}
	return out
}

// computeRoute holds the common route building logic shared amongst all types
func computeRoute[T controllers.Object, O comparable](ctx RouteContext, obj T, translator func(
	obj T,
) iter.Seq2[O, *reporter.RouteCondition],
) ([]routeParentReference, conversionResult[O]) {
	parentRefs := extractParentReferenceInfo(ctx, ctx.RouteParents, obj)

	convertRules := func() conversionResult[O] {
		res := conversionResult[O]{}
		for vs, err := range translator(obj) {
			// This was a hard error
			if err != nil && IsNil(vs) {
				res.error = err
				return conversionResult[O]{error: err}
			}
			// Got an error but also routes
			if err != nil {
				res.error = err
			}
			res.routes = append(res.routes, vs)
		}
		return res
	}
	gwResult := buildGatewayRoutes(convertRules)

	return parentRefs, gwResult
}

// RouteContext defines a common set of inputs to a route collection for agentgateway.
// This should be built once per route translation and not shared outside of that.
// The embedded RouteContextInputs is typically based into a collection, then translated to a RouteContext with RouteContextInputs.WithCtx().
type RouteContext struct {
	Krt krt.HandlerContext
	RouteContextInputs
	AttachedPolicies pluginsdkir.AttachedPolicies
	pluginPasses     []agwir.AgentGatewayTranslationPass
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

// buildGatewayRoutes contains common logic to build a set of routes with gwv1beta1 semantics
func buildGatewayRoutes[T any](convertRules func() T) T {
	return convertRules()
}

// attachRoutePolicies populates ctx.AttachedPolicies with policies that
// target the given HTTPRoute. It uses the exported LookupTargetingPolicies
// from PolicyIndex.
func attachRoutePolicies(ctx *RouteContext, route *gwv1.HTTPRoute) {
	if ctx.Backends == nil {
		return
	}
	pi := ctx.Backends.PolicyIndex()
	if pi == nil {
		return
	}

	target := pluginsdkir.ObjectSource{
		Group:     wellknown.HTTPRouteGVK.Group,
		Kind:      wellknown.HTTPRouteGVK.Kind,
		Namespace: route.Namespace,
		Name:      route.Name,
	}

	pols := pi.LookupTargetingPolicies(ctx.Krt,
		pluginsdk.RouteAttachmentPoint,
		target,
		"", // route-level
		route.GetLabels())

	aps := pluginsdkir.AttachedPolicies{Policies: map[schema.GroupKind][]pluginsdkir.PolicyAtt{}}
	for _, pa := range pols {
		a := aps.Policies[pa.GroupKind]
		aps.Policies[pa.GroupKind] = append(a, pa)
	}

	if _, ok := aps.Policies[pluginsdkir.VirtualBuiltInGK]; !ok {
		aps.Policies[pluginsdkir.VirtualBuiltInGK] = nil
	}
	ctx.AttachedPolicies = aps
}
