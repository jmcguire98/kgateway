//go:build e2e

package configmap

import (
	"context"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kgateway-dev/kgateway/v2/pkg/utils/fsutils"
	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e"
	testdefaults "github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e/defaults"
	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e/tests/base"
)

var _ e2e.NewSuiteFunc = NewTestingSuite

var (
	// manifests
	setupManifest                 = filepath.Join(fsutils.MustGetThisDir(), "testdata", "setup.yaml")
	basicConfigMapManifest        = filepath.Join(fsutils.MustGetThisDir(), "testdata", "basic-configmap.yaml")
	tracingConfigMapManifest      = filepath.Join(fsutils.MustGetThisDir(), "testdata", "tracing-configmap.yaml")
	customFieldsConfigMapManifest = filepath.Join(fsutils.MustGetThisDir(), "testdata", "custom-fields-configmap.yaml")

	// objects
	gatewayObjectMeta = metav1.ObjectMeta{
		Name:      "configmap-gateway",
		Namespace: "default",
	}

	agentGatewayDeploymentMeta = metav1.ObjectMeta{
		Name:      "configmap-gateway",
		Namespace: "default",
	}

	// test cases
	setup = base.TestCase{
		Manifests: []string{
			testdefaults.CurlPodManifest,
		},
		ManifestsWithTransform: map[string]func(string) string{
			setupManifest: func(original string) string {
				return original
			},
		},
	}

	basicConfigMapTest = base.TestCase{
		Manifests: []string{
			basicConfigMapManifest,
		},
	}

	tracingConfigMapTest = base.TestCase{
		Manifests: []string{
			tracingConfigMapManifest,
		},
	}

	customFieldsConfigMapTest = base.TestCase{
		Manifests: []string{
			customFieldsConfigMapManifest,
		},
	}

	testCases = map[string]*base.TestCase{
		"TestBasicConfigMap":        &basicConfigMapTest,
		"TestTracingConfigMap":      &tracingConfigMapTest,
		"TestCustomFieldsConfigMap": &customFieldsConfigMapTest,
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
		metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=configmap-gateway"},
		60*time.Second,
	)
}

// verifyConfigMapMountedInDeployment is a helper function that verifies a specific ConfigMap
// is mounted as config-volume in the agentgateway deployment
func (s *testingSuite) verifyConfigMapMountedInDeployment(expectedConfigMapName string) {
	deploymentObj := &appsv1.Deployment{}
	err := s.TestInstallation.ClusterContext.Client.Get(
		s.T().Context(),
		client.ObjectKey{
			Namespace: agentGatewayDeploymentMeta.Namespace,
			Name:      agentGatewayDeploymentMeta.Name,
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

// TestBasicConfigMap tests that agentgateway can start with a basic custom ConfigMap
func (s *testingSuite) TestBasicConfigMap() {
	s.T().Log("Testing basic ConfigMap configuration")

	s.waitForAgentgatewayPodsRunning()
	s.verifyConfigMapMountedInDeployment("basic-agent-gateway-config")
}

// TestTracingConfigMap tests that agentgateway properly applies tracing configuration from ConfigMap
func (s *testingSuite) TestTracingConfigMap() {
	s.T().Log("Testing tracing ConfigMap configuration")

	s.waitForAgentgatewayPodsRunning()
	s.verifyConfigMapMountedInDeployment("tracing-agent-gateway-config")
}

// TestCustomFieldsConfigMap tests that agentgateway applies custom field configuration from ConfigMap
func (s *testingSuite) TestCustomFieldsConfigMap() {
	s.T().Log("Testing custom fields ConfigMap configuration")

	s.waitForAgentgatewayPodsRunning()
	s.verifyConfigMapMountedInDeployment("custom-fields-agent-gateway-config")

	// Verify the ConfigMap exists and has the expected content
	configMapObj := &corev1.ConfigMap{}
	err := s.TestInstallation.ClusterContext.Client.Get(
		s.T().Context(),
		client.ObjectKey{
			Namespace: "default",
			Name:      "custom-fields-agent-gateway-config",
		},
		configMapObj,
	)
	s.Require().NoError(err)
	s.Require().Contains(configMapObj.Data, "config.yaml", "ConfigMap should contain config.yaml key")
	s.Require().Contains(configMapObj.Data["config.yaml"], "gen_ai.operation.name", "ConfigMap should contain custom trace fields")
}
