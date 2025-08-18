package plugins

import (
	"fmt"

	"github.com/agentgateway/agentgateway/go/api"
	"istio.io/istio/pkg/kube/kclient"
	"istio.io/istio/pkg/kube/krt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/logging"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/kubeutils"
)

const (
	extauthPolicySuffix = ":extauth"
	aiPolicySuffix      = ":ai"
)

// NewTrafficPlugin creates a new TrafficPolicy plugin
func NewTrafficPlugin(agw *AgwCollections) AgentgatewayPlugin {
	col := krt.WrapClient(kclient.NewFiltered[*v1alpha1.TrafficPolicy](
		agw.Client,
		kclient.Filter{ObjectFilter: agw.Client.ObjectFilter()},
	), agw.KrtOpts.ToOptions("TrafficPolicy")...)
	policyCol := krt.NewManyCollection(col, func(krtctx krt.HandlerContext, policyCR *v1alpha1.TrafficPolicy) []ADPPolicy {
		return translateTrafficPolicy(krtctx, agw.GatewayExtensions, policyCR)
	})

	return AgentgatewayPlugin{
		ContributesPolicies: map[schema.GroupKind]PolicyPlugin{
			wellknown.TrafficPolicyGVK.GroupKind(): {
				Policies: policyCol,
			},
		},
		ExtraHasSynced: func() bool {
			return policyCol.HasSynced()
		},
	}
}

// translateTrafficPolicy generates policies for a single traffic policy
func translateTrafficPolicy(ctx krt.HandlerContext, gatewayExtensions krt.Collection[*v1alpha1.GatewayExtension], trafficPolicy *v1alpha1.TrafficPolicy) []ADPPolicy {
	logger := logging.New("agentgateway/plugins/traffic")
	var adpPolicies []ADPPolicy

	for _, target := range trafficPolicy.Spec.TargetRefs {
		var policyTarget *api.PolicyTarget

		switch string(target.Kind) {
		case wellknown.GatewayKind:
			policyTarget = &api.PolicyTarget{
				Kind: &api.PolicyTarget_Gateway{
					Gateway: trafficPolicy.Namespace + "/" + string(target.Name),
				},
			}
			// TODO(npolshak): add listener support once https://github.com/agentgateway/agentgateway/pull/323 goes in
			//if target.SectionName != nil {
			//	policyTarget = &api.PolicyTarget{
			//		Kind: &api.PolicyTarget_Listener{
			//			Listener: InternalGatewayName(trafficPolicy.Namespace, string(target.Name), string(*target.SectionName)),
			//		},
			//	}
			//}

		case wellknown.HTTPRouteKind:
			policyTarget = &api.PolicyTarget{
				Kind: &api.PolicyTarget_Route{
					Route: trafficPolicy.Namespace + "/" + string(target.Name),
				},
			}
			// TODO(npolshak): add route rule support once https://github.com/agentgateway/agentgateway/pull/323 goes in
			//if target.SectionName != nil {
			//	policyTarget = &api.PolicyTarget{
			//		Kind: &api.PolicyTarget_RouteRule{
			//			RouteRule: trafficPolicy.Namespace + "/" + string(target.Name) + "/" + string(*target.SectionName),
			//		},
			//	}
			//}

		default:
			logger.Warn("unsupported target kind", "kind", target.Kind, "policy", trafficPolicy.Name)
			continue
		}

		if policyTarget != nil {
			translatedPolicies := translateTrafficPolicyToADP(ctx, gatewayExtensions, trafficPolicy, string(target.Name), policyTarget)
			adpPolicies = append(adpPolicies, translatedPolicies...)
		}
	}

	return adpPolicies
}

// translateTrafficPolicyToADP converts a TrafficPolicy to agentgateway Policy resources
func translateTrafficPolicyToADP(ctx krt.HandlerContext, gatewayExtensions krt.Collection[*v1alpha1.GatewayExtension], trafficPolicy *v1alpha1.TrafficPolicy, policyTargetName string, policyTarget *api.PolicyTarget) []ADPPolicy {
	adpPolicies := make([]ADPPolicy, 0)

	// Generate a base policy name from the TrafficPolicy reference
	policyName := fmt.Sprintf("trafficpolicy/%s/%s/%s", trafficPolicy.Namespace, trafficPolicy.Name, policyTargetName)

	// Convert ExtAuth policy if present
	if trafficPolicy.Spec.ExtAuth != nil && trafficPolicy.Spec.ExtAuth.ExtensionRef != nil {
		extAuthPolicies := processExtAuthPolicy(ctx, gatewayExtensions, trafficPolicy, policyName, policyTarget)
		adpPolicies = append(adpPolicies, extAuthPolicies...)
	}

	// Process AI policies if present
	if trafficPolicy.Spec.AI != nil {
		aiPolicies := processAIPolicy(ctx, trafficPolicy, policyName, policyTarget)
		adpPolicies = append(adpPolicies, aiPolicies...)
	}

	return adpPolicies
}

