//go:build e2e

package configmap

import (
	"context"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kgateway-dev/kgateway/v2/pkg/utils/fsutils"
	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e"
	testdefaults "github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e/defaults"
	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e/tests/base"
)

var _ e2e.NewSuiteFunc = NewTestingSuite

var (
	setupManifest            = filepath.Join(fsutils.MustGetThisDir(), "testdata", "setup.yaml")
	tracingConfigMapManifest = filepath.Join(fsutils.MustGetThisDir(), "testdata", "tracing-configmap.yaml")

	tracingGatewayObjectMeta = metav1.ObjectMeta{
		Name:      "agent-gateway",
		Namespace: "default",
	}

	tracingAgentGatewayDeploymentMeta = metav1.ObjectMeta{
		Name:      "agent-gateway",
		Namespace: "default",
	}

	setup = base.TestCase{
		Manifests: []string{
			testdefaults.CurlPodManifest,
		},
	}

	tracingConfigMapTest = base.TestCase{
		Manifests: []string{
			tracingConfigMapManifest,
		},
	}

	testCases = map[string]*base.TestCase{
		"TestTracingConfigMap": &tracingConfigMapTest,
	}
)

// testingSuite is a suite of agentgateway configmap tests
type testingSuite struct {
	*base.BaseTestingSuite
}

func NewTestingSuite(ctx context.Context, testInst *e2e.TestInstallation) suite.TestingSuite {
	return &testingSuite{
		base.NewBaseTestingSuite(ctx, testInst, setup, testCases),
	}
}

// waitForAgentgatewayPodsRunning waits for the agentgateway pods to be running
func (s *testingSuite) waitForAgentgatewayPodsRunning() {
	s.TestInstallation.Assertions.EventuallyPodsRunning(
		s.T().Context(),
		"default",
		metav1.ListOptions{LabelSelector: "app.kubernetes.io/component=agentgateway"},
		60*time.Second,
	)
}

// verifyConfigMapMountedInDeployment is a helper function that verifies a specific ConfigMap
// is mounted as config-volume in the agentgateway deployment
func (s *testingSuite) verifyConfigMapMountedInDeployment(expectedConfigMapName string, deploymentMeta metav1.ObjectMeta) {
	deploymentObj := &appsv1.Deployment{}
	err := s.TestInstallation.ClusterContext.Client.Get(
		s.T().Context(),
		client.ObjectKey{
			Namespace: deploymentMeta.Namespace,
			Name:      deploymentMeta.Name,
		},
		deploymentObj,
	)
	s.Require().NoError(err)

	// Check that the expected ConfigMap is referenced in the deployment
	found := false
	for _, volume := range deploymentObj.Spec.Template.Spec.Volumes {
		if volume.Name == "config-volume" && volume.ConfigMap != nil {
			if volume.ConfigMap.Name == expectedConfigMapName {
				found = true
				break
			}
		}
	}
	s.Require().True(found, "ConfigMap %s should be mounted as config-volume", expectedConfigMapName)
}

// verifyTracingConfigurationActive checks that the tracing configuration from ConfigMap is actually active
func (s *testingSuite) verifyTracingConfigurationActive(deploymentMeta metav1.ObjectMeta) {
	// Get pods for this deployment using label selector
	pods, err := s.TestInstallation.Actions.Kubectl().GetPodsInNsWithLabel(
		s.T().Context(),
		deploymentMeta.Namespace,
		"app.kubernetes.io/component=agentgateway",
	)
	s.Require().NoError(err)
	s.Require().NotEmpty(pods, "No agentgateway pods found")

	// Use EventuallyWithT for retry logic when checking logs
	s.Require().EventuallyWithT(func(c *assert.CollectT) {
		logs, err := s.TestInstallation.Actions.Kubectl().GetContainerLogs(
			s.T().Context(),
			deploymentMeta.Namespace,
			pods[0], // Use first pod
		)
		assert.NoError(c, err, "Failed to get pod logs")

		// Verify the tracing endpoint from the ConfigMap is present in logs
		expectedEndpoint := "http://jaeger-collector.observability.svc.cluster.local:4317"
		assert.Contains(c, logs, expectedEndpoint,
			"Tracing endpoint %s from ConfigMap should be present in pod logs", expectedEndpoint)

		// Verify tracing initialization message
		assert.Contains(c, logs, "initializing tracer",
			"Tracing initialization should be present in pod logs")
	}, 60*time.Second, 5*time.Second, "should find tracing configuration in pod logs")

	s.T().Logf("Successfully verified tracing configuration is active")
}

// TestTracingConfigMap tests that agentgateway properly applies tracing configuration from ConfigMap
func (s *testingSuite) TestTracingConfigMap() {
	s.T().Log("Testing tracing ConfigMap configuration")

	s.waitForAgentgatewayPodsRunning()
	s.verifyConfigMapMountedInDeployment("agent-gateway-config", tracingAgentGatewayDeploymentMeta)

	// Verify that the tracing configuration is actually loaded and active
	s.verifyTracingConfigurationActive(tracingAgentGatewayDeploymentMeta)
}

