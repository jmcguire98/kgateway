package nack

import (
	"fmt"
	"sort"
	"strings"

	"istio.io/istio/pkg/kube"
	corev1 "k8s.io/api/core/v1"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/logging"
	"github.com/kgateway-dev/kgateway/v2/pkg/schemes"
)

var log = logging.New("nack/publisher")

// Event reasons for Kubernetes Events created by agentgateway NACK detection
const (
	ReasonNack = "AgentGatewayNackError"
)

// composeNackMessage appends a summarized list of involved resource names to the base error message,
// ensuring the final string remains under a safe size and is readable.
func composeNackMessage(base string, resourceNames []string) string {
	const (
		maxNamesToShow   = 20
		maxMessageLength = 512
	)
	if len(resourceNames) == 0 {
		return base
	}
	// Deduplicate and sort
	uniq := map[string]struct{}{}
	for _, n := range resourceNames {
		if n == "" {
			continue
		}
		uniq[n] = struct{}{}
	}
	all := make([]string, 0, len(uniq))
	for n := range uniq {
		all = append(all, n)
	}
	sort.Strings(all)
	if len(all) == 0 {
		return base
	}
	if len(all) > maxNamesToShow {
		all = all[:maxNamesToShow]
	}
	list := strings.Join(all, ",")
	suffix := fmt.Sprintf(" resources=[%s]", list)
	msg := base
	truncated := false
	if len(msg)+len(suffix) > maxMessageLength {
		allowed := maxMessageLength - len(msg) - len(" resources=[]")
		if allowed < 0 {
			allowed = 0
		}
		if allowed < len(list) {
			list = list[:allowed]
			truncated = true
			suffix = fmt.Sprintf(" resources=[%s]", list)
		}
	}
	if truncated {
		return msg + suffix + " (truncated)"
	}
	return msg + suffix
}

// Publisher converts NACK events from the agentgateway xDS server into Kubernetes Events.
type Publisher struct {
	client        kube.Client
	eventRecorder record.EventRecorder
}

// newPublisher creates a new NACK event publisher that will publish k8s events
func newPublisher(client kube.Client) *Publisher {
	eventBroadcaster := record.NewBroadcaster()
	eventRecorder := eventBroadcaster.NewRecorder(
		schemes.DefaultScheme(),
		corev1.EventSource{Component: wellknown.DefaultAgwControllerName},
	)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: client.Kube().CoreV1().Events(""),
	})

	return &Publisher{
		client:        client,
		eventRecorder: eventRecorder,
	}
}

// onNack publishes a NACK event as a k8s event.
func (p *Publisher) onNack(event NackEvent, resourceNames []string) {
	gatewayRef := &corev1.ObjectReference{
		Kind:       wellknown.GatewayKind,
		APIVersion: wellknown.GatewayGVK.GroupVersion().String(),
		Name:       event.Gateway.Name,
		Namespace:  event.Gateway.Namespace,
	}

	// enrich error message with resource names
	msg := composeNackMessage(event.ErrorMsg, resourceNames)
	p.eventRecorder.Eventf(gatewayRef, corev1.EventTypeWarning, ReasonNack, msg)

	log.Debug("published NACK event for Gateway", "gateway", event.Gateway, "typeURL", event.TypeUrl)
}
