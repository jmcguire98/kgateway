package nack

import (
	"context"
	"time"

	"istio.io/istio/pkg/kube"
	corev1 "k8s.io/api/core/v1"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/logging"
	"github.com/kgateway-dev/kgateway/v2/pkg/schemes"
)

var log = logging.New("nack/publisher")

// Publisher converts NACK events from the agentgateway xDS server into Kubernetes Events.
type Publisher struct {
	ctx           context.Context
	client        kube.Client
	eventRecorder record.EventRecorder
}

// NewPublisher creates a new NACK event publisher that will publish k8s events
func NewPublisher(ctx context.Context, client kube.Client) *Publisher {
	eventBroadcaster := record.NewBroadcaster()
	eventRecorder := eventBroadcaster.NewRecorder(
		schemes.DefaultScheme(),
		corev1.EventSource{Component: wellknown.DefaultAgwControllerName},
	)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: client.Kube().CoreV1().Events(""),
	})

	return &Publisher{
		ctx:           ctx,
		client:        client,
		eventRecorder: eventRecorder,
	}
}

// onNack publishes a NACK event as a k8s event.
func (p *Publisher) onNack(event NackEvent) {
	nackID := ComputeNackID(event.Gateway.Namespace+"/"+event.Gateway.Name, event.TypeUrl)

	gatewayRef := &corev1.ObjectReference{
		Kind:       wellknown.GatewayKind,
		APIVersion: wellknown.GatewayGVK.GroupVersion().String(),
		Name:       event.Gateway.Name,
		Namespace:  event.Gateway.Namespace,
	}

	p.eventRecorder.AnnotatedEventf(
		gatewayRef,
		map[string]string{
			AnnotationNackID:     nackID,
			AnnotationTypeURL:    event.TypeUrl,
			AnnotationObservedAt: event.Timestamp.Format(time.RFC3339),
		},
		corev1.EventTypeWarning,
		ReasonNack,
		event.ErrorMsg,
	)

	log.Debug("published NACK event for Gateway", "gateway", event.Gateway, "nackID", nackID, "typeURL", event.TypeUrl)
}

// onAck publishes an ACK event as a k8s event.
func (p *Publisher) onAck(event AckEvent) {
	nackID := ComputeNackID(event.Gateway.Namespace+"/"+event.Gateway.Name, event.TypeUrl)

	gatewayRef := &corev1.ObjectReference{
		Kind:       wellknown.GatewayKind,
		APIVersion: wellknown.GatewayGVK.GroupVersion().String(),
		Name:       event.Gateway.Name,
		Namespace:  event.Gateway.Namespace,
	}

	p.eventRecorder.AnnotatedEventf(
		gatewayRef,
		map[string]string{
			AnnotationNackID:     nackID,
			AnnotationTypeURL:    event.TypeUrl,
			AnnotationRecoveryOf: nackID,
			AnnotationObservedAt: event.Timestamp.Format(time.RFC3339),
		},
		corev1.EventTypeNormal,
		ReasonAck,
		"Configuration accepted successfully",
	)

	log.Debug("published ACK event for Gateway", "gateway", event.Gateway, "typeURL", event.TypeUrl)
}
