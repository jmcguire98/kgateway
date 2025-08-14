package directresponse

import (
	"testing"

	"github.com/agentgateway/agentgateway/go/api"
	"github.com/stretchr/testify/require"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
	pluginsdkir "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
)

func TestDirectResponseAgentGWPass_ApplyForRoute(t *testing.T) {
	pol := &directResponse{spec: v1alpha1.DirectResponseSpec{StatusCode: 203}}
	pass := NewAgentGatewayPass(nil)
	out := &api.Route{}
	err := pass.ApplyForRoute(&pluginsdkir.RouteContext{Policy: pol}, out)
	require.NoError(t, err)
	require.Len(t, out.Filters, 1)
	require.NotNil(t, out.Filters[0].GetDirectResponse())
	require.EqualValues(t, 203, out.Filters[0].GetDirectResponse().Status)
}
