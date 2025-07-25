// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
)

// AiExtensionApplyConfiguration represents a declarative configuration of the AiExtension type for use
// with apply.
type AiExtensionApplyConfiguration struct {
	Enabled         *bool                               `json:"enabled,omitempty"`
	Image           *ImageApplyConfiguration            `json:"image,omitempty"`
	SecurityContext *v1.SecurityContext                 `json:"securityContext,omitempty"`
	Resources       *v1.ResourceRequirements            `json:"resources,omitempty"`
	Env             []v1.EnvVar                         `json:"env,omitempty"`
	Ports           []v1.ContainerPort                  `json:"ports,omitempty"`
	Stats           *AiExtensionStatsApplyConfiguration `json:"stats,omitempty"`
	Tracing         *AiExtensionTraceApplyConfiguration `json:"tracing,omitempty"`
}

// AiExtensionApplyConfiguration constructs a declarative configuration of the AiExtension type for use with
// apply.
func AiExtension() *AiExtensionApplyConfiguration {
	return &AiExtensionApplyConfiguration{}
}

// WithEnabled sets the Enabled field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Enabled field is set to the value of the last call.
func (b *AiExtensionApplyConfiguration) WithEnabled(value bool) *AiExtensionApplyConfiguration {
	b.Enabled = &value
	return b
}

// WithImage sets the Image field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Image field is set to the value of the last call.
func (b *AiExtensionApplyConfiguration) WithImage(value *ImageApplyConfiguration) *AiExtensionApplyConfiguration {
	b.Image = value
	return b
}

// WithSecurityContext sets the SecurityContext field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the SecurityContext field is set to the value of the last call.
func (b *AiExtensionApplyConfiguration) WithSecurityContext(value v1.SecurityContext) *AiExtensionApplyConfiguration {
	b.SecurityContext = &value
	return b
}

// WithResources sets the Resources field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Resources field is set to the value of the last call.
func (b *AiExtensionApplyConfiguration) WithResources(value v1.ResourceRequirements) *AiExtensionApplyConfiguration {
	b.Resources = &value
	return b
}

// WithEnv adds the given value to the Env field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Env field.
func (b *AiExtensionApplyConfiguration) WithEnv(values ...v1.EnvVar) *AiExtensionApplyConfiguration {
	for i := range values {
		b.Env = append(b.Env, values[i])
	}
	return b
}

// WithPorts adds the given value to the Ports field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Ports field.
func (b *AiExtensionApplyConfiguration) WithPorts(values ...v1.ContainerPort) *AiExtensionApplyConfiguration {
	for i := range values {
		b.Ports = append(b.Ports, values[i])
	}
	return b
}

// WithStats sets the Stats field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Stats field is set to the value of the last call.
func (b *AiExtensionApplyConfiguration) WithStats(value *AiExtensionStatsApplyConfiguration) *AiExtensionApplyConfiguration {
	b.Stats = value
	return b
}

// WithTracing sets the Tracing field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Tracing field is set to the value of the last call.
func (b *AiExtensionApplyConfiguration) WithTracing(value *AiExtensionTraceApplyConfiguration) *AiExtensionApplyConfiguration {
	b.Tracing = value
	return b
}
