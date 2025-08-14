package agentgatewaysyncer

import (
	"fmt"
	"strings"

	"github.com/agentgateway/agentgateway/go/api"
	"github.com/golang/protobuf/ptypes/duration"
	"istio.io/istio/pkg/slices"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/reporter"
)

func createADPMethodMatch(match gwv1.HTTPRouteMatch) (*api.MethodMatch, *reporter.RouteCondition) {
	if match.Method == nil {
		return nil, nil
	}
	return &api.MethodMatch{
		Exact: string(*match.Method),
	}, nil
}

func createADPQueryMatch(match gwv1.HTTPRouteMatch) ([]*api.QueryMatch, *reporter.RouteCondition) {
	res := []*api.QueryMatch{}
	for _, header := range match.QueryParams {
		tp := gwv1.QueryParamMatchExact
		if header.Type != nil {
			tp = *header.Type
		}
		switch tp {
		case gwv1.QueryParamMatchExact:
			res = append(res, &api.QueryMatch{
				Name:  string(header.Name),
				Value: &api.QueryMatch_Exact{Exact: header.Value},
			})
		case gwv1.QueryParamMatchRegularExpression:
			res = append(res, &api.QueryMatch{
				Name:  string(header.Name),
				Value: &api.QueryMatch_Regex{Regex: header.Value},
			})
		default:
			// Should never happen, unless a new field is added
			return nil, &reporter.RouteCondition{
				Type:    gwv1.RouteConditionAccepted,
				Status:  metav1.ConditionFalse,
				Reason:  gwv1.RouteReasonUnsupportedValue,
				Message: fmt.Sprintf("unknown type: %q is not supported QueryMatch type", tp)}
		}
	}
	if len(res) == 0 {
		return nil, nil
	}
	return res, nil
}

func createADPPathMatch(match gwv1.HTTPRouteMatch) (*api.PathMatch, *reporter.RouteCondition) {
	tp := gwv1.PathMatchPathPrefix
	if match.Path == nil {
		return nil, nil
	}
	if match.Path.Type != nil {
		tp = *match.Path.Type
	}
	dest := "/"
	if match.Path.Value != nil {
		dest = *match.Path.Value
	}
	switch tp {
	case gwv1.PathMatchPathPrefix:
		// "When specified, a trailing `/` is ignored."
		if dest != "/" {
			dest = strings.TrimSuffix(dest, "/")
		}
		return &api.PathMatch{Kind: &api.PathMatch_PathPrefix{
			PathPrefix: dest,
		}}, nil
	case gwv1.PathMatchExact:
		return &api.PathMatch{Kind: &api.PathMatch_Exact{
			Exact: dest,
		}}, nil
	case gwv1.PathMatchRegularExpression:
		return &api.PathMatch{Kind: &api.PathMatch_Regex{
			Regex: dest,
		}}, nil
	default:
		// Should never happen, unless a new field is added
		return nil, &reporter.RouteCondition{
			Type:    gwv1.RouteConditionAccepted,
			Status:  metav1.ConditionFalse,
			Reason:  gwv1.RouteReasonUnsupportedValue,
			Message: fmt.Sprintf("unknown type: %q is not supported Path match type", tp)}
	}
}

func createADPHeadersMatch(match gwv1.HTTPRouteMatch) ([]*api.HeaderMatch, *reporter.RouteCondition) {
	var res []*api.HeaderMatch
	for _, header := range match.Headers {
		tp := gwv1.HeaderMatchExact
		if header.Type != nil {
			tp = *header.Type
		}
		switch tp {
		case gwv1.HeaderMatchExact:
			res = append(res, &api.HeaderMatch{
				Name:  string(header.Name),
				Value: &api.HeaderMatch_Exact{Exact: header.Value},
			})
		case gwv1.HeaderMatchRegularExpression:
			res = append(res, &api.HeaderMatch{
				Name:  string(header.Name),
				Value: &api.HeaderMatch_Regex{Regex: header.Value},
			})
		default:
			// Should never happen, unless a new field is added
			return nil, &reporter.RouteCondition{
				Type:    gwv1.RouteConditionAccepted,
				Status:  metav1.ConditionFalse,
				Reason:  gwv1.RouteReasonUnsupportedValue,
				Message: fmt.Sprintf("unknown type: %q is not supported HeaderMatch type", tp)}
		}
	}

	if len(res) == 0 {
		return nil, nil
	}
	return res, nil
}

