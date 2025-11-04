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
	// annotationNackID is a stable hash identifying a specific NACK event
	annotationNackID = "kgateway.dev/nack-id"

	// annotationTypeURL is the xDS TypeURL that was rejected
	annotationTypeURL = "kgateway.dev/type-url"

	// annotationObservedAt is the RFC3339 timestamp when the NACK was last observed
	annotationObservedAt = "kgateway.dev/observed-at"

	// annotationRecoveryOf points to the NACK ID that this ACK event resolves
	annotationRecoveryOf = "kgateway.dev/recovery-of"
)

// computeNackID creates a stable identifier for a NACK event.
// The same combination of inputs will always produce the same NACK ID.
func computeNackID(gatewayNamespacedName, typeURL string) string {
	h := sha256.New()

	input := strings.Join([]string{gatewayNamespacedName, typeURL}, "|")

	h.Write([]byte(input))

	// Return first 16 hex characters (64 bits) for readability
	return hex.EncodeToString(h.Sum(nil))[:16]
}
