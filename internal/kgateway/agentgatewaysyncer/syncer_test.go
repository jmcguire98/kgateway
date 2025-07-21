package agentgatewaysyncer

import (
	"testing"

	"github.com/agentgateway/agentgateway/go/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/reports"
)

func TestBuildADPFilters(t *testing.T) {
	testCases := []struct {
		name            string
		inputFilters    []gwv1.HTTPRouteFilter
		expectedFilters []*api.RouteFilter
		expectedError   bool
	}{
		{
			name: "Request header modifier filter",
			inputFilters: []gwv1.HTTPRouteFilter{
				{
					Type: gwv1.HTTPRouteFilterRequestHeaderModifier,
					RequestHeaderModifier: &gwv1.HTTPHeaderFilter{
						Set: []gwv1.HTTPHeader{
							{Name: "X-Custom-Header", Value: "custom-value"},
						},
						Add: []gwv1.HTTPHeader{
							{Name: "X-Added-Header", Value: "added-value"},
						},
						Remove: []string{"X-Remove-Header"},
					},
				},
			},
			expectedFilters: []*api.RouteFilter{
				{
					Kind: &api.RouteFilter_RequestHeaderModifier{
						RequestHeaderModifier: &api.HeaderModifier{
							Set: []*api.Header{
								{Name: "X-Custom-Header", Value: "custom-value"},
							},
							Add: []*api.Header{
								{Name: "X-Added-Header", Value: "added-value"},
							},
							Remove: []string{"X-Remove-Header"},
						},
					},
				},
			},
			expectedError: false,
		},
		{
			name: "Response header modifier filter",
			inputFilters: []gwv1.HTTPRouteFilter{
				{
					Type: gwv1.HTTPRouteFilterResponseHeaderModifier,
					ResponseHeaderModifier: &gwv1.HTTPHeaderFilter{
						Set: []gwv1.HTTPHeader{
							{Name: "X-Response-Header", Value: "response-value"},
						},
					},
				},
			},
			expectedFilters: []*api.RouteFilter{
				{
					Kind: &api.RouteFilter_ResponseHeaderModifier{
						ResponseHeaderModifier: &api.HeaderModifier{
							Set: []*api.Header{
								{Name: "X-Response-Header", Value: "response-value"},
							},
						},
					},
				},
			},
			expectedError: false,
		},
		{
			name: "Request redirect filter",
			inputFilters: []gwv1.HTTPRouteFilter{
				{
					Type: gwv1.HTTPRouteFilterRequestRedirect,
					RequestRedirect: &gwv1.HTTPRequestRedirectFilter{
						Scheme:     ptr.To("https"),
						Hostname:   ptr.To(gwv1.PreciseHostname("secure.example.com")),
						StatusCode: ptr.To(301),
					},
				},
			},
			expectedFilters: []*api.RouteFilter{
				{
					Kind: &api.RouteFilter_RequestRedirect{
						RequestRedirect: &api.RequestRedirect{
							Scheme: "https",
							Host:   "secure.example.com",
							Status: 301,
						},
					},
				},
			},
			expectedError: false,
		},
		{
			name: "URL rewrite filter",
			inputFilters: []gwv1.HTTPRouteFilter{
				{
					Type: gwv1.HTTPRouteFilterURLRewrite,
					URLRewrite: &gwv1.HTTPURLRewriteFilter{
						Path: &gwv1.HTTPPathModifier{
							Type:               gwv1.PrefixMatchHTTPPathModifier,
							ReplacePrefixMatch: ptr.To("/new-prefix"),
						},
					},
				},
			},
			expectedFilters: []*api.RouteFilter{
				{
					Kind: &api.RouteFilter_UrlRewrite{
						UrlRewrite: &api.UrlRewrite{
							Path: &api.UrlRewrite_Prefix{
								Prefix: "/new-prefix",
							},
						},
					},
				},
			},
			expectedError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := RouteContext{
				RouteContextInputs: RouteContextInputs{
					Grants:       ReferenceGrants{},
					RouteParents: RouteParents{},
				},
			}

			result, err := buildADPFilters(ctx, "default", tc.inputFilters)

			if tc.expectedError {
				assert.NotNil(t, err)
				return
			}

			assert.Nil(t, err)
			require.Equal(t, len(tc.expectedFilters), len(result))

			for i, expectedFilter := range tc.expectedFilters {
				actualFilter := result[i]

				// Compare filter types
				switch expectedFilter.Kind.(type) {
				case *api.RouteFilter_RequestHeaderModifier:
					assert.IsType(t, &api.RouteFilter_RequestHeaderModifier{}, actualFilter.Kind)
				case *api.RouteFilter_ResponseHeaderModifier:
					assert.IsType(t, &api.RouteFilter_ResponseHeaderModifier{}, actualFilter.Kind)
				case *api.RouteFilter_RequestRedirect:
					assert.IsType(t, &api.RouteFilter_RequestRedirect{}, actualFilter.Kind)
				case *api.RouteFilter_UrlRewrite:
					assert.IsType(t, &api.RouteFilter_UrlRewrite{}, actualFilter.Kind)
				}
			}
		})
	}
}

