package agentgatewaybackend

import (
	"errors"
	"fmt"

	"github.com/agentgateway/agentgateway/go/api"
)

func ProcessAIBackendForAgentGateway(be *AgentGatewayBackendIr) ([]*api.Backend, []*api.Policy, error) {
	if len(be.Errors) > 0 {
		return nil, nil, fmt.Errorf("errors occurred while processing ai backend for agent gateway: %w", errors.Join(be.Errors...))
	}
	if be.AIIr == nil {
		return nil, nil, fmt.Errorf("ai backend ir must not be nil for AI backend type")
	}

	var apiBackends []*api.Backend
	var policies []*api.Policy

	for i, backendWithAuth := range be.AIIr.Backends {
		backendName := be.AIIr.Name
		if len(be.AIIr.Backends) > 1 {
			backendName = fmt.Sprintf("%s-%d", be.AIIr.Name, i)
		}

		apiBackend := &api.Backend{
			Name: backendName,
			Kind: &api.Backend_Ai{
				Ai: backendWithAuth.Backend,
			},
		}
		apiBackends = append(apiBackends, apiBackend)

		if backendWithAuth.AuthPolicy != nil {
			authPolicy := &api.Policy{
				Name: fmt.Sprintf("auth-%s", backendName),
				Target: &api.PolicyTarget{Kind: &api.PolicyTarget_Backend{
					Backend: backendName,
				}},
				Spec: &api.PolicySpec{Kind: &api.PolicySpec_Auth{
					Auth: backendWithAuth.AuthPolicy,
				}},
			}
			policies = append(policies, authPolicy)
		}
	}

	return apiBackends, policies, nil
}
