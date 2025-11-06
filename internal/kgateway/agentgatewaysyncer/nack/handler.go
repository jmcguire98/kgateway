package nack

import (
	"time"

	"istio.io/istio/pkg/kube"
	"k8s.io/apimachinery/pkg/types"
)

type NackEventPublisher struct {
	nackPublisher *Publisher
}

// NackEvent represents a NACK received from an agentgateway gateway
type NackEvent struct {
	Gateway   types.NamespacedName
	TypeUrl   string
	ErrorMsg  string
	Timestamp time.Time
}

func NewNackEventPublisher(client kube.Client) *NackEventPublisher {
	return &NackEventPublisher{
		nackPublisher: newPublisher(client),
	}
}

// PublishNack publishes a NACK event to the Kubernetes Event API.
func (h *NackEventPublisher) PublishNack(nackEvent *NackEvent) {
	h.nackPublisher.onNack(*nackEvent)
}