func TestGetProtocolAndTLSConfig(t *testing.T) {
	testCases := []struct {
		name          string
		gateway       Gateway
		expectedProto api.Protocol
		expectedTLS   *api.TLSConfig
		expectedOk    bool
	}{
		{
			name: "HTTP protocol",
			gateway: Gateway{
				parentInfo: parentInfo{
					Protocol: gwv1.HTTPProtocolType,
				},
				TLSInfo: nil,
			},
			expectedProto: api.Protocol_HTTP,
			expectedTLS:   nil,
			expectedOk:    true,
		},
		{
			name: "HTTPS protocol with TLS",
			gateway: Gateway{
				parentInfo: parentInfo{
					Protocol: gwv1.HTTPSProtocolType,
				},
				TLSInfo: &TLSInfo{
					Cert: []byte("cert-data"),
					Key:  []byte("key-data"),
				},
			},
			expectedProto: api.Protocol_HTTPS,
			expectedTLS: &api.TLSConfig{
				Cert:       []byte("cert-data"),
				PrivateKey: []byte("key-data"),
			},
			expectedOk: true,
		},
		{
			name: "HTTPS protocol without TLS (should fail)",
			gateway: Gateway{
				parentInfo: parentInfo{
					Protocol: gwv1.HTTPSProtocolType,
				},
				TLSInfo: nil,
			},
			expectedProto: api.Protocol_HTTPS,
			expectedTLS:   nil,
			expectedOk:    false,
		},
		{
			name: "TCP protocol",
			gateway: Gateway{
				parentInfo: parentInfo{
					Protocol: gwv1.TCPProtocolType,
				},
				TLSInfo: nil,
			},
			expectedProto: api.Protocol_TCP,
			expectedTLS:   nil,
			expectedOk:    true,
		},
		{
			name: "TLS protocol with TLS",
			gateway: Gateway{
				parentInfo: parentInfo{
					Protocol: gwv1.TLSProtocolType,
				},
				TLSInfo: &TLSInfo{
					Cert: []byte("tls-cert"),
					Key:  []byte("tls-key"),
				},
			},
			expectedProto: api.Protocol_TLS,
			expectedTLS: &api.TLSConfig{
				Cert:       []byte("tls-cert"),
				PrivateKey: []byte("tls-key"),
			},
			expectedOk: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			syncer := &AgentGwSyncer{}

			proto, tlsConfig, ok := syncer.getProtocolAndTLSConfig(tc.gateway)

			assert.Equal(t, tc.expectedOk, ok)
			if tc.expectedOk {
				assert.Equal(t, tc.expectedProto, proto)
				if tc.expectedTLS != nil {
					require.NotNil(t, tlsConfig)
					assert.Equal(t, tc.expectedTLS.Cert, tlsConfig.Cert)
					assert.Equal(t, tc.expectedTLS.PrivateKey, tlsConfig.PrivateKey)
				} else {
					assert.Nil(t, tlsConfig)
				}
			}
		})
	}
}

