// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1alpha1

// CustomAttributeApplyConfiguration represents a declarative configuration of the CustomAttribute type for use
// with apply.
type CustomAttributeApplyConfiguration struct {
	Name          *string                                       `json:"name,omitempty"`
	Literal       *CustomAttributeLiteralApplyConfiguration     `json:"literal,omitempty"`
	Environment   *CustomAttributeEnvironmentApplyConfiguration `json:"environment,omitempty"`
	RequestHeader *CustomAttributeHeaderApplyConfiguration      `json:"requestHeader,omitempty"`
	Metadata      *CustomAttributeMetadataApplyConfiguration    `json:"metadata,omitempty"`
}

// CustomAttributeApplyConfiguration constructs a declarative configuration of the CustomAttribute type for use with
// apply.
func CustomAttribute() *CustomAttributeApplyConfiguration {
	return &CustomAttributeApplyConfiguration{}
}

// WithName sets the Name field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Name field is set to the value of the last call.
func (b *CustomAttributeApplyConfiguration) WithName(value string) *CustomAttributeApplyConfiguration {
	b.Name = &value
	return b
}

// WithLiteral sets the Literal field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Literal field is set to the value of the last call.
func (b *CustomAttributeApplyConfiguration) WithLiteral(value *CustomAttributeLiteralApplyConfiguration) *CustomAttributeApplyConfiguration {
	b.Literal = value
	return b
}

// WithEnvironment sets the Environment field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Environment field is set to the value of the last call.
func (b *CustomAttributeApplyConfiguration) WithEnvironment(value *CustomAttributeEnvironmentApplyConfiguration) *CustomAttributeApplyConfiguration {
	b.Environment = value
	return b
}

// WithRequestHeader sets the RequestHeader field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the RequestHeader field is set to the value of the last call.
func (b *CustomAttributeApplyConfiguration) WithRequestHeader(value *CustomAttributeHeaderApplyConfiguration) *CustomAttributeApplyConfiguration {
	b.RequestHeader = value
	return b
}

// WithMetadata sets the Metadata field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Metadata field is set to the value of the last call.
func (b *CustomAttributeApplyConfiguration) WithMetadata(value *CustomAttributeMetadataApplyConfiguration) *CustomAttributeApplyConfiguration {
	b.Metadata = value
	return b
}
