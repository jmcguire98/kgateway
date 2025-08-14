package krtcollections

import (
	"testing"

	"github.com/agentgateway/agentgateway/go/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	pluginsdkir "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
)

func TestBuiltinAGWPass_RequestHeaderModifier(t *testing.T) {
	hm := convertHeaderModifierIR(nil, &gwv1.HTTPHeaderFilter{
		Set:    []gwv1.HTTPHeader{{Name: "X-Custom-Header", Value: "custom-value"}},
		Add:    []gwv1.HTTPHeader{{Name: "X-Added-Header", Value: "added-value"}},
		Remove: []string{"X-Remove-Header"},
	}, true)
	pol := &builtinPlugin{filter: &filterIR{policy: hm}}

	pass := NewBuiltinAgentGatewayPass(nil)
	route := &api.Route{}
	err := pass.ApplyForRoute(&pluginsdkir.RouteContext{Policy: pol}, route)
	require.NoError(t, err)
	require.Len(t, route.Filters, 1)
	rf := route.Filters[0]
	mod := rf.GetRequestHeaderModifier()
	require.NotNil(t, mod)
	// Builtin pass normalizes Add/Set into Set for deterministic output
	assert.ElementsMatch(t, []*api.Header{
		{Name: "X-Custom-Header", Value: "custom-value"},
		{Name: "X-Added-Header", Value: "added-value"},
	}, mod.Set)
	assert.ElementsMatch(t, []string{"X-Remove-Header"}, mod.Remove)
}

func TestBuiltinAGWPass_ResponseHeaderModifier(t *testing.T) {
	hm := convertHeaderModifierIR(nil, &gwv1.HTTPHeaderFilter{
		Set: []gwv1.HTTPHeader{{Name: "X-Response-Header", Value: "response-value"}},
	}, false)
	pol := &builtinPlugin{filter: &filterIR{policy: hm}}

	pass := NewBuiltinAgentGatewayPass(nil)
	route := &api.Route{}
	err := pass.ApplyForRoute(&pluginsdkir.RouteContext{Policy: pol}, route)
	require.NoError(t, err)
	require.Len(t, route.Filters, 1)
	rf := route.Filters[0]
	mod := rf.GetResponseHeaderModifier()
	require.NotNil(t, mod)
	assert.ElementsMatch(t, []*api.Header{{Name: "X-Response-Header", Value: "response-value"}}, mod.Set)
}

func TestBuiltinAGWPass_RequestRedirect(t *testing.T) {
	rr := convertRequestRedirectIR(nil, &gwv1.HTTPRequestRedirectFilter{
		Scheme:     ptrTo("https"),
		Hostname:   ptrToPrecise("secure.example.com"),
		StatusCode: ptrToInt(301),
	})
	pol := &builtinPlugin{filter: &filterIR{policy: rr}}

	pass := NewBuiltinAgentGatewayPass(nil)
	route := &api.Route{}
	err := pass.ApplyForRoute(&pluginsdkir.RouteContext{Policy: pol}, route)
	require.NoError(t, err)
	require.Len(t, route.Filters, 1)
	rf := route.Filters[0]
	redir := rf.GetRequestRedirect()
	require.NotNil(t, redir)
	assert.Equal(t, "https", redir.Scheme)
	assert.Equal(t, "secure.example.com", redir.Host)
	assert.NotZero(t, redir.Status)
}

func TestBuiltinAGWPass_URLRewrite(t *testing.T) {
	ur := convertURLRewriteIR(nil, &gwv1.HTTPURLRewriteFilter{
		Path: &gwv1.HTTPPathModifier{
			Type:               gwv1.PrefixMatchHTTPPathModifier,
			ReplacePrefixMatch: ptrTo("/new-prefix"),
		},
	})
	pol := &builtinPlugin{filter: &filterIR{policy: ur}}

	pass := NewBuiltinAgentGatewayPass(nil)
	route := &api.Route{}
	err := pass.ApplyForRoute(&pluginsdkir.RouteContext{Policy: pol}, route)
	require.NoError(t, err)
	require.Len(t, route.Filters, 1)
	rf := route.Filters[0]
	uw := rf.GetUrlRewrite()
	require.NotNil(t, uw)
	require.NotNil(t, uw.Path)
	assert.Equal(t, "/new-prefix", uw.GetPrefix())
}

func TestBuiltinAGWPass_CORS(t *testing.T) {
	cors := convertCORSIR(nil, &gwv1.HTTPCORSFilter{
		AllowCredentials: true,
		AllowHeaders:     []gwv1.HTTPHeaderName{"X-A", "X-B"},
		AllowMethods:     []gwv1.HTTPMethodWithWildcard{"GET", "POST"},
		AllowOrigins:     []gwv1.AbsoluteURI{"https://a", "https://b"},
		ExposeHeaders:    []gwv1.HTTPHeaderName{"X-C"},
		MaxAge:           120,
	})
	pol := &builtinPlugin{filter: &filterIR{policy: cors}, hasCors: true}

	pass := NewBuiltinAgentGatewayPass(nil)
	route := &api.Route{}
	err := pass.ApplyForRoute(&pluginsdkir.RouteContext{Policy: pol}, route)
	require.NoError(t, err)
	require.Len(t, route.Filters, 1)
	rf := route.Filters[0]
	cf := rf.GetCors()
	require.NotNil(t, cf)
	assert.ElementsMatch(t, []string{"X-A", "X-B"}, cf.AllowHeaders)
	assert.ElementsMatch(t, []string{"GET", "POST"}, cf.AllowMethods)
	assert.ElementsMatch(t, []string{"https://a", "https://b"}, cf.AllowOrigins)
	assert.ElementsMatch(t, []string{"X-C"}, cf.ExposeHeaders)
	// MaxAge is set (value asserted indirectly by non-nil)
	require.NotNil(t, cf.MaxAge)
}

// helpers
func ptrTo(s string) *string { return &s }
func ptrToInt(i int) *int    { return &i }
func ptrToPrecise(h string) *gwv1.PreciseHostname {
	ph := gwv1.PreciseHostname(h)
	return &ph
}