func createADPHeadersFilter(filter *gwv1.HTTPHeaderFilter) *api.RouteFilter {
	if filter == nil {
		return nil
	}
	return &api.RouteFilter{
		Kind: &api.RouteFilter_RequestHeaderModifier{
			RequestHeaderModifier: &api.HeaderModifier{
				Add:    headerListToADP(filter.Add),
				Set:    headerListToADP(filter.Set),
				Remove: filter.Remove,
			},
		},
	}
}

func createADPResponseHeadersFilter(filter *gwv1.HTTPHeaderFilter) *api.RouteFilter {
	if filter == nil {
		return nil
	}
	return &api.RouteFilter{
		Kind: &api.RouteFilter_ResponseHeaderModifier{
			ResponseHeaderModifier: &api.HeaderModifier{
				Add:    headerListToADP(filter.Add),
				Set:    headerListToADP(filter.Set),
				Remove: filter.Remove,
			},
		},
	}
}

// terminalFilterCombinationError creates a standardized error message for when multiple terminal filters are used together
func terminalFilterCombinationError(existingFilter, newFilter string) string {
	return fmt.Sprintf("Cannot combine multiple terminal filters: %s and %s are mutually exclusive. Only one terminal filter is allowed per route rule.", existingFilter, newFilter)
}

// buildADPFilters converts HTTPRoute filters to ADP filters, returning an optional error condition
func buildADPFilters(
	ctx RouteContext,
	ns string,
	inputFilters []gwv1.HTTPRouteFilter,
) ([]*api.RouteFilter, *reporter.RouteCondition) {
	var filters []*api.RouteFilter

	var hasTerminalFilter bool
	var terminalFilterType string

	var filterError *reporter.RouteCondition

	for _, filter := range inputFilters {
		switch filter.Type {
		case gwv1.HTTPRouteFilterRequestHeaderModifier:
			h := createADPHeadersFilter(filter.RequestHeaderModifier)
			if h == nil {
				continue
			}
			filters = append(filters, h)
		case gwv1.HTTPRouteFilterResponseHeaderModifier:
			h := createADPResponseHeadersFilter(filter.ResponseHeaderModifier)
			if h == nil {
				continue
			}
			filters = append(filters, h)
		case gwv1.HTTPRouteFilterRequestRedirect:
			if hasTerminalFilter {
				filterError = &reporter.RouteCondition{
					Type:    gwv1.RouteConditionAccepted,
					Status:  metav1.ConditionFalse,
					Reason:  gwv1.RouteReasonIncompatibleFilters,
					Message: terminalFilterCombinationError(terminalFilterType, "RequestRedirect"),
				}
				continue
			}
			h := createADPRedirectFilter(filter.RequestRedirect)
			if h == nil {
				continue
			}
			filters = append(filters, h)
			hasTerminalFilter = true
			terminalFilterType = "RequestRedirect"
		case gwv1.HTTPRouteFilterRequestMirror:
			h, err := createADPMirrorFilter(ctx, filter.RequestMirror, ns, schema.GroupVersionKind{
				Group:   "gateway.networking.k8s.io",
				Version: "v1",
				Kind:    "HTTPRoute",
			})
			if err != nil {
				if filterError == nil {
					filterError = err
				}
			} else {
				filters = append(filters, h)
			}
		case gwv1.HTTPRouteFilterURLRewrite:
			h := createADPRewriteFilter(filter.URLRewrite)
			if h == nil {
				continue
			}
			filters = append(filters, h)
		case gwv1.HTTPRouteFilterCORS:
			h := createADPCorsFilter(filter.CORS)
			if h == nil {
				continue
			}
			filters = append(filters, h)
		case gwv1.HTTPRouteFilterExtensionRef:
			h, err := createADPExtensionRefFilter(ctx, filter.ExtensionRef, ns)
			if err != nil {
				if filterError == nil {
					filterError = err
				}
				continue
			} else if h != nil {
				if _, ok := h.Kind.(*api.RouteFilter_DirectResponse); ok {
					if hasTerminalFilter {
						filterError = &reporter.RouteCondition{
							Type:    gwv1.RouteConditionAccepted,
							Status:  metav1.ConditionFalse,
							Reason:  gwv1.RouteReasonIncompatibleFilters,
							Message: terminalFilterCombinationError(terminalFilterType, "DirectResponse"),
						}
						continue
					}
					hasTerminalFilter = true
					terminalFilterType = "DirectResponse"
				}
				filters = append(filters, h)
			}
		default:
			return nil, &reporter.RouteCondition{
				Type:    gwv1.RouteConditionAccepted,
				Status:  metav1.ConditionFalse,
				Reason:  gwv1.RouteReasonIncompatibleFilters,
				Message: fmt.Sprintf("unsupported filter type %q", filter.Type),
			}
		}
	}
	return filters, filterError
}

