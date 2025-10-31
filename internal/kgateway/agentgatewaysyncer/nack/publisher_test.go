package nack

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"istio.io/istio/pkg/kube"
	"k8s.io/client-go/tools/record"
)

var (
	testNackEvent = NackEvent{
		Gateway:   testGateway,
		TypeUrl:   testTypeURL,
		ErrorMsg:  testErrorMessage,
		Timestamp: time.Now(),
	}
	testAckEvent = AckEvent{
		Gateway:   testGateway,
		TypeUrl:   testTypeURL,
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

	publisher.onNack(testNackEvent)

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

func TestPublisher_OnAck(t *testing.T) {
	client := kube.NewFakeClient()
	publisher := newPublisher(client)

	fakeRecorder := record.NewFakeRecorder(10)
	publisher.eventRecorder = fakeRecorder

	// Call onAck
	publisher.onAck(testAckEvent)

	// Verify event was recorded
	select {
	case event := <-fakeRecorder.Events:
		assert.Contains(t, event, "Normal")
		assert.Contains(t, event, ReasonAck)
		assert.Contains(t, event, "Configuration accepted successfully")
	default:
		t.Fatal("Expected event to be recorded but none was found")
	}
}

func TestPublisher_ComputeNackID(t *testing.T) {
	gateway1 := "default/test-gw"
	gateway2 := "kube-system/other-gw"
	typeURL1 := testTypeURL
	typeURL2 := "type.googleapis.com/agentgateway.dev.workload.Address"

	// Test that same inputs produce same ID
	id1a := ComputeNackID(gateway1, typeURL1)
	id1b := ComputeNackID(gateway1, typeURL1)
	assert.Equal(t, id1a, id1b)

	// Test that different gateways produce different IDs
	id2 := ComputeNackID(gateway2, typeURL1)
	assert.NotEqual(t, id1a, id2)

	// Test that different type URLs produce different IDs
	id3 := ComputeNackID(gateway1, typeURL2)
	assert.NotEqual(t, id1a, id3)

	assert.NotEmpty(t, id1a)
	assert.NotEmpty(t, id2)
	assert.NotEmpty(t, id3)
}
