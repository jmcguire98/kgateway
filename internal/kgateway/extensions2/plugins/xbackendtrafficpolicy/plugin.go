package xbackendtrafficpolicy

import (
	"context"
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

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/extensions2/common"
	extensionsplug "github.com/kgateway-dev/kgateway/v2/internal/kgateway/extensions2/plugin"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/ir"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/reports"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/utils"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/client/clientset/versioned"
	"github.com/kgateway-dev/kgateway/v2/pkg/logging"
	pluginsdkutils "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/utils"
)

var logger = logging.New("xbackendtrafficpolicy")

const (
	// SessionPersistenceMetadataKey is the key used to store session persistence configuration in cluster metadata
	SessionPersistenceMetadataKey = "session_persistence"
	// StatefulSessionFilterName is the well-known name for the Envoy stateful session HTTP filter
	StatefulSessionFilterName = "envoy.filters.http.stateful_session"
)

// XBackendTrafficPolicyIR represents the internal representation of XBackendTrafficPolicy
type XBackendTrafficPolicyIR struct {
	ct                 time.Time
	sessionPersistence *stateful_sessionv3.StatefulSession
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

	// Create a collection that watches for XBackendTrafficPolicy resources
	col := krt.WrapClient(kclient.NewFiltered[*gwxv1a1.XBackendTrafficPolicy](
		commoncol.Client,
		kclient.Filter{ObjectFilter: commoncol.Client.ObjectFilter()},
	), commoncol.KrtOpts.ToOptions("XBackendTrafficPolicy")...)

	xBackendTrafficPolicyCol := krt.NewCollection(col, func(krtctx krt.HandlerContext, b *gwxv1a1.XBackendTrafficPolicy) *ir.PolicyWrapper {
		objSrc := ir.ObjectSource{
			Group:     wellknown.XBackendTrafficPolicyGVK.Group,
			Kind:      wellknown.XBackendTrafficPolicyGVK.Kind,
			Namespace: b.Namespace,
			Name:      b.Name,
		}

		policyIR, err := translate(commoncol, krtctx, b)
		errs := []error{}
		if err != nil {
			errs = append(errs, err)
		}

		var internalTargetRefs []v1alpha1.LocalPolicyTargetReference
		for _, ref := range b.Spec.TargetRefs {
			internalTargetRefs = append(internalTargetRefs, v1alpha1.LocalPolicyTargetReference{
				Group: ref.Group,
				Kind:  ref.Kind,
				Name:  ref.Name,
			})
		}
		targetRefs := pluginsdkutils.TargetRefsToPolicyRefs(internalTargetRefs, nil)

		return &ir.PolicyWrapper{
			ObjectSource: objSrc,
			Policy:       b,
			PolicyIR:     policyIR,
			TargetRefs:   targetRefs,
			Errors:       errs,
		}
	}, commoncol.KrtOpts.ToOptions("XBackendTrafficPolicyIRs")...)

	return extensionsplug.Plugin{
		ContributesPolicies: map[schema.GroupKind]extensionsplug.PolicyPlugin{
			wellknown.XBackendTrafficPolicyGVK.GroupKind(): {
				Name:                      "XBackendTrafficPolicy",
				Policies:                  xBackendTrafficPolicyCol,
				NewGatewayTranslationPass: newGatewayTranslationPass,
			},
		},
	}
}

func translate(commoncol *common.CommonCollections, krtctx krt.HandlerContext, pol *gwxv1a1.XBackendTrafficPolicy) (*XBackendTrafficPolicyIR, error) {
	ir := &XBackendTrafficPolicyIR{
		ct: pol.CreationTimestamp.Time,
	}

	if pol.Spec.SessionPersistence != nil {
		sessionPersistenceConfig, err := translateSessionPersistence(pol.Spec.SessionPersistence)
		if err != nil {
			return nil, err
		}
		ir.sessionPersistence = sessionPersistenceConfig
	}

	// TODO: Handle RetryConstraint translation when we implement it

	return ir, nil
}

func translateSessionPersistence(sessionPersistence *gwv1.SessionPersistence) (*stateful_sessionv3.StatefulSession, error) {
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
		// Only set LifetimeType if present in CookieConfig
		if sessionPersistence.CookieConfig != nil &&
			sessionPersistence.CookieConfig.LifetimeType != nil {
			switch *sessionPersistence.CookieConfig.LifetimeType {
			case gwv1.SessionCookieLifetimeType:
				// Session cookies — cookies without a Max-Age or Expires attribute – are deleted when the current session ends
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

	return statefulSession, nil
}

type xBackendTrafficPolicyGwPass struct {
	ir.UnimplementedProxyTranslationPass
}

func newGatewayTranslationPass(ctx context.Context, tctx ir.GwTranslationCtx, reporter reports.Reporter) ir.ProxyTranslationPass {
	return &xBackendTrafficPolicyGwPass{}
}

func (p *xBackendTrafficPolicyGwPass) ApplyForRouteBackend(
	ctx context.Context,
	policy ir.PolicyIR,
	pCtx *ir.RouteBackendContext,
) error {
	pol, ok := policy.(*XBackendTrafficPolicyIR)
	if !ok {
		return nil
	}

	if pol.sessionPersistence != nil {
		// Convert StatefulSession to Any for route configuration
		sessionPersistenceAny, err := utils.MessageToAny(pol.sessionPersistence)
		if err != nil {
			logger.Error("failed to convert session persistence to any",
				"error", err,
				"backend", pCtx.Backend.GetName())
			return err
		}

		// Apply session persistence at route level using TypedFilterConfig
		pCtx.TypedFilterConfig.AddTypedConfig(StatefulSessionFilterName, sessionPersistenceAny)

		logger.Info("XBackendTrafficPolicy session persistence applied to route",
			"backend", pCtx.Backend.GetName(),
			"filter", StatefulSessionFilterName)
	}

	return nil
}
