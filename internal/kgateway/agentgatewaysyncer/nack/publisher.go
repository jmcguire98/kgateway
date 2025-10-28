package nack

import (
	"context"
	"time"

	"istio.io/istio/pkg/kube"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/agentgatewaysyncer/krtxds"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/logging"
)

var log = logging.New("nack/publisher")

// Publisher converts NACK events from the agentgateway xDS server into Kubernetes Events.
type Publisher struct {
	ctx    context.Context
	client kube.Client
}

// NewPublisher creates a new NACK event publisher that will publish Events
// using the provided Kubernetes client.
func NewPublisher(ctx context.Context, client kube.Client) *Publisher {
	return &Publisher{
		client: client,
		ctx:    ctx,
	}
}

// OnNack implements krtxds.NackEventHandler.
// It converts a NACK event from the xDS server into a Kubernetes Event
// scoped to the affected Gateway resource.
func (p *Publisher) OnNack(event krtxds.NackEvent) {

	// TODO: check if version / code is available from the event and if not remove the params from ComputeNackID
	nackID := ComputeNackID(event.Gateway.Namespace+"/"+event.Gateway.Name, event.TypeUrl, "", "", event.ErrorMsg)

	k8sEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "agentgateway-nack-",
			Namespace:    event.Gateway.Namespace,
			Annotations: map[string]string{
				AnnotationNackID:     nackID,
				AnnotationTypeURL:    event.TypeUrl,
				AnnotationObservedAt: event.Timestamp.Format(time.RFC3339),
				// TODO: Add (or remove) more annotations when I check what comes out of nacks again
				// AnnotationResourceNames: event.ResourceNames,
				// AnnotationVersion: event.Version,
			},
		},
		// TODO: verify that this corresponds to 'regarding' in the event api
		InvolvedObject: corev1.ObjectReference{
			Kind:       wellknown.GatewayKind,
			APIVersion: wellknown.GatewayGVK.GroupVersion().String(),
			Name:       event.Gateway.Name,
			Namespace:  event.Gateway.Namespace,
		},
		Reason:              ReasonNack,
		Message:             event.ErrorMsg,
		Type:                corev1.EventTypeWarning,
		FirstTimestamp:      metav1.NewTime(event.Timestamp),
		LastTimestamp:       metav1.NewTime(event.Timestamp),
		Count:               1,
		ReportingController: wellknown.DefaultAgwControllerName,
	}

	// Publish Event to Kubernetes API
	_, err := p.client.Kube().CoreV1().Events(event.Gateway.Namespace).Create(
		p.ctx, k8sEvent, metav1.CreateOptions{},
	)
	if err != nil {
		// Log error but don't fail NACK handling - Event publishing is best-effort
		log.Error("Failed to publish NACK event for Gateway", "gateway", event.Gateway, "error", err)
		return
	}

	log.Debug("Published NACK event for Gateway", "gateway", event.Gateway, "nackID", nackID, "typeURL", event.TypeUrl)
}
