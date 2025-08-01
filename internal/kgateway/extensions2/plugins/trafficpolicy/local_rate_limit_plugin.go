package trafficpolicy

import (
	"time"

	envoycorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	localratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/local_ratelimit/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/ir"
)

const (
	localRatelimitFilterEnabledRuntimeKey  = "local_rate_limit_enabled"
	localRatelimitFilterEnforcedRuntimeKey = "local_rate_limit_enforced"
	localRatelimitFilterDisabledRuntimeKey = "local_rate_limit_disabled"
)

func localRateLimitForSpec(spec v1alpha1.TrafficPolicySpec, out *trafficPolicySpecIr) error {
	if spec.RateLimit == nil || spec.RateLimit.Local == nil {
		return nil
	}

	var err error
	if spec.RateLimit.Local != nil {
		out.localRateLimit, err = toLocalRateLimitFilterConfig(spec.RateLimit.Local)
		if err != nil {
			// In case of an error with translating the local rate limit configuration,
			// the route will be dropped
			return err
		}
	}
	return nil
}

func toLocalRateLimitFilterConfig(t *v1alpha1.LocalRateLimitPolicy) (*localratelimitv3.LocalRateLimit, error) {
	if t == nil {
		return nil, nil
	}

	// If the local rate limit policy is empty, we add a LocalRateLimit configuration that disables
	// any other applied local rate limit policy (if any) for the target.
	if *t == (v1alpha1.LocalRateLimitPolicy{}) {
		return createDisabledRateLimit(), nil
	}

	tokenBucket := &typev3.TokenBucket{}
	if t.TokenBucket != nil {
		fillInterval, err := time.ParseDuration(string(t.TokenBucket.FillInterval))
		if err != nil {
			return nil, err
		}
		tokenBucket.FillInterval = durationpb.New(fillInterval)
		tokenBucket.MaxTokens = t.TokenBucket.MaxTokens
		if t.TokenBucket.TokensPerFill != nil {
			tokenBucket.TokensPerFill = wrapperspb.UInt32(*t.TokenBucket.TokensPerFill)
		}
	}

	var lrl *localratelimitv3.LocalRateLimit = &localratelimitv3.LocalRateLimit{
		StatPrefix:  localRateLimitStatPrefix,
		TokenBucket: tokenBucket,
		// By default filter is enabled for 0% of the requests. We enable it for all requests.
		// TODO: Make this configurable in the rate limit policy API.
		FilterEnabled: &envoycorev3.RuntimeFractionalPercent{
			RuntimeKey: localRatelimitFilterEnabledRuntimeKey,
			DefaultValue: &typev3.FractionalPercent{
				Numerator:   100,
				Denominator: typev3.FractionalPercent_HUNDRED,
			},
		},
		// By default filter is enforced for 0% of the requests (out of the enabled fraction).
		// We enable it for all requests.
		// TODO: Make this configurable in the rate limit policy API.
		FilterEnforced: &envoycorev3.RuntimeFractionalPercent{
			RuntimeKey: localRatelimitFilterEnforcedRuntimeKey,
			DefaultValue: &typev3.FractionalPercent{
				Numerator:   100,
				Denominator: typev3.FractionalPercent_HUNDRED,
			},
		},
	}

	return lrl, nil
}

// createDisabledRateLimit returns a LocalRateLimit configuration that disables rate limiting.
// This is used when an empty policy is provided to override any existing rate limit configuration.
func createDisabledRateLimit() *localratelimitv3.LocalRateLimit {
	return &localratelimitv3.LocalRateLimit{
		StatPrefix: localRateLimitStatPrefix,
		// Config per route requires a token bucket, so we create a minimal one
		TokenBucket: &typev3.TokenBucket{
			MaxTokens:    1,
			FillInterval: durationpb.New(1),
		},
		// Set filter enabled to 0% to effectively disable rate limiting
		FilterEnabled: &envoycorev3.RuntimeFractionalPercent{
			RuntimeKey:   localRatelimitFilterDisabledRuntimeKey,
			DefaultValue: &typev3.FractionalPercent{},
		},
	}
}

func (p *trafficPolicyPluginGwPass) handleLocalRateLimit(fcn string, typedFilterConfig *ir.TypedFilterConfigMap, localRateLimit *localratelimitv3.LocalRateLimit) {
	if localRateLimit == nil {
		return
	}
	typedFilterConfig.AddTypedConfig(localRateLimitFilterNamePrefix, localRateLimit)

	// Add a filter to the chain. When having a rate limit for a route we need to also have a
	// globally disabled rate limit filter in the chain otherwise it will be ignored.
	// If there is also rate limit for the listener, it will not override this one.
	if p.localRateLimitInChain == nil {
		p.localRateLimitInChain = make(map[string]*localratelimitv3.LocalRateLimit)
	}
	if _, ok := p.localRateLimitInChain[fcn]; !ok {
		p.localRateLimitInChain[fcn] = &localratelimitv3.LocalRateLimit{
			StatPrefix: localRateLimitStatPrefix,
		}
	}
}