// processExtAuthPolicy processes ExtAuth configuration and creates corresponding agentgateway policies
func processExtAuthPolicy(ctx krt.HandlerContext, gatewayExtensions krt.Collection[*v1alpha1.GatewayExtension], trafficPolicy *v1alpha1.TrafficPolicy, policyName string, policyTarget *api.PolicyTarget) []ADPPolicy {
	logger := logging.New("agentgateway/plugins/traffic")

	// Look up the GatewayExtension referenced by the ExtAuth policy
	extensionName := trafficPolicy.Spec.ExtAuth.ExtensionRef.Name
	extensionNamespace := string(ptr.Deref(trafficPolicy.Spec.ExtAuth.ExtensionRef.Namespace, ""))
	if extensionNamespace == "" {
		extensionNamespace = trafficPolicy.Namespace
	}
	gwExtKey := fmt.Sprintf("%s/%s", extensionNamespace, extensionName)
	gwExt := krt.FetchOne(ctx, gatewayExtensions, krt.FilterKey(gwExtKey))

	if gwExt == nil || (*gwExt).Spec.Type != v1alpha1.GatewayExtensionTypeExtAuth || (*gwExt).Spec.ExtAuth == nil {
		logger.Error("gateway extension not found or not of type ExtAuth", "extension", gwExtKey)
		return nil
	}
	extAuth := (*gwExt).Spec.ExtAuth

	// Extract service target from GatewayExtension's ExtAuth configuration
	var extauthSvcTarget *api.BackendReference
	if extAuth.GrpcService != nil && extAuth.GrpcService.BackendRef != nil {
		backendRef := extAuth.GrpcService.BackendRef
		serviceName := string(backendRef.Name)
		port := uint32(80) // default port
		if backendRef.Port != nil {
			port = uint32(*backendRef.Port)
		}
		// use trafficPolicy namespace as default
		namespace := trafficPolicy.Namespace
		if backendRef.Namespace != nil {
			namespace = string(*backendRef.Namespace)
		}
		serviceHost := kubeutils.ServiceFQDN(metav1.ObjectMeta{Namespace: namespace, Name: serviceName})
		extauthSvcTarget = &api.BackendReference{
			Kind: &api.BackendReference_Service{Service: namespace + "/" + serviceHost},
			Port: port,
		}
	}

	if extauthSvcTarget == nil {
		logger.Warn("failed to translate traffic policy", "policy", trafficPolicy.Name, "target", policyTarget, "error", "missing extauthservice target")
		return nil
	}

	extauthPolicy := &api.Policy{
		Name:   policyName + extauthPolicySuffix,
		Target: policyTarget,
		Spec: &api.PolicySpec{
			Kind: &api.PolicySpec_ExtAuthz{
				ExtAuthz: &api.PolicySpec_ExternalAuth{
					Target:  extauthSvcTarget,
					Context: trafficPolicy.Spec.ExtAuth.ContextExtensions,
				},
			},
		},
	}

	logger.Debug("generated ExtAuth policy",
		"policy", trafficPolicy.Name,
		"agentgateway_policy", extauthPolicy.Name,
		"target", extauthSvcTarget)

	return []ADPPolicy{{Policy: extauthPolicy}}
}

