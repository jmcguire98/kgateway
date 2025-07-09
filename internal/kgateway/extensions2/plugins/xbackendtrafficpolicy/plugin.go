package xbackendtrafficpolicy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	stateful_sessionv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/stateful_session/v3"
	stateful_cookie "github.com/envoyproxy/go-control-plane/envoy/extensions/http/stateful_session/cookie/v3"
	stateful_header "github.com/envoyproxy/go-control-plane/envoy/extensions/http/stateful_session/header/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/type/http/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	skubeclient "istio.io/istio/pkg/config/schema/kubeclient"
	"istio.io/istio/pkg/kube/kclient"
	"istio.io/istio/pkg/kube/krt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwxv1a1 "sigs.k8s.io/gateway-api/apisx/v1alpha1"

	envoy_config_cluster_v3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/extensions2/common"
	extensionsplug "github.com/kgateway-dev/kgateway/v2/internal/kgateway/extensions2/plugin"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/ir"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/plugins"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/reports"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/utils"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/client/clientset/versioned"
	"github.com/kgateway-dev/kgateway/v2/pkg/logging"
	pluginsdkutils "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/utils"
)

var (
	logger = logging.New("xbackendtrafficpolicy")
)

const (
	StatefulSessionFilterName = "envoy.filters.http.stateful_session"
)

type XBackendTrafficPolicyIR struct {
	ct                 time.Time
	sessionPersistence *stateful_sessionv3.StatefulSessionPerRoute
	retryConstraint    *anypb.Any //TODO(jmcguire) implement handling of retry constraints
}

var _ ir.PolicyIR = &XBackendTrafficPolicyIR{}

func (d *XBackendTrafficPolicyIR) CreationTime() time.Time {
	return d.ct
}

func (d *XBackendTrafficPolicyIR) Equals(other any) bool {
	d2, ok := other.(*XBackendTrafficPolicyIR)
	if !ok {
		return false
	}

	if !d.ct.Equal(d2.ct) {
		return false
	}

	if (d.sessionPersistence == nil) != (d2.sessionPersistence == nil) {
		return false
	}

	if d.sessionPersistence != nil && d2.sessionPersistence != nil {
		if !proto.Equal(d.sessionPersistence, d2.sessionPersistence) {
			return false
		}
	}

	if (d.retryConstraint == nil) != (d2.retryConstraint == nil) {
		return false
	}

	if d.retryConstraint != nil && d2.retryConstraint != nil {
		if !proto.Equal(d.retryConstraint, d2.retryConstraint) {
			return false
		}
	}
	return true
}

func registerTypes(ourCli versioned.Interface) {
	skubeclient.Register[*gwxv1a1.XBackendTrafficPolicy](
		wellknown.XBackendTrafficPolicyGVR,
		wellknown.XBackendTrafficPolicyGVK,
		func(c skubeclient.ClientGetter, namespace string, o metav1.ListOptions) (runtime.Object, error) {
			return c.GatewayAPI().ExperimentalV1alpha1().XBackendTrafficPolicies(namespace).List(context.Background(), o)
		},
		func(c skubeclient.ClientGetter, namespace string, o metav1.ListOptions) (watch.Interface, error) {
			return c.GatewayAPI().ExperimentalV1alpha1().XBackendTrafficPolicies(namespace).Watch(context.Background(), o)
		},
	)
}

