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

// NewPublisher creates a new NACK event publisher that will publish k8s events
func NewPublisher(ctx context.Context, client kube.Client, systemNamespace string) *Publisher {
	return &Publisher{
		client:          client,
		ctx:             ctx,
		systemNamespace: systemNamespace,
	}
}

// onNack publishes a NACK event as a k8s event.
func (p *Publisher) onNack(event NackEvent) {
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
		log.Error("failed to publish NACK event for Gateway", "gateway", event.Gateway, "error", err)
		return
	}

	log.Debug("published NACK event for Gateway", "gateway", event.Gateway, "nackID", nackID, "typeURL", event.TypeUrl)
}

// onAck publishes an ACK event as a k8s event.
func (p *Publisher) onAck(event AckEvent) {
	recoveredNackID := ComputeNackID(event.Gateway.Namespace+"/"+event.Gateway.Name, event.TypeUrl)

	k8sEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "agentgateway-ack-",
			Namespace:    event.Gateway.Namespace,
			Annotations: map[string]string{
				AnnotationNackID:     ComputeNackID(event.Gateway.Namespace+"/"+event.Gateway.Name, event.TypeUrl),
				AnnotationTypeURL:    event.TypeUrl,
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
		log.Error("failed to publish ACK event for Gateway", "gateway", event.Gateway, "error", err)
		return
	}

	log.Debug("published ACK event for Gateway", "gateway", event.Gateway, "typeURL", event.TypeUrl)
}