// processAIPolicy processes AI configuration and creates corresponding ADP policies
func processAIPolicy(ctx krt.HandlerContext, trafficPolicy *v1alpha1.TrafficPolicy, policyName string, policyTarget *api.PolicyTarget) []ADPPolicy {
	logger := logging.New("agentgateway/plugins/traffic")

	aiSpec := trafficPolicy.Spec.AI
	if aiSpec.PromptGuard == nil {
		return nil
	}

	// Create AI policy
	aiPolicy := &api.Policy{
		Name:   policyName + aiPolicySuffix,
		Target: policyTarget,
		Spec: &api.PolicySpec{
			Kind: &api.PolicySpec_Ai_{
				Ai: &api.PolicySpec_Ai{
					PromptGuard: &api.PolicySpec_Ai_PromptGuard{},
				},
			},
		},
	}

	// Configure prompt enrichment if specified
	if aiSpec.PromptEnrichment != nil {
		enrichment := &api.PolicySpec_Ai_PromptEnrichment{}

		// Add prepend messages
		for _, msg := range aiSpec.PromptEnrichment.Prepend {
			enrichment.Prepend = append(enrichment.Prepend, &api.PolicySpec_Ai_Message{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}

		// Add append messages
		for _, msg := range aiSpec.PromptEnrichment.Append {
			enrichment.Append = append(enrichment.Append, &api.PolicySpec_Ai_Message{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}

		aiPolicy.GetSpec().GetAi().Prompts = enrichment
	}

	// Configure request prompt guard if specified
	if aiSpec.PromptGuard.Request != nil {
		req := aiSpec.PromptGuard.Request
		pgReq := &api.PolicySpec_Ai_RequestGuard{}

		// Add custom response if specified
		if req.CustomResponse != nil {
			pgReq.Rejection = &api.PolicySpec_Ai_RequestRejection{
				Body:   []byte(*req.CustomResponse.Message),
				Status: *req.CustomResponse.StatusCode,
			}
		}

		if req.Webhook != nil {
			pgReq.Webhook = &api.PolicySpec_Ai_Webhook{
				Host: req.Webhook.Host.Host,
				Port: uint32(req.Webhook.Host.Port),
			}
		}

		// Add regex patterns if specified
		if req.Regex != nil {
			pgReq.Regex = &api.PolicySpec_Ai_RegexRules{}
			if req.Regex.Action != nil {
				pgReq.Regex.Action = &api.PolicySpec_Ai_Action{}
				if *req.Regex.Action == v1alpha1.MASK {
					pgReq.Regex.Action.Kind = api.PolicySpec_Ai_MASK
				} else if *req.Regex.Action == v1alpha1.REJECT {
					pgReq.Regex.Action.Kind = api.PolicySpec_Ai_REJECT
					pgReq.Regex.Action.RejectResponse = &api.PolicySpec_Ai_RequestRejection{}
					// TODO (jmcguire): it's not clear to me as yet how the user is so supposed to provide a different custom response
					// for the reject action (as opposed to the top level custom response, but we do make a distinction in the dataplane)
					// so i'm just using the top level custom response for now under the reject action
					if req.CustomResponse != nil && req.CustomResponse.Message != nil {
						pgReq.Regex.Action.RejectResponse.Body = []byte(*req.CustomResponse.Message)
					}
					if req.CustomResponse != nil && req.CustomResponse.StatusCode != nil {
						pgReq.Regex.Action.RejectResponse.Status = *req.CustomResponse.StatusCode
					}
				} else {
					logger.Warn("unsupported regex action", "action", *req.Regex.Action)
					pgReq.Regex.Action.Kind = api.PolicySpec_Ai_ACTION_UNSPECIFIED
				}
			}

			for _, match := range req.Regex.Matches {
				pgReq.Regex.Rules = append(pgReq.Regex.Rules, &api.PolicySpec_Ai_RegexRule{
					Kind: &api.PolicySpec_Ai_RegexRule_Regex{
						Regex: &api.PolicySpec_Ai_NamedRegex{
							Pattern: *match.Pattern,
							Name:    *match.Name,
						},
					},
				})
			}
		}

		aiPolicy.GetSpec().GetAi().PromptGuard.Request = pgReq
	}

	// Configure response prompt guard if specified
	if aiSpec.PromptGuard.Response != nil {
		resp := aiSpec.PromptGuard.Response
		pgResp := &api.PolicySpec_Ai_ResponseGuard{}

		// Add webhook if specified
		if resp.Webhook != nil {
			pgResp.Webhook = &api.PolicySpec_Ai_Webhook{
				Host: resp.Webhook.Host.Host,
				Port: uint32(resp.Webhook.Host.Port),
			}
		}

		// Add regex patterns if specified
		if resp.Regex != nil {
			pgResp.Regex = &api.PolicySpec_Ai_RegexRules{}
			if resp.Regex.Action != nil {
				pgResp.Regex.Action = &api.PolicySpec_Ai_Action{}
				if *resp.Regex.Action == v1alpha1.MASK {
					pgResp.Regex.Action.Kind = api.PolicySpec_Ai_MASK
				} else if *resp.Regex.Action == v1alpha1.REJECT {
					pgResp.Regex.Action.Kind = api.PolicySpec_Ai_REJECT
					pgResp.Regex.Action.RejectResponse = &api.PolicySpec_Ai_RequestRejection{}
					// For response guard, we don't have custom response settings
				} else {
					logger.Warn("unsupported regex action", "action", *resp.Regex.Action)
					pgResp.Regex.Action.Kind = api.PolicySpec_Ai_ACTION_UNSPECIFIED
				}
			}

			// Add regex patterns
			for _, match := range resp.Regex.Matches {
				pgResp.Regex.Rules = append(pgResp.Regex.Rules, &api.PolicySpec_Ai_RegexRule{
					Kind: &api.PolicySpec_Ai_RegexRule_Regex{
						Regex: &api.PolicySpec_Ai_NamedRegex{
							Pattern: *match.Pattern,
							Name:    *match.Name,
						},
					},
				})
			}

			// Add builtin patterns
			for _, builtin := range resp.Regex.Builtins {
				pgResp.Regex.Rules = append(pgResp.Regex.Rules, &api.PolicySpec_Ai_RegexRule{
					Kind: &api.PolicySpec_Ai_RegexRule_Builtin{
						Builtin: api.PolicySpec_Ai_BuiltinRegexRule(api.PolicySpec_Ai_BuiltinRegexRule_value[string(builtin)]),
					},
				})
			}
		}

		aiPolicy.GetSpec().GetAi().PromptGuard.Response = pgResp
	}

	logger.Debug("generated AI policy",
		"policy", trafficPolicy.Name,
		"agentgateway_policy", aiPolicy.Name)

	return []ADPPolicy{{Policy: aiPolicy}}
}