func NewPlugin(ctx context.Context, commoncol *common.CommonCollections) extensionsplug.Plugin {
	registerTypes(commoncol.OurClient)

	col := krt.WrapClient(kclient.NewFiltered[*gwxv1a1.XBackendTrafficPolicy](
		commoncol.Client,
		kclient.Filter{ObjectFilter: commoncol.Client.ObjectFilter()},
	), commoncol.KrtOpts.ToOptions("XBackendTrafficPolicy")...)

	xBackendTrafficPolicyCol := krt.NewCollection(col, func(krtctx krt.HandlerContext, policyCR *gwxv1a1.XBackendTrafficPolicy) *ir.PolicyWrapper {
		objSrc := ir.ObjectSource{
			Group:     wellknown.XBackendTrafficPolicyGVK.Group,
			Kind:      wellknown.XBackendTrafficPolicyGVK.Kind,
			Namespace: policyCR.Namespace,
			Name:      policyCR.Name,
		}

		policyIR, err := translate(commoncol, krtctx, policyCR)
		errs := []error{}
		if err != nil {
			errs = append(errs, err)
		}

		logger.Info("policyIR after translate", "policyIR", policyIR.sessionPersistence, "policyIR.retryConstraint", policyIR.retryConstraint, "policyIR.ct", policyIR.ct)

		var routeTargetRefs []v1alpha1.LocalPolicyTargetReference
		for _, ref := range policyCR.Spec.TargetRefs {
			routeRefs := findRoutesUsingBackend(krtctx, commoncol, ref)
			routeTargetRefs = append(routeTargetRefs, routeRefs...)
		}

		// transform exp target refs to v1alpha1.LocalPolicyTargetReference so we can use pluginsdkutils
		var internalTargetRefs []v1alpha1.LocalPolicyTargetReference
		for _, ref := range policyCR.Spec.TargetRefs {
			internalTargetRefs = append(internalTargetRefs, v1alpha1.LocalPolicyTargetReference{
				Group: ref.Group,
				Kind:  ref.Kind,
				Name:  ref.Name,
			})
		}
		targetRefs := pluginsdkutils.TargetRefsToPolicyRefs(internalTargetRefs, nil)

		return &ir.PolicyWrapper{
			ObjectSource: objSrc,
			Policy:       policyCR,
			PolicyIR:     policyIR,
			TargetRefs:   targetRefs,
			Errors:       errs,
		}
	}, commoncol.KrtOpts.ToOptions("XBackendTrafficPolicyIRs")...)

	return extensionsplug.Plugin{
		ContributesPolicies: map[schema.GroupKind]extensionsplug.PolicyPlugin{
			wellknown.XBackendTrafficPolicyGVK.GroupKind(): {
				Policies:                  xBackendTrafficPolicyCol,
				ProcessBackend:            processBackend,
				NewGatewayTranslationPass: newGatewayTranslationPass,
				GetPolicyStatus:           getPolicyStatusFn(commoncol.CrudClient),
				PatchPolicyStatus:         patchPolicyStatusFn(commoncol.CrudClient),
			},
		},
	}
}

func translate(commoncol *common.CommonCollections, krtctx krt.HandlerContext, pol *gwxv1a1.XBackendTrafficPolicy) (*XBackendTrafficPolicyIR, error) {
	logger.Info("translate called",
		"name", pol.Name,
		"namespace", pol.Namespace,
		"hasSessionPersistence", pol.Spec.SessionPersistence != nil)

	ir := &XBackendTrafficPolicyIR{
		ct: pol.CreationTimestamp.Time,
	}

	if pol.Spec.SessionPersistence != nil {
		logger.Info("translating session persistence",
			"sessionName", pol.Spec.SessionPersistence.SessionName,
			"type", pol.Spec.SessionPersistence.Type)

		sessionPersistenceConfig, err := translateSessionPersistence(pol.Spec.SessionPersistence)
		if err != nil {
			logger.Error("failed to translate session persistence", "error", err)
			return nil, err
		}
		ir.sessionPersistence = sessionPersistenceConfig

		logger.Info("session persistence translated successfully",
			"hasConfig", sessionPersistenceConfig != nil)
	}

	// TODO: Handle RetryConstraint translation when we implement it

	logger.Info("translate completed",
		"hasSessionPersistence", ir.sessionPersistence != nil)

	return ir, nil
}

func translateSessionPersistence(sessionPersistence *gwv1.SessionPersistence) (*stateful_sessionv3.StatefulSessionPerRoute, error) {
	if sessionPersistence == nil {
		return nil, nil
	}

	var sessionState proto.Message
	spType := gwv1.CookieBasedSessionPersistence
	if sessionPersistence.Type != nil {
		spType = *sessionPersistence.Type
	}

	switch spType {
	case gwv1.CookieBasedSessionPersistence:
		var ttl *durationpb.Duration
		if sessionPersistence.AbsoluteTimeout != nil {
			if parsed, err := time.ParseDuration(string(*sessionPersistence.AbsoluteTimeout)); err == nil {
				ttl = durationpb.New(parsed)
			}
		}
		cookie := &httpv3.Cookie{
			Name: utils.SanitizeCookieName(ptr.Deref(sessionPersistence.SessionName, "sessionPersistence")),
			Ttl:  ttl,
		}
		if sessionPersistence.CookieConfig != nil &&
			sessionPersistence.CookieConfig.LifetimeType != nil {
			switch *sessionPersistence.CookieConfig.LifetimeType {
			case gwv1.SessionCookieLifetimeType:
				cookie.Ttl = nil
			case gwv1.PermanentCookieLifetimeType:
				if cookie.GetTtl() == nil {
					cookie.Ttl = durationpb.New(time.Hour * 24 * 365)
				}
			}
		}
		sessionState = &stateful_cookie.CookieBasedSessionState{
			Cookie: cookie,
		}
	case gwv1.HeaderBasedSessionPersistence:
		sessionState = &stateful_header.HeaderBasedSessionState{
			Name: utils.SanitizeHeaderName(ptr.Deref(sessionPersistence.SessionName, "x-session-persistence")),
		}
	}

	sessionStateAny, err := utils.MessageToAny(sessionState)
	if err != nil {
		return nil, err
	}

	statefulSession := &stateful_sessionv3.StatefulSession{
		SessionState: &corev3.TypedExtensionConfig{
			Name:        "envoy.http.stateful_session." + strings.ToLower(string(spType)),
			TypedConfig: sessionStateAny,
		},
	}

	return &stateful_sessionv3.StatefulSessionPerRoute{
		Override: &stateful_sessionv3.StatefulSessionPerRoute_StatefulSession{
			StatefulSession: statefulSession,
		},
	}, nil
}

