package krtcollections

import (
	"net/http"
	"time"

	"github.com/agentgateway/agentgateway/go/api"
	envoycorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
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

	if pol.filter != nil {
		switch v := pol.filter.policy.(type) {
		case *corsIr:
			applyCorsToAgwRoute(route, v)
		case *headerModifierIr:
			applyHeaderModifierToAgwRoute(route, v)
		case *requestRedirectIr:
			applyRequestRedirectToAgwRoute(route, v)
		case *urlRewriteIr:
			applyUrlRewriteToAgwRoute(route, v)
		}
	}
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

// mapRedirectEnumToHTTP converts Envoy redirect response code enum to numeric HTTP status
func mapRedirectEnumToHTTP(code envoyroutev3.RedirectAction_RedirectResponseCode) uint32 {
	switch code {
	case envoyroutev3.RedirectAction_MOVED_PERMANENTLY:
		return http.StatusMovedPermanently
	case envoyroutev3.RedirectAction_FOUND:
		return http.StatusFound
	case envoyroutev3.RedirectAction_SEE_OTHER:
		return http.StatusSeeOther
	case envoyroutev3.RedirectAction_TEMPORARY_REDIRECT:
		return http.StatusTemporaryRedirect
	case envoyroutev3.RedirectAction_PERMANENT_REDIRECT:
		return http.StatusPermanentRedirect
	default:
		return http.StatusFound
	}
}
func applyCorsToAgwRoute(route *api.Route, c *corsIr) {
	if c == nil || c.Cfg == nil {
		return
	}
	cors := &api.CORS{
		AllowCredentials: bool(c.Cfg.AllowCredentials),
	}
	for _, h := range c.Cfg.AllowHeaders {
		cors.AllowHeaders = append(cors.AllowHeaders, string(h))
	}
	for _, m := range c.Cfg.AllowMethods {
		cors.AllowMethods = append(cors.AllowMethods, string(m))
	}
	for _, o := range c.Cfg.AllowOrigins {
		cors.AllowOrigins = append(cors.AllowOrigins, string(o))
	}
	for _, h := range c.Cfg.ExposeHeaders {
		cors.ExposeHeaders = append(cors.ExposeHeaders, string(h))
	}
	if c.Cfg.MaxAge > 0 {
		cors.MaxAge = durationpb.New(time.Duration(c.Cfg.MaxAge) * time.Second)
	}
	route.Filters = append(route.Filters, &api.RouteFilter{Kind: &api.RouteFilter_Cors{Cors: cors}})
}

func applyHeaderModifierToAgwRoute(route *api.Route, hm *headerModifierIr) {
	if hm == nil {
		return
	}
	toHeaders := func(add []*envoycorev3.HeaderValueOption) []*api.Header {
		var out []*api.Header
		for _, h := range add {
			if h == nil || h.Header == nil {
				continue
			}
			out = append(out, &api.Header{Name: h.Header.Key, Value: h.Header.Value})
		}
		return out
	}
	mod := &api.HeaderModifier{Set: toHeaders(hm.Add), Remove: hm.Remove}
	if hm.IsRequest {
		route.Filters = append(route.Filters, &api.RouteFilter{Kind: &api.RouteFilter_RequestHeaderModifier{RequestHeaderModifier: mod}})
	} else {
		route.Filters = append(route.Filters, &api.RouteFilter{Kind: &api.RouteFilter_ResponseHeaderModifier{ResponseHeaderModifier: mod}})
	}
}

func applyRequestRedirectToAgwRoute(route *api.Route, rr *requestRedirectIr) {
	if rr == nil || rr.Redir == nil {
		return
	}
	redir := rr.Redir
	scheme := ""
	if redir.GetHttpsRedirect() {
		scheme = "https"
	} else if redir.GetSchemeRedirect() != "" {
		scheme = redir.GetSchemeRedirect()
	}
	rf := &api.RequestRedirect{
		Scheme: scheme,
		Host:   redir.GetHostRedirect(),
		Port:   redir.GetPortRedirect(),
		Status: mapRedirectEnumToHTTP(redir.GetResponseCode()),
	}
	switch x := redir.GetPathRewriteSpecifier().(type) {
	case *envoyroutev3.RedirectAction_PathRedirect:
		rf.Path = &api.RequestRedirect_Full{Full: x.PathRedirect}
	case *envoyroutev3.RedirectAction_PrefixRewrite:
		rf.Path = &api.RequestRedirect_Prefix{Prefix: x.PrefixRewrite}
	}
	route.Filters = append(route.Filters, &api.RouteFilter{Kind: &api.RouteFilter_RequestRedirect{RequestRedirect: rf}})
}

func applyUrlRewriteToAgwRoute(route *api.Route, ur *urlRewriteIr) {
	if ur == nil {
		return
	}
	uf := &api.UrlRewrite{Host: ""}
	if ur.HostRewrite != nil {
		uf.Host = ur.HostRewrite.HostRewriteLiteral
	}
	if ur.FullReplace != "" {
		uf.Path = &api.UrlRewrite_Full{Full: ur.FullReplace}
	}
	if ur.PrefixReplace != "" {
		uf.Path = &api.UrlRewrite_Prefix{Prefix: ur.PrefixReplace}
	}
	route.Filters = append(route.Filters, &api.RouteFilter{Kind: &api.RouteFilter_UrlRewrite{UrlRewrite: uf}})
}
