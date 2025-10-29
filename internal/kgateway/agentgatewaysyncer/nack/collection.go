package nack

import (
	"sort"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/logging"
	"istio.io/istio/pkg/kube/krt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var collectionLog = logging.New("nack/collection")

// FilterEventsAndConvertToNackStatusUpdate filters Kubernetes Events to only process NACK/ACK events for Gateways.
// Returns nil for events that should be ignored, or a NackStatusUpdate for events relevant events.
func FilterEventsAndConvertToNackStatusUpdate(event *corev1.Event) *NackStatusUpdate {
	collectionLog.Debug("Processing event", "reason", event.Reason, "kind", event.InvolvedObject.Kind, "name", event.InvolvedObject.Name, "namespace", event.InvolvedObject.Namespace)

	if event.Reason != ReasonNack && event.Reason != ReasonAck {
		collectionLog.Debug("Ignoring event - not a NACK/ACK event", "reason", event.Reason)
		return nil
	}

	if event.InvolvedObject.Kind != wellknown.GatewayKind {
		collectionLog.Debug("Ignoring event - not a Gateway", "kind", event.InvolvedObject.Kind)
		return nil
	}

	nackID, exists := event.Annotations[AnnotationNackID]
	if !exists || nackID == "" {
		collectionLog.Error("Event missing NACK ID annotation", "event", event.Name, "annotations", event.Annotations)
		return nil
	}

	// Handle recovery events (ACKs that reference a previous NACK)
	isRecovery := event.Reason == ReasonAck
	if isRecovery {
		// For recovery events, check if it references a NACK to recover
		recoveryOf, hasRecovery := event.Annotations[AnnotationRecoveryOf]
		if !hasRecovery || recoveryOf == "" {
			collectionLog.Error("ACK event missing recovery annotation", "event", event.Name, "annotations", event.Annotations)
			return nil
		}
	}

	update := &NackStatusUpdate{
		Gateway: types.NamespacedName{
			Name:      event.InvolvedObject.Name,
			Namespace: event.InvolvedObject.Namespace,
		},
		NackID:     nackID,
		IsRecovery: isRecovery,
		Message:    event.Message,
		TypeURL:    event.Annotations[AnnotationTypeURL],
		ObservedAt: event.Annotations[AnnotationObservedAt],
	}

	collectionLog.Debug("Created NACK status update", "gateway", update.Gateway, "nackID", update.NackID, "isRecovery", update.IsRecovery, "message", update.Message)
	return update
}

// ComputeGatewayNackStatus computes whether or not any NACKS are active for a given Gateway by processing Events.
func ComputeGatewayNackStatus(ctx krt.HandlerContext, gateway *types.NamespacedName, events krt.Collection[*corev1.Event]) *gwv1.GatewayStatus {
	allEvents := krt.Fetch(ctx, events)
	return ComputeGatewayNackStatusFromEvents(ctx, gateway, allEvents)
}

// ComputeGatewayNackStatusFromEvents computes whether or not any NACKS are active for a given Gateway from a slice of Events.
// This function is used when Events have already been fetched to establish KRT dependencies.
func ComputeGatewayNackStatusFromEvents(ctx krt.HandlerContext, gateway *types.NamespacedName, allEvents []*corev1.Event) *gwv1.GatewayStatus {
	gatewayName := types.NamespacedName{
		Name:      gateway.Name,
		Namespace: gateway.Namespace,
	}

	collectionLog.Debug("Computing NACK status for Gateway", "gateway", gatewayName, "totalEvents", len(allEvents))

	// Sort events by LastTimestamp to ensure chronological processing (most recent event wins)
	sort.Slice(allEvents, func(i, j int) bool {
		return allEvents[i].LastTimestamp.Before(&allEvents[j].LastTimestamp)
	})

	nackState := GatewayNackState{
		Gateway:     gatewayName,
		ActiveNacks: make(map[string]string),
	}

	relevantUpdates := 0
	for _, event := range allEvents {
		// Convert event to NackStatusUpdate
		update := FilterEventsAndConvertToNackStatusUpdate(event)
		if update == nil {
			continue
		}

		// Only process events for this gateway
		if update.Gateway != gatewayName {
			continue
		}
		relevantUpdates++

		collectionLog.Debug("Processing NACK update for Gateway", "gateway", gatewayName, "nackID", update.NackID, "isRecovery", update.IsRecovery, "message", update.Message)

		if update.IsRecovery {
			nackState.RemoveNack(update.NackID)
		} else {
			nackState.AddNack(update.NackID, update.Message)
		}
	}

	collectionLog.Debug("Processed NACK updates for Gateway", "gateway", gatewayName, "relevantUpdates", relevantUpdates, "activeNacks", len(nackState.ActiveNacks))

	status := nackState.ComputeStatus()
	if status != nil {
		collectionLog.Debug("Generated NACK status for Gateway", "gateway", gatewayName, "conditions", len(status.Conditions))
	} else {
		collectionLog.Debug("No NACK status generated for Gateway", "gateway", gatewayName)
	}
	return status
}