func createADPCorsFilter(cors *gwv1.HTTPCORSFilter) *api.RouteFilter {
	if cors == nil {
		return nil
	}
	return &api.RouteFilter{
		Kind: &api.RouteFilter_Cors{Cors: &api.CORS{
			AllowCredentials: bool(cors.AllowCredentials),
			AllowHeaders:     slices.Map(cors.AllowHeaders, func(h gwv1.HTTPHeaderName) string { return string(h) }),
			AllowMethods:     slices.Map(cors.AllowMethods, func(m gwv1.HTTPMethodWithWildcard) string { return string(m) }),
			AllowOrigins:     slices.Map(cors.AllowOrigins, func(o gwv1.AbsoluteURI) string { return string(o) }),
			ExposeHeaders:    slices.Map(cors.ExposeHeaders, func(h gwv1.HTTPHeaderName) string { return string(h) }),
			MaxAge: &duration.Duration{
				Seconds: int64(cors.MaxAge),
			},
		}},
	}
}

// createADPExtensionRefFilter creates ADP filter from Gateway API ExtensionRef filter
func createADPExtensionRefFilter(
	ctx RouteContext,
	extensionRef *gwv1.LocalObjectReference,
	ns string,
) (*api.RouteFilter, *reporter.RouteCondition) {
	if extensionRef == nil {
		return nil, nil
	}

	// Check if it's a DirectResponse reference
	if string(extensionRef.Group) == wellknown.DirectResponseGVK.Group && string(extensionRef.Kind) == wellknown.DirectResponseGVK.Kind {
		// Look up the DirectResponse resource
		directResponse := findDirectResponse(ctx, string(extensionRef.Name), ns)
		if directResponse == nil {
			return nil, &reporter.RouteCondition{
				Type:    gwv1.RouteConditionAccepted,
				Status:  metav1.ConditionFalse,
				Reason:  gwv1.RouteReasonBackendNotFound,
				Message: fmt.Sprintf("DirectResponse %s/%s not found", ns, extensionRef.Name),
			}
		}

		// Convert to ADP DirectResponse filter
		filter := &api.RouteFilter{
			Kind: &api.RouteFilter_DirectResponse{
				DirectResponse: &api.DirectResponse{
					Status: directResponse.Spec.StatusCode,
				},
			},
		}

		// Add body if specified
		if directResponse.Spec.Body != nil {
			filter.GetDirectResponse().Body = []byte(*directResponse.Spec.Body)
		}

		return filter, nil
	}

	// Unsupported ExtensionRef
	return nil, &reporter.RouteCondition{
		Type:    gwv1.RouteConditionAccepted,
		Status:  metav1.ConditionFalse,
		Reason:  gwv1.RouteReasonIncompatibleFilters,
		Message: fmt.Sprintf("unsupported ExtensionRef: %s/%s", extensionRef.Group, extensionRef.Kind),
	}
}

