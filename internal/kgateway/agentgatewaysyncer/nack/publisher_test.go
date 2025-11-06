package nack

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"istio.io/istio/pkg/kube"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
)

var (
	testGateway      = types.NamespacedName{Name: "test-gw", Namespace: "default"}
	testTypeURL      = "type.googleapis.com/agentgateway.dev.resource.Resource"
	testErrorMessage = "test error"
	testNackEvent    = NackEvent{
		Gateway:   testGateway,
		TypeUrl:   testTypeURL,
		ErrorMsg:  testErrorMessage,
		Timestamp: time.Now(),
	}
)

func TestNewPublisher(t *testing.T) {
	client := kube.NewFakeClient()
	publisher := newPublisher(client)

	assert.NotNil(t, publisher)
	assert.NotNil(t, publisher.client)
	assert.NotNil(t, publisher.eventRecorder)
}

func TestPublisher_OnNack(t *testing.T) {
	client := kube.NewFakeClient()
	publisher := newPublisher(client)

	fakeRecorder := record.NewFakeRecorder(10)
	publisher.eventRecorder = fakeRecorder

	publisher.onNack(context.TODO(), testNackEvent)

	// Verify event was recorded
	select {
	case event := <-fakeRecorder.Events:
		assert.Contains(t, event, "Warning")
		assert.Contains(t, event, ReasonNack)
		assert.Contains(t, event, testErrorMessage)
	default:
		t.Fatal("Expected event to be recorded but none was found")
	}
}
