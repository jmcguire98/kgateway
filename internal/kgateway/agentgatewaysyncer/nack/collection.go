package nack

import (
	"istio.io/istio/pkg/kube/krt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/krtutil"
)

// FilterEventsAndConvertToNackStatusUpdate filters Kubernetes Events to only process NACK/ACK events for Gateways.
// Returns nil for events that should be ignored, or a NackStatusUpdate for events relevant events.
func FilterEventsAndConvertToNackStatusUpdate(event *corev1.Event) *NackStatusUpdate {
	if event.Reason != ReasonNack && event.Reason != ReasonAck {
		return nil
	}

	if event.InvolvedObject.Kind != wellknown.GatewayKind {
		return nil
	}

	nackID, exists := event.Annotations[AnnotationNackID]
	if !exists || nackID == "" {
		// TODO: log error here (this should never happen)
		return nil
	}

	// Handle recovery events (ACKs that reference a previous NACK)
	isRecovery := event.Reason == ReasonAck
	if isRecovery {
		// For recovery events, check if it references a NACK to recover
		recoveryOf, hasRecovery := event.Annotations[AnnotationRecoveryOf]
		if !hasRecovery || recoveryOf == "" {
			// TODO: log error here (this should never happen)
			return nil
		}
	}

	return &NackStatusUpdate{
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
}

// CreateNackEventCollection creates a KRT collection that watches Kubernetes Events
// and converts NACK/ACK events into NackStatusUpdate objects for further processing.
func CreateNackEventCollection(
	events krt.Collection[*corev1.Event],
	opts krtutil.KrtOptions,
) krt.Collection[NackStatusUpdate] {
	// Transform Events into NackStatusUpdate objects, filtering out irrelevant events
	return krt.NewCollection(events, func(ctx krt.HandlerContext, event *corev1.Event) *NackStatusUpdate {
		return FilterEventsAndConvertToNackStatusUpdate(event)
	}, opts.ToOptions("NackEvents")...)
}

// CreateNackStatusCollection creates a KRT collection that computes Gateway status
// from NACK events. This transforms NACK/ACK events into Gateway status updates
// that can be fed into the existing Gateway status collection system.
func CreateNackStatusCollection(
	nackEvents krt.Collection[NackStatusUpdate],
	opts krtutil.KrtOptions,
) krt.Collection[gwv1.GatewayStatus] {
	// Group NACK events by Gateway for aggregation
	gatewayIndex := krt.NewIndex(nackEvents, "GatewayNackIndex", func(update NackStatusUpdate) []types.NamespacedName {
		return []types.NamespacedName{update.Gateway}
	})

	// Track processed NACK IDs to avoid duplicate processing of the same event
	// TODO: I'm not sure if we still need this, come back to it.
	processedNacks := make(map[string]bool)

	// Create collection that aggregates all NACK events per Gateway into status
	return krt.NewCollection(nackEvents, func(
		ctx krt.HandlerContext,
		update NackStatusUpdate,
	) *gwv1.GatewayStatus {
		// Get all NACK events for this Gateway using the index
		updates := gatewayIndex.Lookup(update.Gateway)
		if len(updates) == 0 {
			return nil
		}

		// Skip if this specific update was already processed (deduplication)
		nackKey := update.NackID + ":" + update.Gateway.String()
		if processedNacks[nackKey] {
			return nil
		}
		processedNacks[nackKey] = true

		// Create aggregate NACK state for this Gateway
		state := &GatewayNackState{
			Gateway:     update.Gateway,
			ActiveNacks: make(map[string]string),
		}

		// Process all NACK/ACK events for this Gateway
		for _, u := range updates {
			if u.IsRecovery {
				// ACK event - remove the corresponding NACK
				state.RemoveNack(u.NackID)
			} else {
				// NACK event - add to active set
				state.AddNack(u.NackID, u.Message)
			}
		}

		status := state.ComputeStatus()
		return &status
	}, opts.ToOptions("GatewayNackStatus")...)
}
