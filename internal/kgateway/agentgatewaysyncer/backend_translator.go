package agentgatewaysyncer

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentgateway/agentgateway/go/api"
	"istio.io/istio/pkg/kube/krt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	extensionsplug "github.com/kgateway-dev/kgateway/v2/internal/kgateway/extensions2/plugin"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
)

// AgentGatewayBackendTranslator handles translation of backends to agent gateway resources
type AgentGatewayBackendTranslator struct {
	ContributedBackends map[schema.GroupKind]ir.BackendInit
	ContributedPolicies map[schema.GroupKind]extensionsplug.PolicyPlugin
}

// NewAgentGatewayBackendTranslator creates a new AgentGatewayBackendTranslator
func NewAgentGatewayBackendTranslator(extensions extensionsplug.Plugin) *AgentGatewayBackendTranslator {
	translator := &AgentGatewayBackendTranslator{
		ContributedBackends: make(map[schema.GroupKind]ir.BackendInit),
		ContributedPolicies: extensions.ContributesPolicies,
	}

	for k, up := range extensions.ContributesBackends {
		translator.ContributedBackends[k] = up.BackendInit
	}

	return translator
}

// TranslateBackend converts a BackendObjectIR to agent gateway Backend and Policy resources
func (t *AgentGatewayBackendTranslator) TranslateBackend(
	ctx krt.HandlerContext,
	backend *ir.BackendObjectIR,
	svcCol krt.Collection[*corev1.Service],
	secretsCol krt.Collection[*corev1.Secret],
	nsCol krt.Collection[*corev1.Namespace],
) ([]*api.Backend, []*api.Policy, error) {
	gk := schema.GroupKind{
		Group: backend.Group,
		Kind:  backend.Kind,
	}

	process, ok := t.ContributedBackends[gk]
	if !ok {
		return nil, nil, errors.New("no backend translator found for " + gk.String())
	}

	if process.InitAgentBackend == nil {
		return nil, nil, errors.New("no agent backend plugin found for " + gk.String())
	}

	if backend.Errors != nil {
		// The backend has errors so we can't translate it
		// Return the errors to signify it's not a dev error but a real error from backend object translation
		return nil, nil, fmt.Errorf("backend has errors: %w", errors.Join(backend.Errors...))
	}

	// Create AgentGatewayBackendContext with collections
	agentCtx := &ir.AgentGatewayBackendContext{
		Context:    context.TODO(),
		KrtCtx:     ctx,
		Namespaces: nsCol,
		Services:   svcCol,
		Secrets:    secretsCol,
	}

	// Call the plugin's InitAgentBackend function
	backends, policies, err := process.InitAgentBackend(agentCtx, *backend)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize agent backend: %w", err)
	}

	// Process backend policies using translation passes, like envoy does
	for _, agentBackend := range backends {
		err := t.runBackendPolicies(ctx, backend, agentBackend)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to process backend policies: %w", err)
		}
	}

	return backends, policies, nil
}

// runBackendPolicies applies backend policies to the translated backend
func (t *AgentGatewayBackendTranslator) runBackendPolicies(
	ctx krt.HandlerContext,
	backend *ir.BackendObjectIR,
	agentBackend *api.Backend,
) error {
	var errs []error

	// Apply all relevant backend policies to the translated backend
	for gk, policyPlugin := range t.ContributedPolicies {
		// Only process if this policy plugin has ProcessAgentBackend (unified IR-based approach)
		if policyPlugin.ProcessAgentBackend == nil {
			continue
		}

		// Loop through all policies of this GroupKind attached to the backend
		for _, polAttachment := range backend.AttachedPolicies.Policies[gk] {
			// Skip if policy has errors
			if len(polAttachment.Errors) > 0 {
				errs = append(errs, polAttachment.Errors...)
				continue
			}

			// Call ProcessAgentBackend for each attached policy, like envoy calls ProcessBackend
			err := policyPlugin.ProcessAgentBackend(context.TODO(), polAttachment.PolicyIr, *backend)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}
