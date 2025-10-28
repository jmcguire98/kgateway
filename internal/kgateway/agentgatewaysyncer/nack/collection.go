package nack

import (
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/krtutil"
	"istio.io/istio/pkg/kube/krt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
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

// ComputeGatewayNackStatus computes whether or not any NACKS are active for a given Gateway.
func ComputeGatewayNackStatus(ctx krt.HandlerContext, gateway *types.NamespacedName, nackEvents krt.Collection[NackStatusUpdate]) *gwv1.GatewayStatus {
	gatewayName := types.NamespacedName{
		Name:      gateway.Name,
		Namespace: gateway.Namespace,
	}

	allNackUpdates := krt.Fetch(ctx, nackEvents)

	nackState := GatewayNackState{
		Gateway:     gatewayName,
		ActiveNacks: make(map[string]string),
	}

	for _, update := range allNackUpdates {
		if update.Gateway != gatewayName {
			continue
		}

		if update.IsRecovery {
			nackState.RemoveNack(update.NackID)
		} else {
			nackState.AddNack(update.NackID, update.Message)
		}
	}

	status := nackState.ComputeStatus()
	return status
}