func createADPRewriteFilter(filter *gwv1.HTTPURLRewriteFilter) *api.RouteFilter {
	if filter == nil {
		return nil
	}

	var hostname string
	if filter.Hostname != nil {
		hostname = string(*filter.Hostname)
	}
	ff := &api.UrlRewrite{
		Host: hostname,
	}
	if filter.Path != nil {
		switch filter.Path.Type {
		case gwv1.PrefixMatchHTTPPathModifier:
			ff.Path = &api.UrlRewrite_Prefix{Prefix: strings.TrimSuffix(*filter.Path.ReplacePrefixMatch, "/")}
		case gwv1.FullPathHTTPPathModifier:
			ff.Path = &api.UrlRewrite_Full{Full: strings.TrimSuffix(*filter.Path.ReplaceFullPath, "/")}
		}
	}
	return &api.RouteFilter{
		Kind: &api.RouteFilter_UrlRewrite{
			UrlRewrite: ff,
		},
	}
}

func createADPMirrorFilter(
	ctx RouteContext,
	filter *gwv1.HTTPRequestMirrorFilter,
	ns string,
	k schema.GroupVersionKind,
) (*api.RouteFilter, *reporter.RouteCondition) {
	if filter == nil {
		return nil, nil
	}
	var weightOne int32 = 1
	dst, err := buildADPDestination(ctx, gwv1.HTTPBackendRef{
		BackendRef: gwv1.BackendRef{
			BackendObjectReference: filter.BackendRef,
			Weight:                 &weightOne,
		},
	}, ns, k, ctx.Backends)
	if err != nil {
		return nil, err
	}
	var percent float64
	if f := filter.Fraction; f != nil {
		denominator := float64(100)
		if f.Denominator != nil {
			denominator = float64(*f.Denominator)
		}
		percent = (100 * float64(f.Numerator)) / denominator
	} else if p := filter.Percent; p != nil {
		percent = float64(*p)
	} else {
		percent = 100
	}
	if percent == 0 {
		return nil, nil
	}
	rm := &api.RequestMirror{
		Percentage: percent,
		Backend:    dst.GetBackend(),
	}
	return &api.RouteFilter{Kind: &api.RouteFilter_RequestMirror{RequestMirror: rm}}, nil
}

func createADPRedirectFilter(filter *gwv1.HTTPRequestRedirectFilter) *api.RouteFilter {
	if filter == nil {
		return nil
	}
	var scheme, host string
	var port, statusCode uint32
	if filter.Scheme != nil {
		scheme = *filter.Scheme
	}
	if filter.Hostname != nil {
		host = string(*filter.Hostname)
	}
	if filter.Port != nil {
		port = uint32(*filter.Port)
	}
	if filter.StatusCode != nil {
		statusCode = uint32(*filter.StatusCode)
	}

	ff := &api.RequestRedirect{
		Scheme: scheme,
		Host:   host,
		Port:   port,
		Status: statusCode,
	}
	if filter.Path != nil {
		switch filter.Path.Type {
		case gwv1.PrefixMatchHTTPPathModifier:
			ff.Path = &api.RequestRedirect_Prefix{Prefix: strings.TrimSuffix(*filter.Path.ReplacePrefixMatch, "/")}
		case gwv1.FullPathHTTPPathModifier:
			ff.Path = &api.RequestRedirect_Full{Full: strings.TrimSuffix(*filter.Path.ReplaceFullPath, "/")}
		}
	}
	return &api.RouteFilter{
		Kind: &api.RouteFilter_RequestRedirect{
			RequestRedirect: ff,
		},
	}
}

func headerListToADP(hl []gwv1.HTTPHeader) []*api.Header {
	return slices.Map(hl, func(hl gwv1.HTTPHeader) *api.Header {
		return &api.Header{
			Name:  string(hl.Name),
			Value: hl.Value,
		}
	})
}

// GRPC-specific ADP conversion functions