type xBackendTrafficPolicyGwPass struct {
	ir.UnimplementedProxyTranslationPass
	needsStatefulSessionFilter map[string]bool
}

var _ ir.ProxyTranslationPass = &xBackendTrafficPolicyGwPass{}

func newGatewayTranslationPass(ctx context.Context, tctx ir.GwTranslationCtx, reporter reports.Reporter) ir.ProxyTranslationPass {
	return &xBackendTrafficPolicyGwPass{
		needsStatefulSessionFilter: make(map[string]bool),
	}
}

func (p *xBackendTrafficPolicyGwPass) ApplyForRouteBackend(
	ctx context.Context,
	policy ir.PolicyIR,
	pCtx *ir.RouteBackendContext,
) error {
	pol, ok := policy.(*XBackendTrafficPolicyIR)
	if !ok {
		logger.Error("ApplyForRouteBackend: unexpected policy type", "type", fmt.Sprintf("%T", policy))
		return nil
	}

	logger.Info("ApplyForRouteBackend: processing XBackendTrafficPolicy",
		"backend", pCtx.Backend.GetName(),
		"hasSessionPersistence", pol.sessionPersistence != nil)

	if pol.sessionPersistence != nil {
		logger.Info("ApplyForRouteBackend: processing session persistence",
			"backend", pCtx.Backend.GetName(),
			"filter_chain", pCtx.FilterChainName)

		sessionPersistenceAny, err := utils.MessageToAny(pol.sessionPersistence)
		if err != nil {
			logger.Error("failed to convert session persistence to any",
				"error", err,
				"backend", pCtx.Backend.GetName())
			return err
		}

		pCtx.TypedFilterConfig.AddTypedConfig(StatefulSessionFilterName, sessionPersistenceAny)

		p.needsStatefulSessionFilter[pCtx.FilterChainName] = true

		logger.Info("ApplyForRouteBackend: added session persistence config",
			"backend", pCtx.Backend.GetName(),
			"filter_chain", pCtx.FilterChainName)
	} else {
		logger.Info("ApplyForRouteBackend: no session persistence in policy",
			"backend", pCtx.Backend.GetName())
	}

	return nil
}

func (p *xBackendTrafficPolicyGwPass) HttpFilters(ctx context.Context, fcc ir.FilterChainCommon) ([]plugins.StagedHttpFilter, error) {
	result := []plugins.StagedHttpFilter{}

	var errs []error
	if p.needsStatefulSessionFilter[fcc.FilterChainName] {
		filter := plugins.MustNewStagedFilter(
			StatefulSessionFilterName,
			&stateful_sessionv3.StatefulSessionPerRoute{},
			plugins.DuringStage(plugins.RouteStage),
		)

		result = append(result, filter)
	}
	return result, errors.Join(errs...)
}

// processBackend is a no-op function that marks this plugin as backend-attachable
// The actual processing happens in ApplyForRouteBackend
func processBackend(ctx context.Context, policy ir.PolicyIR, backend ir.BackendObjectIR, out *envoy_config_cluster_v3.Cluster) {
	pol, ok := policy.(*XBackendTrafficPolicyIR)
	if !ok {
		return
	}

	logger.Info("processBackend: processing XBackendTrafficPolicy",
		"backend", backend.GetName(),
		"hasSessionPersistence", pol.sessionPersistence != nil)

	// Apply session persistence via consistent hash load balancing
	if pol.sessionPersistence != nil {
		if err := applySessionPersistenceAsConsistentHash(pol.sessionPersistence, out); err != nil {
			logger.Error("failed to apply session persistence as consistent hash",
				"error", err,
				"backend", backend.GetName())
		}
	}
}

