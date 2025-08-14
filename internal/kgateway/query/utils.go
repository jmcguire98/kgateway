package query

import (
	"errors"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/krtcollections"
	reports "github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/reporter"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/kubeutils"
)

func ProcessBackendError(err error, reporter reports.ParentRefReporter) {
	cond := formatBackendErrorCondition(err)
	reporter.SetCondition(cond)
}

// formatBackendErrorCondition normalizes backend resolution errors into a single
// RouteCondition shape to ensure parity across HTTP and GRPC translation paths.
func formatBackendErrorCondition(err error) reports.RouteCondition {
	// Normalize NotFound error messaging first
	var nf *krtcollections.NotFoundError
	if errors.As(err, &nf) {
		msg := err.Error()
		if nf.NotFoundObj.Kind == "Service" {
			fqdn := kubeutils.GetServiceHostname(nf.NotFoundObj.Name, nf.NotFoundObj.Namespace)
			msg = "backend(" + fqdn + ") not found"
		}
		return reports.RouteCondition{
			Type:    gwv1.RouteConditionResolvedRefs,
			Status:  metav1.ConditionFalse,
			Reason:  gwv1.RouteReasonBackendNotFound,
			Message: msg,
		}
	}
	switch {
	case errors.Is(err, krtcollections.ErrUnknownBackendKind):
		msg := err.Error()
		// Remove wrapped sentinel suffix for user-facing message to match expected text
		msg = strings.TrimSuffix(msg, ": unknown backend kind")
		return reports.RouteCondition{
			Type:    gwv1.RouteConditionResolvedRefs,
			Status:  metav1.ConditionFalse,
			Reason:  gwv1.RouteReasonInvalidKind,
			Message: msg,
		}
	case errors.Is(err, krtcollections.ErrMissingReferenceGrant):
		return reports.RouteCondition{
			Type:    gwv1.RouteConditionResolvedRefs,
			Status:  metav1.ConditionFalse,
			Reason:  gwv1.RouteReasonRefNotPermitted,
			Message: err.Error(),
		}
	case errors.Is(err, ErrCyclicReference):
		return reports.RouteCondition{
			Type:    gwv1.RouteConditionResolvedRefs,
			Status:  metav1.ConditionFalse,
			Reason:  gwv1.RouteReasonRefNotPermitted,
			Message: err.Error(),
		}
	case errors.Is(err, ErrUnresolvedReference):
		return reports.RouteCondition{
			Type:    gwv1.RouteConditionResolvedRefs,
			Status:  metav1.ConditionFalse,
			Reason:  gwv1.RouteReasonBackendNotFound,
			Message: err.Error(),
		}
	case apierrors.IsNotFound(err):
		return reports.RouteCondition{
			Type:    gwv1.RouteConditionResolvedRefs,
			Status:  metav1.ConditionFalse,
			Reason:  gwv1.RouteReasonBackendNotFound,
			Message: err.Error(),
		}
	default:
		return reports.RouteCondition{
			Type:    gwv1.RouteConditionResolvedRefs,
			Status:  metav1.ConditionFalse,
			Reason:  gwv1.RouteReasonBackendNotFound,
			Message: err.Error(),
		}
	}
}
