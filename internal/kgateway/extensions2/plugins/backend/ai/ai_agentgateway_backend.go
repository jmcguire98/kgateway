package ai

import (
	"fmt"

	"github.com/agentgateway/agentgateway/go/api"
	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
)

func ProcessAIBackendForAgentGateway(be *v1alpha1.Backend) ([]*api.Backend, error) {
	if be.Spec.AI == nil {
		return nil, fmt.Errorf("ai backend spec must not be nil for AI backend type")
	}

	// Extract the provider configuration
	var providerConfig *api.AIBackend

	if be.Spec.AI.LLM != nil {
		providerConfig = buildAIBackendFromLLM(be.Spec.AI.LLM)
	} else if be.Spec.AI.MultiPool != nil && len(be.Spec.AI.MultiPool.Priorities) > 0 &&
		len(be.Spec.AI.MultiPool.Priorities[0].Pool) > 0 {
		// For MultiPool, use the first provider from the first priority pool
		providerConfig = buildAIBackendFromLLM(&be.Spec.AI.MultiPool.Priorities[0].Pool[0])
	} else {
		return nil, fmt.Errorf("AI backend has no valid LLM or MultiPool configuration")
	}

	aiBackend := &api.Backend{
		Name: be.Namespace + "/" + be.Name,
		Kind: &api.Backend_Ai{
			Ai: providerConfig,
		},
	}
	return []*api.Backend{aiBackend}, nil
}

// buildAIBackendFromLLM converts a kgateway LLMProvider to an agentgateway AIBackend
func buildAIBackendFromLLM(llm *v1alpha1.LLMProvider) *api.AIBackend {
	// Create AIBackend structure with provider-specific configuration
	aiBackend := &api.AIBackend{}

	// Extract and set provider configuration based on the LLM provider type
	provider := llm.Provider

	if provider.OpenAI != nil {
		model := ""
		if provider.OpenAI.Model != nil {
			model = *provider.OpenAI.Model
		}
		aiBackend.Provider = &api.AIBackend_Openai{
			Openai: &api.AIBackend_OpenAI{
				Model: model,
			},
		}
	} else if provider.AzureOpenAI != nil {
		// TODO: is this the same as open ai
		model := ""
		if provider.OpenAI.Model != nil {
			model = *provider.OpenAI.Model
		}
		aiBackend.Provider = &api.AIBackend_Openai{
			Openai: &api.AIBackend_OpenAI{
				Model: model,
			},
		}
	} else if provider.Anthropic != nil {
		model := ""
		if provider.Anthropic.Model != nil {
			model = *provider.Anthropic.Model
		}
		aiBackend.Provider = &api.AIBackend_Anthropic_{
			Anthropic: &api.AIBackend_Anthropic{
				Model: model,
			},
		}
	} else if provider.Gemini != nil {
		model := provider.Gemini.Model
		aiBackend.Provider = &api.AIBackend_Gemini_{
			Gemini: &api.AIBackend_Gemini{
				Model: model,
			},
		}
	} else if provider.VertexAI != nil {
		model := provider.VertexAI.Model
		aiBackend.Provider = &api.AIBackend_Vertex_{
			Vertex: &api.AIBackend_Vertex{
				Model: model,
			},
		}
	}
	// TODO: add bedrock support

	// Map common override configurations
	if llm.HostOverride != nil {
		aiBackend.Override = &api.AIBackend_Override{
			Host: llm.HostOverride.Host,
			Port: int32(llm.HostOverride.Port),
		}
	}

	return aiBackend
}