func TestADPResourceCreation(t *testing.T) {
	testCases := []struct {
		name                 string
		expectedResource     *api.Resource
		expectedResourceName string
	}{
		{
			name: "Create Bind resource",
			expectedResource: &api.Resource{
				Kind: &api.Resource_Bind{
					Bind: &api.Bind{
						Key:  "8080/default/test-gateway",
						Port: 8080,
					},
				},
			},
			expectedResourceName: "bind/8080/default/test-gateway",
		},
		{
			name: "Create Listener resource",
			expectedResource: &api.Resource{
				Kind: &api.Resource_Listener{
					Listener: &api.Listener{
						Key:         "default/test-gateway",
						Name:        "http",
						BindKey:     "8080/default/test-gateway",
						GatewayName: "default/test-gateway",
						Protocol:    api.Protocol_HTTP,
						Hostname:    "example.com",
					},
				},
			},
			expectedResourceName: "listener/default/test-gateway",
		},
		{
			name: "Create Route resource",
			expectedResource: &api.Resource{
				Kind: &api.Resource_Route{
					Route: &api.Route{
						Key:         "default.test-route.0.0",
						RouteName:   "default/test-route",
						ListenerKey: "http",
						Hostnames:   []string{"example.com"},
						Matches: []*api.RouteMatch{
							{
								Path: &api.PathMatch{
									Kind: &api.PathMatch_PathPrefix{
										PathPrefix: "/api",
									},
								},
							},
						},
					},
				},
			},
			expectedResourceName: "route/default.test-route.0.0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gateway := types.NamespacedName{
				Name:      "test-gateway",
				Namespace: "default",
			}

			adpResource := ADPResource{
				Resource: tc.expectedResource,
				Gateway:  gateway,
			}

			assert.Equal(t, tc.expectedResourceName, adpResource.ResourceName())

			// Test that two identical resources are equal
			adpResource2 := ADPResource{
				Resource: tc.expectedResource,
				Gateway:  gateway,
			}
			assert.True(t, adpResource.Equals(adpResource2))
		})
	}
}