func createADPGRPCHeadersMatch(match gwv1.GRPCRouteMatch) ([]*api.HeaderMatch, *reporter.RouteCondition) {
	var res []*api.HeaderMatch
	for _, header := range match.Headers {
		tp := gwv1.GRPCHeaderMatchExact
		if header.Type != nil {
			tp = *header.Type
		}
		switch tp {
		case gwv1.GRPCHeaderMatchExact:
			res = append(res, &api.HeaderMatch{
				Name:  string(header.Name),
				Value: &api.HeaderMatch_Exact{Exact: header.Value},
			})
		case gwv1.GRPCHeaderMatchRegularExpression:
			res = append(res, &api.HeaderMatch{
				Name:  string(header.Name),
				Value: &api.HeaderMatch_Regex{Regex: header.Value},
			})
		default:
			// Should never happen, unless a new field is added
			return nil, &reporter.RouteCondition{
				Type:    gwv1.RouteConditionAccepted,
				Status:  metav1.ConditionFalse,
				Reason:  gwv1.RouteReasonUnsupportedValue,
				Message: fmt.Sprintf("unknown type: %q is not supported HeaderMatch type", tp)}
		}
	}

	if len(res) == 0 {
		return nil, nil
	}
	return res, nil
}

func buildADPGRPCFilters(
	ctx RouteContext,
	ns string,
	inputFilters []gwv1.GRPCRouteFilter,
) ([]*api.RouteFilter, *reporter.RouteCondition) {
	var filters []*api.RouteFilter
	var mirrorBackendErr *reporter.RouteCondition
	for _, filter := range inputFilters {
		switch filter.Type {
		case gwv1.GRPCRouteFilterRequestHeaderModifier:
			h := createADPHeadersFilter(filter.RequestHeaderModifier)
			if h == nil {
				continue
			}
			filters = append(filters, h)
		case gwv1.GRPCRouteFilterResponseHeaderModifier:
			h := createADPResponseHeadersFilter(filter.ResponseHeaderModifier)
			if h == nil {
				continue
			}
			filters = append(filters, h)
		case gwv1.GRPCRouteFilterRequestMirror:
			h, err := createADPMirrorFilter(ctx, filter.RequestMirror, ns, schema.GroupVersionKind{
				Group:   "gateway.networking.k8s.io",
				Version: "v1",
				Kind:    "GRPCRoute",
			})
			if err != nil {
				mirrorBackendErr = err
			} else {
				filters = append(filters, h)
			}
		default:
			return nil, &reporter.RouteCondition{
				Type:    gwv1.RouteConditionAccepted,
				Status:  metav1.ConditionFalse,
				Reason:  gwv1.RouteReasonIncompatibleFilters,
				Message: fmt.Sprintf("unsupported filter type %q", filter.Type),
			}
		}
	}
	return filters, mirrorBackendErr
}

func buildADPGRPCDestination(
	ctx RouteContext,
	forwardTo []gwv1.GRPCBackendRef,
	ns string,
) ([]*api.RouteBackend, *reporter.RouteCondition, *reporter.RouteCondition) {
	if forwardTo == nil {
		return nil, nil, nil
	}

	var invalidBackendErr *reporter.RouteCondition
	var res []*api.RouteBackend
	for _, fwd := range forwardTo {
		dst, err := buildADPDestination(ctx, gwv1.HTTPBackendRef{
			BackendRef: fwd.BackendRef,
			Filters:    nil, // GRPC filters are handled separately
		}, ns, schema.GroupVersionKind{
			Group:   "gateway.networking.k8s.io",
			Version: "v1",
			Kind:    "GRPCRoute",
		}, ctx.Backends)
		if err != nil {
			logger.Error("error building agent gateway destination", "error", err)
			if isInvalidBackend(err) {
				invalidBackendErr = err
				// keep going, we will gracefully drop invalid backends
			} else {
				return nil, nil, err
			}
		}
		if dst != nil {
			filters, err := buildADPGRPCFilters(ctx, ns, fwd.Filters)
			if err != nil {
				return nil, nil, err
			}
			dst.Filters = filters
		}
		res = append(res, dst)
	}
	return res, invalidBackendErr, nil
}
