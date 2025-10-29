package nack

import (
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/kgateway-dev/kgateway/v2/pkg/logging"
)

var nackHandlerLog = logging.New("nack/handler")

type NackHandler struct {
	nackStateStore map[types.NamespacedName]map[string]string
	nackPublisher  *Publisher
	mu             sync.RWMutex
}

// NackEvent represents a NACK received from an agentgateway gateway
type NackEvent struct {
	Gateway   types.NamespacedName
	TypeUrl   string
	ErrorMsg  string
	Timestamp time.Time
}

// AckEvent represents a successful ACK received from an agentgateway gateway
type AckEvent struct {
	Gateway   types.NamespacedName
	TypeUrl   string
	Timestamp time.Time
}

func NewNackHandler(nackPublisher *Publisher) *NackHandler {
	return &NackHandler{
		nackStateStore: make(map[types.NamespacedName]map[string]string),
		nackPublisher:  nackPublisher,
		mu:             sync.RWMutex{},
	}
}

// HandleNack publishes a NACK event to the Kubernetes Event API.
func (h *NackHandler) HandleNack(nackEvent *NackEvent) {
	h.nackPublisher.onNack(*nackEvent)
}

// HandleAck publishes an ACK event to the Kubernetes Event API.
func (h *NackHandler) HandleAck(ackEvent *AckEvent) {
	h.nackPublisher.onAck(*ackEvent)
}

// FilterEventsAndConvertToNackStatusUpdate filters Kubernetes Events to only process NACK/ACK events for Gateways.
// Returns nil for events that should be ignored, or a NackStatusUpdate for events relevant events.
func (h *NackHandler) FilterEventsAndUpdateState(event *corev1.Event) error {
	nackHandlerLog.Debug("Processing event", "reason", event.Reason, "kind", event.InvolvedObject.Kind, "name", event.InvolvedObject.Name, "namespace", event.InvolvedObject.Namespace)

	if event.Reason != ReasonNack && event.Reason != ReasonAck {
		nackHandlerLog.Debug("Ignoring event - not a NACK/ACK event", "reason", event.Reason)
		return nil
	}

	nackID, exists := event.Annotations[AnnotationNackID]
	if !exists || nackID == "" {
		return fmt.Errorf("event missing NACK ID annotation: %v", event.Annotations)
	}

	// Handle recovery events (ACKs that reference a previous NACK)
	isRecovery := event.Reason == ReasonAck
	if isRecovery {
		recoveryOf, hasRecovery := event.Annotations[AnnotationRecoveryOf]
		if !hasRecovery || recoveryOf == "" {
			return fmt.Errorf("ACK event missing recovery annotation: %v", event.Annotations)
		}
		h.removeNack(types.NamespacedName{Name: event.InvolvedObject.Name, Namespace: event.InvolvedObject.Namespace}, recoveryOf)
		return nil
	}
	h.addNack(types.NamespacedName{Name: event.InvolvedObject.Name, Namespace: event.InvolvedObject.Namespace}, nackID, event.Message)
	return nil
}

// ComputeStatus computes the Gateway status condition based on the current set of active NACKs for a gateway.
// - No active NACKs: No status returned
// - One active NACK: Programmed=False with specific error message
// - Multiple active NACKs: Programmed=False with aggregated error count
func (h *NackHandler) ComputeStatus(gateway types.NamespacedName) *metav1.Condition {
	h.mu.RLock()
	defer h.mu.RUnlock()
	activeNacks := h.nackStateStore[gateway]
	if len(activeNacks) == 0 {
		// if there are no active NACKs, return nil (let the caller decide what the status should be)
		return nil
	} else {
		var message string
		if len(activeNacks) == 1 {
			for _, msg := range activeNacks {
				message = fmt.Sprintf("Configuration rejected: %s", msg)
				break
			}
		} else {
			message = fmt.Sprintf("Configuration rejected: %d errors found", len(activeNacks))
		}

		return &metav1.Condition{
			Type:               string(gwv1.GatewayConditionProgrammed),
			Status:             metav1.ConditionFalse,
			Reason:             string(gwv1.GatewayReasonInvalid),
			Message:            message,
			LastTransitionTime: metav1.Now(),
		}
	}
}

// addNack adds a NACK to the Gateway's active NACK set when a NACK event is received via the Kubernetes Event API.
func (h *NackHandler) addNack(gateway types.NamespacedName, nackID, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.nackStateStore[gateway] == nil {
		h.nackStateStore[gateway] = make(map[string]string)
	}
	h.nackStateStore[gateway][nackID] = message
}

// removeNack removes a NACK from the Gateway's active set when an ACK event is received via the Kubernetes Event API.
func (h *NackHandler) removeNack(gateway types.NamespacedName, nackID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.nackStateStore[gateway] == nil {
		return
	}
	delete(h.nackStateStore[gateway], nackID)
}
