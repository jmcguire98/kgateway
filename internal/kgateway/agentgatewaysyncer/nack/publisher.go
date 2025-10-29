package nack

import (
	"context"
	"time"

	"istio.io/istio/pkg/kube"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/logging"
)

var log = logging.New("nack/publisher")

// Publisher converts NACK events from the agentgateway xDS server into Kubernetes Events.
type Publisher struct {
	ctx             context.Context
	client          kube.Client
	systemNamespace string
}

// NewPublisher creates a new NACK event publisher that will publish Events
// using the provided Kubernetes client in the specified system namespace.
func NewPublisher(ctx context.Context, client kube.Client, systemNamespace string) *Publisher {
	return &Publisher{
		client:          client,
		ctx:             ctx,
		systemNamespace: systemNamespace,
	}
}

// OnNack publishes a NACK event as a Kubernetes Event.
// It converts a NACK event from the xDS server into a Kubernetes Event
func (p *Publisher) onNack(event NackEvent) {

	// TODO: check if version / code is available from the event and if not remove the params from ComputeNackID
	nackID := ComputeNackID(event.Gateway.Namespace+"/"+event.Gateway.Name, event.TypeUrl)

	k8sEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "agentgateway-nack-",
			Namespace:    event.Gateway.Namespace,
			Annotations: map[string]string{
				AnnotationNackID:     nackID,
				AnnotationTypeURL:    event.TypeUrl,
				AnnotationObservedAt: event.Timestamp.Format(time.RFC3339),
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       wellknown.GatewayKind,
			APIVersion: wellknown.GatewayGVK.GroupVersion().String(),
			Name:       event.Gateway.Name,
			Namespace:  event.Gateway.Namespace,
		},
		Reason:              ReasonNack,
		Message:             event.ErrorMsg,
		Type:                corev1.EventTypeWarning,
		LastTimestamp:       metav1.NewTime(event.Timestamp),
		Count:               1,
		ReportingController: wellknown.DefaultAgwControllerName,
	}

	_, err := p.client.Kube().CoreV1().Events(event.Gateway.Namespace).Create(
		p.ctx, k8sEvent, metav1.CreateOptions{},
	)
	if err != nil && !errors.IsAlreadyExists(err) {
		log.Error("Failed to publish NACK event for Gateway", "gateway", event.Gateway, "error", err)
		return
	}

	log.Debug("Published NACK event for Gateway", "gateway", event.Gateway, "nackID", nackID, "typeURL", event.TypeUrl)
}

// OnAck publishes an ACK event as a Kubernetes Event.
// It converts an ACK event from the xDS server into a Kubernetes Event
func (p *Publisher) onAck(event AckEvent) {
	recoveredNackID := ComputeNackID(event.Gateway.Namespace+"/"+event.Gateway.Name, event.TypeUrl)

	k8sEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "agentgateway-ack-",
			Namespace:    event.Gateway.Namespace,
			Annotations: map[string]string{
				AnnotationNackID:     ComputeNackID(event.Gateway.Namespace+"/"+event.Gateway.Name, event.TypeUrl),
				AnnotationTypeURL:    event.TypeUrl,
				AnnotationVersion:    event.Version,
				AnnotationRecoveryOf: recoveredNackID,
				AnnotationObservedAt: event.Timestamp.Format(time.RFC3339),
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       wellknown.GatewayKind,
			APIVersion: wellknown.GatewayGVK.GroupVersion().String(),
			Name:       event.Gateway.Name,
			Namespace:  event.Gateway.Namespace,
		},
		Reason:              ReasonAck,
		Message:             "Configuration accepted successfully",
		Type:                corev1.EventTypeNormal,
		LastTimestamp:       metav1.NewTime(event.Timestamp),
		Count:               1,
		ReportingController: wellknown.DefaultAgwControllerName,
	}

	_, err := p.client.Kube().CoreV1().Events(event.Gateway.Namespace).Create(
		p.ctx, k8sEvent, metav1.CreateOptions{},
	)
	if err != nil && !errors.IsAlreadyExists(err) {
		log.Error("Failed to publish ACK event for Gateway", "gateway", event.Gateway, "error", err)
		return
	}

	log.Debug("Published ACK event for Gateway", "gateway", event.Gateway, "typeURL", event.TypeUrl, "version", event.Version)
}
