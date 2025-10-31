package nack

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"istio.io/istio/pkg/kube"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var (
	testGateway      = types.NamespacedName{Name: "test-gw", Namespace: "default"}
	testNamespace    = "default"
	testTypeURL      = "type.googleapis.com/agentgateway.dev.resource.Resource"
	testErrorMessage = "test error"
)

func TestNewNackHandler(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	assert.NotNil(t, handler)
	assert.NotNil(t, handler.nackStateStore)
	assert.NotNil(t, handler.nackPublisher)
}

func TestNackHandler_HandleNack(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	nackEvent := &NackEvent{
		Gateway:   testGateway,
		TypeUrl:   testTypeURL,
		ErrorMsg:  testErrorMessage,
		Timestamp: time.Now(),
	}

	// We will test if the actual event publishing works in the publisher test where we can use a fake recorder
	assert.NotPanics(t, func() {
		handler.HandleNack(nackEvent)
	})
}

func TestNackHandler_HandleAck(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	ackEvent := &AckEvent{
		Gateway:   testGateway,
		TypeUrl:   testTypeURL,
		Timestamp: time.Now(),
	}

	// We will test if the actual event publishing works in the publisher test where we can use a fake recorder
	assert.NotPanics(t, func() {
		handler.HandleAck(ackEvent)
	})
}

func TestNackHandler_StateManagement(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	gateway := testGateway
	nackID1 := "nack1"
	nackID2 := "nack2"
	message1 := "error message 1"
	message2 := "error message 2"

	// Test adding first NACK
	handler.addNack(gateway, nackID1, message1)
	assert.Len(t, handler.nackStateStore[gateway], 1)
	assert.Equal(t, message1, handler.nackStateStore[gateway][nackID1])

	// Test adding second NACK to same gateway
	handler.addNack(gateway, nackID2, message2)
	assert.Len(t, handler.nackStateStore[gateway], 2)
	assert.Equal(t, message1, handler.nackStateStore[gateway][nackID1])
	assert.Equal(t, message2, handler.nackStateStore[gateway][nackID2])

	// Test removing one NACK
	handler.removeNack(gateway, nackID1)
	assert.Len(t, handler.nackStateStore[gateway], 1)
	assert.Equal(t, message2, handler.nackStateStore[gateway][nackID2])
	_, exists := handler.nackStateStore[gateway][nackID1]
	assert.False(t, exists)

	// Test removing last NACK cleans up gateway entry
	handler.removeNack(gateway, nackID2)
	_, exists = handler.nackStateStore[gateway]
	assert.False(t, exists, "Gateway entry should be removed when no NACKs remain")
}

func TestNackHandler_StateManagement_NonExistentGateway(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	gateway := types.NamespacedName{Name: "non-existent", Namespace: testNamespace}

	// Test removing from non-existent gateway should not panic
	assert.NotPanics(t, func() {
		handler.removeNack(gateway, "non-existent-nack")
	})
}

func TestNackHandler_ComputeStatus_NoNacks(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	gateway := testGateway

	condition := handler.ComputeStatus(gateway)
	assert.Nil(t, condition, "Should return nil when no NACKs exist")
}

func TestNackHandler_ComputeStatus_SingleNack(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	gateway := testGateway
	nackID := "test-nack"
	errorMsg := "configuration error"

	handler.addNack(gateway, nackID, errorMsg)

	condition := handler.ComputeStatus(gateway)
	require.NotNil(t, condition)
	assert.Equal(t, string(gwv1.GatewayConditionProgrammed), condition.Type)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Equal(t, string(gwv1.GatewayReasonInvalid), condition.Reason)
	assert.Contains(t, condition.Message, "Configuration rejected")
	assert.Contains(t, condition.Message, errorMsg)
	assert.NotZero(t, condition.LastTransitionTime)
}

func TestNackHandler_ComputeStatus_MultipleNacks(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	gateway := testGateway

	// Add multiple NACKs
	handler.addNack(gateway, "nack1", "error 1")
	handler.addNack(gateway, "nack2", "error 2")
	handler.addNack(gateway, "nack3", "error 3")

	condition := handler.ComputeStatus(gateway)
	require.NotNil(t, condition)
	assert.Equal(t, string(gwv1.GatewayConditionProgrammed), condition.Type)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Equal(t, string(gwv1.GatewayReasonInvalid), condition.Reason)
	assert.Contains(t, condition.Message, "Configuration rejected")
	assert.Contains(t, condition.Message, "3 errors found")
}

func TestNackHandler_FilterEventsAndUpdateState_IgnoreNonNackAck(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	// Event with different reason should be ignored
	event := &corev1.Event{
		Reason: "SomeOtherReason",
		InvolvedObject: corev1.ObjectReference{
			Name:      testGateway.Name,
			Namespace: testGateway.Namespace,
		},
	}

	err := handler.FilterEventsAndUpdateState(event)
	assert.NoError(t, err)
	assert.Empty(t, handler.nackStateStore)
}

func TestNackHandler_FilterEventsAndUpdateState_NackEvent(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	nackID := "test-nack-id"
	errorMsg := "test error message"

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationNackID:  nackID,
				AnnotationTypeURL: testTypeURL,
			},
		},
		Reason:  ReasonNack,
		Message: errorMsg,
		InvolvedObject: corev1.ObjectReference{
			Name:      testGateway.Name,
			Namespace: testGateway.Namespace,
		},
	}

	err := handler.FilterEventsAndUpdateState(event)
	assert.NoError(t, err)

	gateway := testGateway
	assert.Len(t, handler.nackStateStore[gateway], 1)
	assert.Equal(t, errorMsg, handler.nackStateStore[gateway][nackID])
}

func TestNackHandler_FilterEventsAndUpdateState_AckEvent(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	gateway := testGateway
	nackID := "test-nack-id"

	// First add a NACK
	handler.addNack(gateway, nackID, "test error")
	assert.Len(t, handler.nackStateStore[gateway], 1)

	// Now process an ACK event that recovers this NACK
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationNackID:     nackID,
				AnnotationRecoveryOf: nackID,
				AnnotationTypeURL:    testTypeURL,
			},
		},
		Reason: ReasonAck,
		InvolvedObject: corev1.ObjectReference{
			Name:      testGateway.Name,
			Namespace: testGateway.Namespace,
		},
	}

	err := handler.FilterEventsAndUpdateState(event)
	assert.NoError(t, err)

	// NACK should be removed and gateway cleaned up
	_, exists := handler.nackStateStore[gateway]
	assert.False(t, exists, "Gateway should be cleaned up after last NACK is resolved")
}

func TestNackHandler_FilterEventsAndUpdateState_MissingAnnotations(t *testing.T) {
	handler := NewNackHandler(kube.NewFakeClient())

	tests := []struct {
		name        string
		reason      string
		annotations map[string]string
		expectError bool
	}{
		{
			name:   "NACK missing NackID",
			reason: ReasonNack,
			annotations: map[string]string{
				AnnotationTypeURL: "type.googleapis.com/test",
			},
			expectError: true,
		},
		{
			name:        "NACK missing all annotations",
			reason:      ReasonNack,
			annotations: map[string]string{},
			expectError: true,
		},
		{
			name:   "ACK missing RecoveryOf",
			reason: ReasonAck,
			annotations: map[string]string{
				AnnotationNackID:  "test-id",
				AnnotationTypeURL: "type.googleapis.com/test",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
				Reason: tt.reason,
				InvolvedObject: corev1.ObjectReference{
					Name:      testGateway.Name,
					Namespace: testGateway.Namespace,
				},
			}

			err := handler.FilterEventsAndUpdateState(event)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