func applySessionPersistenceAsConsistentHash(sessionPersistence *stateful_sessionv3.StatefulSessionPerRoute, cluster *envoy_config_cluster_v3.Cluster) error {
	if sessionPersistence == nil {
		return nil
	}

	// Extract the session state from the stateful session config
	statefulSession := sessionPersistence.GetStatefulSession()
	if statefulSession == nil {
		return fmt.Errorf("no stateful session found in session persistence config")
	}

	sessionState := statefulSession.GetSessionState()
	if sessionState == nil {
		return fmt.Errorf("no session state found in stateful session config")
	}

	// Convert to consistent hash based on session state type
	var consistentHashLB *envoy_config_cluster_v3.Cluster_LbPolicy_ConsistentHashLB

	switch sessionState.GetName() {
	case "envoy.http.stateful_session.cookie":
		// Parse the cookie config
		cookieConfig := &stateful_cookie.CookieBasedSessionState{}
		if err := sessionState.GetTypedConfig().UnmarshalTo(cookieConfig); err != nil {
			return fmt.Errorf("failed to unmarshal cookie config: %w", err)
		}

		cookieName := cookieConfig.GetCookie().GetName()
		if cookieName == "" {
			return fmt.Errorf("cookie name is required for session persistence")
		}

		// Create consistent hash config using HTTP cookie
		consistentHashLB = &envoy_config_cluster_v3.ConsistentHashLbConfig{
			HashKey: &envoy_config_cluster_v3.Cluster_LbPolicy_ConsistentHashLB_HttpCookie{
				HttpCookie: &envoy_config_cluster_v3.Cluster_LbPolicy_ConsistentHashLB_Cookie{
					Name: cookieName,
					Ttl:  cookieConfig.GetCookie().GetTtl(), // Use TTL from original config
				},
			},
		}

		logger.Info("applying session persistence via cookie-based consistent hash",
			"cookieName", cookieName,
			"ttl", cookieConfig.GetCookie().GetTtl())

	case "envoy.http.stateful_session.header":
		// Parse the header config
		headerConfig := &stateful_header.HeaderBasedSessionState{}
		if err := sessionState.GetTypedConfig().UnmarshalTo(headerConfig); err != nil {
			return fmt.Errorf("failed to unmarshal header config: %w", err)
		}

		headerName := headerConfig.GetName()
		if headerName == "" {
			return fmt.Errorf("header name is required for session persistence")
		}

		// Create consistent hash config using HTTP header
		consistentHashLB = &envoy_config_cluster_v3.Cluster_LbPolicy_ConsistentHashLB{
			HashKey: &envoy_config_cluster_v3.Cluster_LbPolicy_ConsistentHashLB_HttpHeaderName{
				HttpHeaderName: headerName,
			},
		}

		logger.Info("applying session persistence via header-based consistent hash",
			"headerName", headerName)

	default:
		return fmt.Errorf("unsupported session state type: %s", sessionState.GetName())
	}

	// Apply the consistent hash load balancer to the cluster
	cluster.LbPolicy = envoy_config_cluster_v3.Cluster_RING_HASH
	cluster.LbConfig = &envoy_config_cluster_v3.Cluster_LbConfig{
		LbConfig: &envoy_config_cluster_v3.Cluster_LbConfig_RingHashLbConfig_{
			RingHashLbConfig: &envoy_config_cluster_v3.Cluster_LbConfig_RingHashLbConfig{
				HashFunction:    envoy_config_cluster_v3.Cluster_LbConfig_RingHashLbConfig_XX_HASH,
				MinimumRingSize: &wrapperspb.UInt64Value{Value: 1024},
				MaximumRingSize: &wrapperspb.UInt64Value{Value: 8388608},
			},
		},
	}

	// Set the consistent hash configuration
	if consistentHashLB != nil {
		cluster.LbConfig.GetRingHashLbConfig().HashKey = consistentHashLB.HashKey
	}

	logger.Info("successfully applied session persistence via consistent hash load balancing")
	return nil
}

func findRoutesUsingBackend(krtctx krt.HandlerContext, commoncol *common.CommonCollections, backendRef gwxv1a1.LocalPolicyTargetReference) []v1alpha1.LocalPolicyTargetReference {
	var routeRefs []v1alpha1.LocalPolicyTargetReference

	// Get all routes from the routes index
	allRoutes := commoncol.ListAllRoutes() // You'd need this method

	for _, route := range allRoutes {
		if routeUsesBackend(route, backendRef) {
			routeRefs = append(routeRefs, v1alpha1.LocalPolicyTargetReference{
				Group: "gateway.networking.k8s.io",
				Kind:  "HTTPRoute",
				Name:  route.Name,
			})
		}
	}

	return routeRefs
}

func routeUsesBackend(route ir.Route, backendRef gwxv1a1.LocalPolicyTargetReference) bool {
	// Check if this route references the backend in any of its rules
	// This would need to walk through route.Rules[].Backends and check refs
	return true
}