func TestMergeProxyReports(t *testing.T) {
	tests := []struct {
		name     string
		proxies  []agentGwXdsResources
		expected reports.ReportMap
	}{
		{
			name: "Merge HTTPRoute reports for different parents",
			proxies: []agentGwXdsResources{
				{
					reports: reports.ReportMap{
						HTTPRoutes: map[types.NamespacedName]*reports.RouteReport{
							{Name: "route1", Namespace: "default"}: {
								Parents: map[reports.ParentRefKey]*reports.ParentRefReport{
									{NamespacedName: types.NamespacedName{Name: "gw-1", Namespace: "default"}}: {
										Conditions: []metav1.Condition{
											{
												Type:   "Accepted",
												Status: metav1.ConditionTrue,
												Reason: "Accepted",
											},
										},
									},
								},
							},
						},
					},
				},
				{
					reports: reports.ReportMap{
						HTTPRoutes: map[types.NamespacedName]*reports.RouteReport{
							{Name: "route1", Namespace: "default"}: {
								Parents: map[reports.ParentRefKey]*reports.ParentRefReport{
									{NamespacedName: types.NamespacedName{Name: "gw-2", Namespace: "default"}}: {
										Conditions: []metav1.Condition{
											{
												Type:   "Accepted",
												Status: metav1.ConditionTrue,
												Reason: "Accepted",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: reports.ReportMap{
				HTTPRoutes: map[types.NamespacedName]*reports.RouteReport{
					{Name: "route1", Namespace: "default"}: {
						Parents: map[reports.ParentRefKey]*reports.ParentRefReport{
							{NamespacedName: types.NamespacedName{Name: "gw-1", Namespace: "default"}}: {
								Conditions: []metav1.Condition{
									{
										Type:   "Accepted",
										Status: metav1.ConditionTrue,
										Reason: "Accepted",
									},
								},
							},
							{NamespacedName: types.NamespacedName{Name: "gw-2", Namespace: "default"}}: {
								Conditions: []metav1.Condition{
									{
										Type:   "Accepted",
										Status: metav1.ConditionTrue,
										Reason: "Accepted",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "Merge TCPRoute reports for different parents",
			proxies: []agentGwXdsResources{
				{
					reports: reports.ReportMap{
						TCPRoutes: map[types.NamespacedName]*reports.RouteReport{
							{Name: "route1", Namespace: "default"}: {
								Parents: map[reports.ParentRefKey]*reports.ParentRefReport{
									{NamespacedName: types.NamespacedName{Name: "gw-1", Namespace: "default"}}: {
										Conditions: []metav1.Condition{
											{
												Type:   "Accepted",
												Status: metav1.ConditionTrue,
												Reason: "Accepted",
											},
										},
									},
								},
							},
						},
					},
				},
				{
					reports: reports.ReportMap{
						TCPRoutes: map[types.NamespacedName]*reports.RouteReport{
							{Name: "route1", Namespace: "default"}: {
								Parents: map[reports.ParentRefKey]*reports.ParentRefReport{
									{NamespacedName: types.NamespacedName{Name: "gw-2", Namespace: "default"}}: {
										Conditions: []metav1.Condition{
											{
												Type:   "Accepted",
												Status: metav1.ConditionTrue,
												Reason: "Accepted",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: reports.ReportMap{
				TCPRoutes: map[types.NamespacedName]*reports.RouteReport{
					{Name: "route1", Namespace: "default"}: {
						Parents: map[reports.ParentRefKey]*reports.ParentRefReport{
							{NamespacedName: types.NamespacedName{Name: "gw-1", Namespace: "default"}}: {
								Conditions: []metav1.Condition{
									{
										Type:   "Accepted",
										Status: metav1.ConditionTrue,
										Reason: "Accepted",
									},
								},
							},
							{NamespacedName: types.NamespacedName{Name: "gw-2", Namespace: "default"}}: {
								Conditions: []metav1.Condition{
									{
										Type:   "Accepted",
										Status: metav1.ConditionTrue,
										Reason: "Accepted",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "Merge Gateway reports from multiple proxies",
			proxies: []agentGwXdsResources{
				{
					reports: reports.ReportMap{
						Gateways: map[types.NamespacedName]*reports.GatewayReport{
							{Name: "gw1", Namespace: "default"}: {},
						},
					},
				},
				{
					reports: reports.ReportMap{
						Gateways: map[types.NamespacedName]*reports.GatewayReport{
							{Name: "gw2", Namespace: "default"}: {},
						},
					},
				},
			},
			expected: reports.ReportMap{
				Gateways: map[types.NamespacedName]*reports.GatewayReport{
					{Name: "gw1", Namespace: "default"}: {},
					{Name: "gw2", Namespace: "default"}: {},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := assert.New(t)

			actual := mergeProxyReports(tt.proxies)
			if tt.expected.HTTPRoutes != nil {
				a.Equal(tt.expected.HTTPRoutes, actual.HTTPRoutes)
			}
			if tt.expected.TCPRoutes != nil {
				a.Equal(tt.expected.TCPRoutes, actual.TCPRoutes)
			}
			if tt.expected.TLSRoutes != nil {
				a.Equal(tt.expected.TLSRoutes, actual.TLSRoutes)
			}
			if tt.expected.GRPCRoutes != nil {
				a.Equal(tt.expected.GRPCRoutes, actual.GRPCRoutes)
			}
			if tt.expected.Gateways != nil {
				a.Equal(tt.expected.Gateways, actual.Gateways)
			}
		})
	}
}

func TestADPResourceEquals(t *testing.T) {
	testCases := []struct {
		name      string
		resource1 ADPResource
		resource2 ADPResource
		expected  bool
	}{
		{
			name: "Equal bind resources",
			resource1: ADPResource{
				Resource: &api.Resource{
					Kind: &api.Resource_Bind{
						Bind: &api.Bind{
							Key:  "test-key",
							Port: 8080,
						},
					},
				},
				Gateway: types.NamespacedName{Name: "test", Namespace: "default"},
			},
			resource2: ADPResource{
				Resource: &api.Resource{
					Kind: &api.Resource_Bind{
						Bind: &api.Bind{
							Key:  "test-key",
							Port: 8080,
						},
					},
				},
				Gateway: types.NamespacedName{Name: "test", Namespace: "default"},
			},
			expected: true,
		},
		{
			name: "Different gateway",
			resource1: ADPResource{
				Resource: &api.Resource{
					Kind: &api.Resource_Bind{
						Bind: &api.Bind{
							Key:  "test-key",
							Port: 8080,
						},
					},
				},
				Gateway: types.NamespacedName{Name: "test", Namespace: "default"},
			},
			resource2: ADPResource{
				Resource: &api.Resource{
					Kind: &api.Resource_Bind{
						Bind: &api.Bind{
							Key:  "test-key",
							Port: 8080,
						},
					},
				},
				Gateway: types.NamespacedName{Name: "other", Namespace: "default"},
			},
			expected: false,
		},
		{
			name: "Different resource port",
			resource1: ADPResource{
				Resource: &api.Resource{
					Kind: &api.Resource_Bind{
						Bind: &api.Bind{
							Key:  "test-key",
							Port: 8080,
						},
					},
				},
				Gateway: types.NamespacedName{Name: "test", Namespace: "default"},
			},
			resource2: ADPResource{
				Resource: &api.Resource{
					Kind: &api.Resource_Bind{
						Bind: &api.Bind{
							Key:  "test-key",
							Port: 9090,
						},
					},
				},
				Gateway: types.NamespacedName{Name: "test", Namespace: "default"},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := proto.Equal(tc.resource1.Resource, tc.resource2.Resource) && tc.resource1.Gateway == tc.resource2.Gateway
			assert.Equal(t, tc.expected, result)
		})
	}
}
