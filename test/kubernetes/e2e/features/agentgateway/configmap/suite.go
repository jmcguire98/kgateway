//go:build e2e

package configmap

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kgateway-dev/kgateway/v2/pkg/utils/fsutils"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/kubeutils"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/requestutils/curl"
	testmatchers "github.com/kgateway-dev/kgateway/v2/test/gomega/matchers"
	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e"
	testdefaults "github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e/defaults"
	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e/tests/base"
)

var _ e2e.NewSuiteFunc = NewTestingSuite

var (
	setupManifest                 = filepath.Join(fsutils.MustGetThisDir(), "testdata", "setup.yaml")
	basicConfigMapManifest        = filepath.Join(fsutils.MustGetThisDir(), "testdata", "basic-configmap.yaml")
	tracingConfigMapManifest      = filepath.Join(fsutils.MustGetThisDir(), "testdata", "tracing-configmap.yaml")
	customFieldsConfigMapManifest = filepath.Join(fsutils.MustGetThisDir(), "testdata", "custom-fields-configmap.yaml")

	gatewayObjectMeta = metav1.ObjectMeta{
		Name:      "configmap-gateway",
		Namespace: "default",
	}

	agentGatewayDeploymentMeta = metav1.ObjectMeta{
		Name:      "configmap-gateway",
		Namespace: "default",
	}

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
			testdefaults.HttpbinManifest,
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
// and can successfully route requests to httpbin
func (s *testingSuite) TestCustomFieldsConfigMap() {
	s.T().Log("Testing custom fields ConfigMap configuration with end-to-end routing")

	s.waitForAgentgatewayPodsRunning()
	s.verifyConfigMapMountedInDeployment("custom-fields-agent-gateway-config")

	// Wait for httpbin to be ready
	s.TestInstallation.Assertions.EventuallyPodsRunning(
		s.T().Context(),
		"default",
		metav1.ListOptions{LabelSelector: testdefaults.HttpbinLabelSelector},
		60*time.Second,
	)

	// Test end-to-end functionality by making an HTTP request through the gateway to httpbin
	s.TestInstallation.Assertions.AssertEventualCurlResponse(
		s.T().Context(),
		testdefaults.CurlPodExecOpt,
		[]curl.Option{
			curl.WithHost(kubeutils.ServiceFQDN(gatewayObjectMeta)),
			curl.WithPort(8080),
			curl.WithPath("/get"),
			curl.WithHeader("x-user-id", "test-user-123"),
		},
		&testmatchers.HttpResponse{
			StatusCode: http.StatusOK,
		},
		30*time.Second,
	)
}
