package nack

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Event reasons for Kubernetes Events created by agentgateway NACK/ACK detection
const (
	ReasonNack = "AgentgatewayNACK"
	ReasonAck  = "AgentgatewayACK"
)

// Annotation keys used for NACK events
const (
	// AnnotationNackID is a stable hash identifying a specific NACK event
	AnnotationNackID = "kgateway.dev/nack-id"

	// AnnotationTypeURL is the xDS TypeURL that was rejected
	AnnotationTypeURL = "kgateway.dev/type-url"

	// AnnotationObservedAt is the RFC3339 timestamp when the NACK was last observed
	AnnotationObservedAt = "kgateway.dev/observed-at"

	// AnnotationRecoveryOf points to the NACK ID that this ACK event resolves
	AnnotationRecoveryOf = "kgateway.dev/recovery-of"
)

// ComputeNackID creates a stable, identifier for a NACK event.
// The same combination of inputs will always produce the same NACK ID.
func ComputeNackID(gatewayNamespacedName, typeURL string) string {
	h := sha256.New()

	input := strings.Join([]string{gatewayNamespacedName, typeURL}, "|")

	h.Write([]byte(input))

	// Return first 16 hex characters (64 bits) for readability
	return hex.EncodeToString(h.Sum(nil))[:16]
}
