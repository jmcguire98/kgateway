package nack

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// NackStatusUpdate represents a status change derived from a NACK/ACK event.
type NackStatusUpdate struct {
	Gateway    types.NamespacedName
	NackID     string
	IsRecovery bool
	Message    string
	TypeURL    string
	ObservedAt string
}

// GatewayNackState tracks the active NACK state for a single Gateway
// and computes the appropriate Gateway status condition.
type GatewayNackState struct {
	Gateway     types.NamespacedName
	ActiveNacks map[string]string // nackID -> error message
}

// AddNack adds a NACK to the Gateway's active NACK set.
func (g *GatewayNackState) AddNack(nackID, message string) {
	if g.ActiveNacks == nil {
		g.ActiveNacks = make(map[string]string)
	}

	g.ActiveNacks[nackID] = message
}

// RemoveNack removes a NACK from the Gateway's active set.
func (g *GatewayNackState) RemoveNack(nackID string) {
	if g.ActiveNacks == nil {
		return
	}
	delete(g.ActiveNacks, nackID)
}

// ComputeStatus computes the Gateway status based on the current set of active NACKs.
// This is a pure function that returns a computed status without side effects.
// Implements the multiple NACK handling strategy:
// - No active NACKs: No status returned
// - One active NACK: Programmed=False with specific error message
// - Multiple active NACKs: Programmed=False with aggregated error count
func (g *GatewayNackState) ComputeStatus() *gwv1.GatewayStatus {
	if len(g.ActiveNacks) == 0 {
		// if there are no active NACKs, return nil (let the caller decide what the status should be)
		return nil
	} else {
		var message string
		if len(g.ActiveNacks) == 1 {
			for _, msg := range g.ActiveNacks {
				message = fmt.Sprintf("Configuration rejected: %s", msg)
				break
			}
		} else {
			message = fmt.Sprintf("Configuration rejected: %d errors found", len(g.ActiveNacks))
		}

		return &gwv1.GatewayStatus{
			Conditions: []metav1.Condition{
				{
					Type:               string(gwv1.GatewayConditionProgrammed),
					Status:             metav1.ConditionFalse,
					Reason:             string(gwv1.GatewayReasonInvalid),
					Message:            message,
					LastTransitionTime: metav1.Now(),
				},
			},
		}
	}
}
