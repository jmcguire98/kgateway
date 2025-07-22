package agentgatewaysyncer

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/agentgateway/agentgateway/go/api"
	"github.com/avast/retry-go/v4"
	envoytypes "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	envoycache "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/proto"
	networkingclient "istio.io/client-go/pkg/apis/networking/v1"
	"istio.io/istio/pkg/config/schema/kubeclient"
	"istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/kube/controllers"
	"istio.io/istio/pkg/kube/kclient"
	"istio.io/istio/pkg/kube/krt"
	"istio.io/istio/pkg/kube/kubetypes"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	inf "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"
	"sigs.k8s.io/gateway-api-inference-extension/client-go/clientset/versioned"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
	gwxv1a1 "sigs.k8s.io/gateway-api/apisx/v1alpha1"

	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/extensions2/common"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/krtcollections"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/reports"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/utils"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/utils/krtutil"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/wellknown"
	"github.com/kgateway-dev/kgateway/v2/pkg/logging"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/reporter"
)

var logger = logging.New("agentgateway/syncer")

const (
	// Retry configuration constants
	maxRetryAttempts = 5
	retryDelay       = 100 * time.Millisecond

	// Resource name format strings
	resourceNameFormat = "%s~%s"
	bindKeyFormat      = "%s/%s"
	gatewayNameFormat  = "%s/%s"

	// Log message keys
	logKeyControllerName = "controllername"
	logKeyError          = "error"
	logKeyGateway        = "gateway"
	logKeyResourceRef    = "resource_ref"
	logKeyRouteType      = "route_type"
)

// AgentGwSyncer synchronizes Kubernetes Gateway API resources with xDS for agentgateway proxies.
// It watches Gateway resources with the agentgateway class and translates them to agentgateway configuration.
type AgentGwSyncer struct {
	// Core collections and dependencies
	commonCols *common.CommonCollections
	mgr        manager.Manager
	client     kube.Client
	plugins    pluginsdk.Plugin

	// Configuration
	controllerName        string
	agentGatewayClassName string
	domainSuffix          string
	systemNamespace       string
	clusterID             string

	// XDS and caching
	xDS                 krt.Collection[agentGwXdsResources]
	xdsCache            envoycache.SnapshotCache
	xdsSnapshotsMetrics krtcollections.CollectionMetricsRecorder

	// Status reporting
	statusReport krt.Singleton[report]

	// Synchronization
	waitForSync []cache.InformerSynced
	ready       atomic.Bool

	// features
	EnableAlphaGatewayAPI bool
}

// agentGwXdsResources represents XDS resources for a single agent gateway
type agentGwXdsResources struct {
	types.NamespacedName

	// Status reports for this gateway
	reports reports.ReportMap

	// Resources config for gateway (Bind, Listener, Route)
	ResourceConfig envoycache.Resources

	// Address config (Services, Workloads)
	AddressConfig envoycache.Resources
}

// ResourceName needs to match agentgateway role configured in client.rs (https://github.com/agentgateway/agentgateway/blob/main/crates/agentgateway/src/xds/client.rs)
func (r agentGwXdsResources) ResourceName() string {
	return fmt.Sprintf(resourceNameFormat, r.Namespace, r.Name)
}

func (r agentGwXdsResources) Equals(in agentGwXdsResources) bool {
	return r.NamespacedName == in.NamespacedName &&
		report{r.reports}.Equals(report{in.reports}) &&
		r.ResourceConfig.Version == in.ResourceConfig.Version &&
		r.AddressConfig.Version == in.AddressConfig.Version
}

func NewAgentGwSyncer(
	controllerName string,
	agentGatewayClassName string,
	client kube.Client,
	mgr manager.Manager,
	commonCols *common.CommonCollections,
	plugins pluginsdk.Plugin,
	xdsCache envoycache.SnapshotCache,
	domainSuffix string,
	systemNamespace string,
	clusterID string,
	enableAlphaGwAPIs bool,
) *AgentGwSyncer {
	// TODO: register types (auth, policy, etc.) if necessary
	return &AgentGwSyncer{
		commonCols:            commonCols,
		controllerName:        controllerName,
		agentGatewayClassName: agentGatewayClassName,
		plugins:               plugins,
		xdsCache:              xdsCache,
		client:                client,
		mgr:                   mgr,
		domainSuffix:          domainSuffix,
		systemNamespace:       systemNamespace,
		clusterID:             clusterID,
		xdsSnapshotsMetrics:   krtcollections.NewCollectionMetricsRecorder("AgentGatewayXDSSnapshots"),
		EnableAlphaGatewayAPI: enableAlphaGwAPIs,
	}
}

type envoyResourceWithCustomName struct {
	proto.Message
	Name    string
	version uint64
}

func (r envoyResourceWithCustomName) ResourceName() string {
	return r.Name
}

func (r envoyResourceWithCustomName) GetName() string {
	return r.Name
}

func (r envoyResourceWithCustomName) Equals(in envoyResourceWithCustomName) bool {
	return r.version == in.version
}

var _ envoytypes.ResourceWithName = envoyResourceWithCustomName{}

type report struct {
	// lower case so krt doesn't error in debug handler
	reportMap reports.ReportMap
}

func (r report) ResourceName() string {
	return "report"
}

func (r report) Equals(in report) bool {
	// Always return false to force status recalculation on every sync.
	// This is safe because status includes observedGeneration, so any edit to the Gateway resource
	// (e.g. changing the port) will eventually trigger a new status update.
	// We currently rely on the translation process (not the status diff) to trigger reconciliation,
	// so skipping deep equality here avoids brittle or unnecessary comparisons (e.g. map equality).
	// If we find a case where this causes redundant updates or missed transitions, we can revisit this.
	return false
}

// Inputs holds all the input collections needed for the syncer
type Inputs struct {
	// Core Kubernetes resources
	Namespaces krt.Collection[*corev1.Namespace]
	Services   krt.Collection[*corev1.Service]
	Secrets    krt.Collection[*corev1.Secret]

	// Gateway API resources
	GatewayClasses  krt.Collection[*gwv1.GatewayClass]
	Gateways        krt.Collection[*gwv1.Gateway]
	HTTPRoutes      krt.Collection[*gwv1.HTTPRoute]
	GRPCRoutes      krt.Collection[*gwv1.GRPCRoute]
	TCPRoutes       krt.Collection[*gwv1alpha2.TCPRoute]
	TLSRoutes       krt.Collection[*gwv1alpha2.TLSRoute]
	ReferenceGrants krt.Collection[*gwv1beta1.ReferenceGrant]

	// Extended resources
	ServiceEntries krt.Collection[*networkingclient.ServiceEntry]
	InferencePools krt.Collection[*inf.InferencePool]

	// kgateway resources
	Backends *krtcollections.BackendIndex
}

func (s *AgentGwSyncer) Init(krtopts krtutil.KrtOptions) {
	logger.Debug("init agentgateway Syncer", "controllername", s.controllerName)

	s.setupInferenceExtensionClient()
	inputs := s.buildInputCollections(krtopts)
	s.buildResourceCollections(inputs, krtopts)
}

func (s *AgentGwSyncer) setupInferenceExtensionClient() {
	// TODO: share this in a common spot with the inference extension plugin
	// Create the inference extension clientset.
	inferencePoolGVR := wellknown.InferencePoolGVK.GroupVersion().WithResource("inferencepools")
	infCli, err := versioned.NewForConfig(s.commonCols.Client.RESTConfig())
	if err != nil {
		logger.Error("failed to create inference extension client", "error", err)
	} else {
		kubeclient.Register[*inf.InferencePool](
			inferencePoolGVR,
			wellknown.InferencePoolGVK,
			func(c kubeclient.ClientGetter, namespace string, o metav1.ListOptions) (runtime.Object, error) {
				return infCli.InferenceV1alpha2().InferencePools(namespace).List(context.Background(), o)
			},
			func(c kubeclient.ClientGetter, namespace string, o metav1.ListOptions) (watch.Interface, error) {
				return infCli.InferenceV1alpha2().InferencePools(namespace).Watch(context.Background(), o)
			},
		)
	}
}

func (s *AgentGwSyncer) buildInputCollections(krtopts krtutil.KrtOptions) Inputs {
	inputs := Inputs{
		Namespaces: krt.NewInformer[*corev1.Namespace](s.client),
		Secrets: krt.WrapClient[*corev1.Secret](
			kclient.NewFiltered[*corev1.Secret](s.client, kubetypes.Filter{
				//FieldSelector: kubesecrets.SecretsFieldSelector,
				ObjectFilter: s.client.ObjectFilter(),
			}),
		),
		Services: krt.WrapClient[*corev1.Service](
			kclient.NewFiltered[*corev1.Service](s.client, kubetypes.Filter{ObjectFilter: s.client.ObjectFilter()}),
			krtopts.ToOptions("informer/Services")...),

		GatewayClasses: krt.WrapClient(kclient.NewFiltered[*gwv1.GatewayClass](s.client, kubetypes.Filter{ObjectFilter: s.client.ObjectFilter()}), krtopts.ToOptions("informer/GatewayClasses")...),
		Gateways:       krt.WrapClient(kclient.NewFiltered[*gwv1.Gateway](s.client, kubetypes.Filter{ObjectFilter: s.client.ObjectFilter()}), krtopts.ToOptions("informer/Gateways")...),
		HTTPRoutes:     krt.WrapClient(kclient.NewFiltered[*gwv1.HTTPRoute](s.client, kubetypes.Filter{ObjectFilter: s.client.ObjectFilter()}), krtopts.ToOptions("informer/HTTPRoutes")...),
		GRPCRoutes:     krt.WrapClient(kclient.NewFiltered[*gwv1.GRPCRoute](s.client, kubetypes.Filter{ObjectFilter: s.client.ObjectFilter()}), krtopts.ToOptions("informer/GRPCRoutes")...),

		ReferenceGrants: krt.WrapClient(kclient.NewFiltered[*gwv1beta1.ReferenceGrant](s.client, kubetypes.Filter{ObjectFilter: s.client.ObjectFilter()}), krtopts.ToOptions("informer/ReferenceGrants")...),
		//ServiceEntries:  krt.WrapClient(kclient.New[*networkingclient.ServiceEntry](s.client), krtopts.ToOptions("informer/ServiceEntries")...),
		InferencePools: krt.WrapClient(kclient.NewDelayedInformer[*inf.InferencePool](s.client, wellknown.InferencePoolGVK.GroupVersion().WithResource("inferencepools"), kubetypes.StandardInformer, kclient.Filter{ObjectFilter: s.commonCols.Client.ObjectFilter()}), krtopts.ToOptions("informer/InferencePools")...),

		// kgateway resources
		Backends: s.commonCols.BackendIndex,
	}

	if s.EnableAlphaGatewayAPI {
		logger.Debug("alpha gateway apis are enabled")
		inputs.TCPRoutes = krt.WrapClient(kclient.NewFiltered[*gwv1alpha2.TCPRoute](s.client, kubetypes.Filter{ObjectFilter: s.client.ObjectFilter()}), krtopts.ToOptions("informer/TCPRoutes")...)
		inputs.TLSRoutes = krt.WrapClient(kclient.NewFiltered[*gwv1alpha2.TLSRoute](s.client, kubetypes.Filter{ObjectFilter: s.client.ObjectFilter()}), krtopts.ToOptions("informer/TLSRoutes")...)
	} else {
		logger.Debug("alpha gateway apis are disabled")
		// If disabled, still build a collection but make it always empty
		inputs.TCPRoutes = krt.NewStaticCollection[*gwv1alpha2.TCPRoute](nil, krtopts.ToOptions("disable/TCPRoutes")...)
		inputs.TLSRoutes = krt.NewStaticCollection[*gwv1alpha2.TLSRoute](nil, krtopts.ToOptions("disable/TLSRoutes")...)
	}

	return inputs
}

func (s *AgentGwSyncer) buildResourceCollections(inputs Inputs, krtopts krtutil.KrtOptions) {
	rm := reports.NewReportMap()
	rep := reports.NewReporter(&rm)

	// Build core collections for irs
	gatewayClasses := GatewayClassesCollection(inputs.GatewayClasses, krtopts)
	refGrants := BuildReferenceGrants(ReferenceGrantsCollection(inputs.ReferenceGrants, krtopts))
	gateways := s.buildGatewayCollection(inputs, gatewayClasses, refGrants, krtopts, rep)

	// Build ADP resources
	adpResources := s.buildADPResources(gateways, inputs, refGrants, krtopts, rep, rm)

	// Build address collections
	addresses := s.buildAddressCollections(inputs, krtopts)

	// Build XDS collection
	s.buildXDSCollection(adpResources, addresses, krtopts, rm)

	// Build status reporting
	s.buildStatusReporting()

	// Set up sync dependencies
	s.setupSyncDependencies(gateways, adpResources, addresses, inputs)
}

func (s *AgentGwSyncer) buildGatewayCollection(
	inputs Inputs,
	gatewayClasses krt.Collection[GatewayClass],
	refGrants ReferenceGrants,
	krtopts krtutil.KrtOptions,
	rep reporter.Reporter,
) krt.Collection[Gateway] {
	return GatewayCollection(
		s.agentGatewayClassName,
		inputs.Gateways,
		gatewayClasses,
		inputs.Namespaces,
		refGrants,
		inputs.Secrets,
		s.domainSuffix,
		krtopts,
		rep,
	)
}

func (s *AgentGwSyncer) buildADPResources(
	gateways krt.Collection[Gateway],
	inputs Inputs,
	refGrants ReferenceGrants,
	krtopts krtutil.KrtOptions,
	rep reporter.Reporter,
	repMap reports.ReportMap,
) krt.Collection[ADPResource] {
	// Build ports and binds
	ports := krt.NewCollection(gateways, func(ctx krt.HandlerContext, obj Gateway) *IndexObject[string, Gateway] {
		port := fmt.Sprint(obj.parentInfo.Port)
		return &IndexObject[string, Gateway]{
			Key:     port,
			Objects: []Gateway{obj},
		}
	}, krtopts.ToOptions("ports")...)

	binds := krt.NewManyCollection(ports, func(ctx krt.HandlerContext, object IndexObject[string, Gateway]) []ADPResource {
		port, _ := strconv.Atoi(object.Key)
		uniq := sets.New[types.NamespacedName]()
		for _, gw := range object.Objects {
			uniq.Insert(types.NamespacedName{
				Namespace: gw.parent.Namespace,
				Name:      gw.parent.Name,
			})
		}
		var binds []ADPResource

		for _, obj := range uniq.UnsortedList() {
			bind := Bind{
				Bind: &api.Bind{
					Key:  object.Key + "/" + obj.String(),
					Port: uint32(port),
				},
			}
			binds = append(binds, toResourceWithReports(obj, bind, repMap))
		}
		return binds
	}, krtopts.ToOptions("Binds")...)

	// Build listeners
	listeners := krt.NewCollection(gateways, func(ctx krt.HandlerContext, obj Gateway) *ADPResource {
		return s.buildListenerFromGateway(ctx, obj, repMap)
	}, krtopts.ToOptions("Listeners")...)

	// Build routes
	routeParents := BuildRouteParents(gateways)
	routeInputs := RouteContextInputs{
		Grants:         refGrants,
		RouteParents:   routeParents,
		DomainSuffix:   s.domainSuffix,
		Services:       inputs.Services,
		Namespaces:     inputs.Namespaces,
		InferencePools: inputs.InferencePools,
		Backends:       s.commonCols.BackendIndex,
		Plugins:        s.plugins,
	}
	adpRoutes := ADPRouteCollection(inputs.HTTPRoutes, inputs.GRPCRoutes, inputs.TCPRoutes, inputs.TLSRoutes, gateways, inputs.Gateways, routeInputs, krtopts, repMap, rep, s.plugins)

	// Join all ADP resources
	allADPResources := krt.JoinCollection([]krt.Collection[ADPResource]{binds, listeners, adpRoutes}, krtopts.ToOptions("ADPResources")...)

	return allADPResources
}

// buildListenerFromGateway creates a listener resource from a gateway
func (s *AgentGwSyncer) buildListenerFromGateway(ctx krt.HandlerContext, obj Gateway, repMap reports.ReportMap) *ADPResource {
	l := &api.Listener{
		Key:         obj.ResourceName(),
		Name:        string(obj.parentInfo.SectionName),
		BindKey:     fmt.Sprint(obj.parentInfo.Port) + "/" + obj.parent.Namespace + "/" + obj.parent.Name,
		GatewayName: obj.parent.Namespace + "/" + obj.parent.Name,
		Hostname:    obj.parentInfo.OriginalHostname,
	}

	// Set protocol and TLS configuration
	protocol, tlsConfig, ok := s.getProtocolAndTLSConfig(obj)
	if !ok {
		return nil // Unsupported protocol or missing TLS config
	}

	l.Protocol = protocol
	l.Tls = tlsConfig

	return toResourcepWithReports(types.NamespacedName{
		Namespace: obj.parent.Namespace,
		Name:      obj.parent.Name,
	}, ADPListener{l}, repMap)
}

// getProtocolAndTLSConfig extracts protocol and TLS configuration from a gateway
func (s *AgentGwSyncer) getProtocolAndTLSConfig(obj Gateway) (api.Protocol, *api.TLSConfig, bool) {
	var tlsConfig *api.TLSConfig

	// Build TLS config if needed
	if obj.TLSInfo != nil {
		tlsConfig = &api.TLSConfig{
			Cert:       obj.TLSInfo.Cert,
			PrivateKey: obj.TLSInfo.Key,
		}
	}

	switch obj.parentInfo.Protocol {
	case gwv1.HTTPProtocolType:
		return api.Protocol_HTTP, nil, true
	case gwv1.HTTPSProtocolType:
		if tlsConfig == nil {
			return api.Protocol_HTTPS, nil, false // TLS required but not configured
		}
		return api.Protocol_HTTPS, tlsConfig, true
	case gwv1.TLSProtocolType:
		if tlsConfig == nil {
			return api.Protocol_TLS, nil, false // TLS required but not configured
		}
		return api.Protocol_TLS, tlsConfig, true
	case gwv1.TCPProtocolType:
		return api.Protocol_TCP, nil, true
	default:
		return api.Protocol_HTTP, nil, false // Unsupported protocol
	}
}

func (s *AgentGwSyncer) buildAddressCollections(inputs Inputs, krtopts krtutil.KrtOptions) krt.Collection[envoyResourceWithCustomName] {
	// Build endpoint slices and namespaces
	epSliceClient := kclient.NewFiltered[*discoveryv1.EndpointSlice](
		s.commonCols.Client,
		kclient.Filter{ObjectFilter: s.commonCols.Client.ObjectFilter()},
	)
	endpointSlices := krt.WrapClient(epSliceClient, s.commonCols.KrtOpts.ToOptions("informer/EndpointSlices")...)

	nsClient := kclient.NewFiltered[*corev1.Namespace](
		s.commonCols.Client,
		kclient.Filter{ObjectFilter: s.commonCols.Client.ObjectFilter()},
	)
	namespaces := krt.WrapClient(nsClient, s.commonCols.KrtOpts.ToOptions("informer/Namespaces")...)

	// Build workload index
	workloadIndex := index{
		namespaces:      s.commonCols.Namespaces,
		SystemNamespace: s.systemNamespace,
		ClusterID:       s.clusterID,
		DomainSuffix:    s.domainSuffix,
	}

	// Build service and workload collections
	workloadServices := workloadIndex.ServicesCollection(inputs.Services, nil, inputs.InferencePools, namespaces, krtopts)
	workloads := workloadIndex.WorkloadsCollection(
		s.commonCols.WrappedPods,
		workloadServices,
		nil, // serviceEntries,
		endpointSlices,
		namespaces,
		krtopts,
	)

	// Build address collections
	svcAddresses := krt.NewCollection(workloadServices, func(ctx krt.HandlerContext, obj ServiceInfo) *ADPCacheAddress {
		addrMessage := obj.AsAddress.Address
		resourceVersion := utils.HashProto(addrMessage)
		result := &ADPCacheAddress{
			NamespacedName:      types.NamespacedName{Name: obj.Service.GetName(), Namespace: obj.Service.GetNamespace()},
			Address:             addrMessage,
			AddressResourceName: obj.ResourceName(),
			AddressVersion:      resourceVersion,
		}
		logger.Debug("created XDS resources for svc address with ID", "addr", fmt.Sprintf("%s,%s", obj.Service.GetName(), obj.Service.GetNamespace()), "resourceid", result.ResourceName())
		return result
	})

	workloadAddresses := krt.NewCollection(workloads, func(ctx krt.HandlerContext, obj WorkloadInfo) *ADPCacheAddress {
		addrMessage := obj.AsAddress.Address
		resourceVersion := utils.HashProto(addrMessage)
		result := &ADPCacheAddress{
			NamespacedName:      types.NamespacedName{Name: obj.Workload.GetName(), Namespace: obj.Workload.GetNamespace()},
			Address:             addrMessage,
			AddressVersion:      resourceVersion,
			AddressResourceName: obj.ResourceName(),
		}
		logger.Debug("created XDS resources for workload address with ID", "addr", fmt.Sprintf("%s,%s", obj.Workload.GetName(), obj.Workload.GetNamespace()), "resourceid", result.ResourceName())
		return result
	})

	adpAddresses := krt.JoinCollection([]krt.Collection[ADPCacheAddress]{svcAddresses, workloadAddresses}, krtopts.ToOptions("ADPAddresses")...)
	return krt.NewCollection(adpAddresses, func(kctx krt.HandlerContext, obj ADPCacheAddress) *envoyResourceWithCustomName {
		return &envoyResourceWithCustomName{
			Message: obj.Address,
			Name:    obj.AddressResourceName,
			version: obj.AddressVersion,
		}
	}, krtopts.ToOptions("XDSAddresses")...)
}

func (s *AgentGwSyncer) buildXDSCollection(adpResources krt.Collection[ADPResource], xdsAddresses krt.Collection[envoyResourceWithCustomName], krtopts krtutil.KrtOptions, rm reports.ReportMap) {
	// Create an index on adpResources by Gateway to avoid fetching all resources
	adpResourcesByGateway := krt.NewIndex(adpResources, func(resource ADPResource) []types.NamespacedName {
		return []types.NamespacedName{resource.Gateway}
	})

	s.xDS = krt.NewCollection(adpResources, func(kctx krt.HandlerContext, obj ADPResource) *agentGwXdsResources {
		gwNamespacedName := obj.Gateway

		cacheAddresses := krt.Fetch(kctx, xdsAddresses)
		envoytypesAddresses := make([]envoytypes.Resource, 0, len(cacheAddresses))
		for _, addr := range cacheAddresses {
			envoytypesAddresses = append(envoytypesAddresses, addr)
		}

		var cacheResources []envoytypes.Resource
		// Use index to fetch only resources for this gateway instead of all resources
		resourceList := krt.Fetch(kctx, adpResources, krt.FilterIndex(adpResourcesByGateway, gwNamespacedName))
		for _, resource := range resourceList {
			cacheResources = append(cacheResources, &envoyResourceWithCustomName{
				Message: resource.Resource,
				Name:    resource.ResourceName(),
				version: utils.HashProto(resource.Resource),
			})
		}

		// Create the resource wrappers
		var resourceVersion uint64
		for _, res := range cacheResources {
			resourceVersion ^= res.(*envoyResourceWithCustomName).version
		}
		// Calculate address version
		var addrVersion uint64
		for _, res := range cacheAddresses {
			addrVersion ^= res.version
		}

		result := &agentGwXdsResources{
			NamespacedName: gwNamespacedName,
			reports:        rm,
			ResourceConfig: envoycache.NewResources(fmt.Sprintf("%d", resourceVersion), cacheResources),
			AddressConfig:  envoycache.NewResources(fmt.Sprintf("%d", addrVersion), envoytypesAddresses),
		}
		logger.Debug("created XDS resources for gateway with ID", "gwname", fmt.Sprintf("%s,%s", gwNamespacedName.Name, gwNamespacedName.Namespace), "resourceid", result.ResourceName())
		return result
	})
}

func (s *AgentGwSyncer) buildStatusReporting() {
	// as proxies are created, they also contain a reportMap containing status for the Gateway and associated xRoutes (really parentRefs)
	// here we will merge reports that are per-Proxy to a singleton Report used to persist to k8s on a timer
	s.statusReport = krt.NewSingleton(func(kctx krt.HandlerContext) *report {
		proxies := krt.Fetch(kctx, s.xDS)
		merged := mergeProxyReports(proxies)
		return &report{merged}
	})
}

func (s *AgentGwSyncer) setupSyncDependencies(gateways krt.Collection[Gateway], adpResources krt.Collection[ADPResource], addresses krt.Collection[envoyResourceWithCustomName], inputs Inputs) {
	s.waitForSync = []cache.InformerSynced{
		s.commonCols.HasSynced,
		gateways.HasSynced,
		// resources
		adpResources.HasSynced,
		s.xDS.HasSynced,
		// addresses
		addresses.HasSynced,
		inputs.Namespaces.HasSynced,
	}
}

func (s *AgentGwSyncer) Start(ctx context.Context) error {
	logger.Info("starting agentgateway Syncer", "controllername", s.controllerName)
	logger.Info("waiting for agentgateway cache to sync")

	// wait for krt collections to sync
	logger.Info("waiting for cache to sync")
	s.client.WaitForCacheSync(
		"kube gw proxy syncer",
		ctx.Done(),
		s.waitForSync...,
	)

	// wait for ctrl-rtime caches to sync before accepting events
	if !s.mgr.GetCache().WaitForCacheSync(ctx) {
		return fmt.Errorf("kube gateway sync loop waiting for all caches to sync failed")
	}
	logger.Info("caches warm!")

	// latestReport will be constantly updated to contain the merged status report for Kube Gateway status
	// when timer ticks, we will use the state of the mergedReports at that point in time to sync the status to k8s
	latestReportQueue := utils.NewAsyncQueue[reports.ReportMap]()
	s.statusReport.Register(func(o krt.Event[report]) {
		if o.Event == controllers.EventDelete {
			// TODO: handle garbage collection
			return
		}
		latestReportQueue.Enqueue(o.Latest().reportMap)
	})
	routeStatusLogger := logger.With("subcomponent", "routeStatusSyncer")
	listenerSetStatusLogger := logger.With("subcomponent", "listenerSetStatusSyncer")
	gatewayStatusLogger := logger.With("subcomponent", "gatewayStatusSyncer")
	go func() {
		for {
			latestReport, err := latestReportQueue.Dequeue(ctx)
			if err != nil {
				logger.Error("failed to dequeue latest report", "error", err)
				return
			}
			s.syncGatewayStatus(ctx, gatewayStatusLogger, latestReport)
			s.syncListenerSetStatus(ctx, listenerSetStatusLogger, latestReport)
			s.syncRouteStatus(ctx, routeStatusLogger, latestReport)
		}
	}()

	s.xDS.RegisterBatch(func(events []krt.Event[agentGwXdsResources], _ bool) {
		for _, e := range events {
			snap := e.Latest()
			if e.Event == controllers.EventDelete {
				s.xdsCache.ClearSnapshot(snap.ResourceName())
				continue
			}
			snapshot := &agentGwSnapshot{
				Resources: snap.ResourceConfig,
				Addresses: snap.AddressConfig,
			}
			logger.Debug("setting xds snapshot", "resource_name", snap.ResourceName())
			logger.Debug("snapshot config", "resource_snapshot", snapshot.Resources, "workload_snapshot", snapshot.Addresses)
			err := s.xdsCache.SetSnapshot(ctx, snap.ResourceName(), snapshot)
			if err != nil {
				logger.Error("failed to set xds snapshot", "resource_name", snap.ResourceName(), "error", err.Error())
				continue
			}
		}
	}, true)

	s.ready.Store(true)
	<-ctx.Done()
	return nil
}

func (s *AgentGwSyncer) HasSynced() bool {
	return s.ready.Load()
}

type agentGwSnapshot struct {
	Resources  envoycache.Resources
	Addresses  envoycache.Resources
	VersionMap map[string]map[string]string
}

func (m *agentGwSnapshot) GetResources(typeURL string) map[string]envoytypes.Resource {
	resources := m.GetResourcesAndTTL(typeURL)
	result := make(map[string]envoytypes.Resource, len(resources))
	for k, v := range resources {
		result[k] = v.Resource
	}
	return result
}

func (m *agentGwSnapshot) GetResourcesAndTTL(typeURL string) map[string]envoytypes.ResourceWithTTL {
	switch typeURL {
	case TargetTypeResourceUrl:
		return m.Resources.Items
	case TargetTypeAddressUrl:
		return m.Addresses.Items
	default:
		return nil
	}
}

func (m *agentGwSnapshot) GetVersion(typeURL string) string {
	switch typeURL {
	case TargetTypeResourceUrl:
		return m.Resources.Version
	case TargetTypeAddressUrl:
		return m.Addresses.Version
	default:
		return ""
	}
}

func (m *agentGwSnapshot) ConstructVersionMap() error {
	if m == nil {
		return fmt.Errorf("missing snapshot")
	}
	if m.VersionMap != nil {
		return nil
	}

	m.VersionMap = make(map[string]map[string]string)
	resources := map[string]map[string]envoytypes.ResourceWithTTL{
		TargetTypeResourceUrl: m.Resources.Items,
		TargetTypeAddressUrl:  m.Addresses.Items,
	}

	for typeUrl, items := range resources {
		inner := make(map[string]string, len(items))
		for _, r := range items {
			marshaled, err := envoycache.MarshalResource(r.Resource)
			if err != nil {
				return err
			}
			v := envoycache.HashResource(marshaled)
			if v == "" {
				return fmt.Errorf("failed to build resource version")
			}
			inner[envoycache.GetResourceName(r.Resource)] = v
		}
		m.VersionMap[typeUrl] = inner
	}
	return nil
}

func (m *agentGwSnapshot) GetVersionMap(typeURL string) map[string]string {
	return m.VersionMap[typeURL]
}

var _ envoycache.ResourceSnapshot = &agentGwSnapshot{}

func (s *AgentGwSyncer) syncRouteStatus(ctx context.Context, logger *slog.Logger, rm reports.ReportMap) {
	stopwatch := utils.NewTranslatorStopWatch("RouteStatusSyncer")
	stopwatch.Start()
	defer stopwatch.Stop(ctx)

	// TODO: add routeStatusMetrics

	// Helper function to sync route status with retry
	syncStatusWithRetry := func(
		routeType string,
		routeKey client.ObjectKey,
		getRouteFunc func() client.Object,
		statusUpdater func(route client.Object) error,
	) error {
		return retry.Do(
			func() error {
				route := getRouteFunc()
				err := s.mgr.GetClient().Get(ctx, routeKey, route)
				if err != nil {
					if apierrors.IsNotFound(err) {
						// the route is not found, we can't report status on it
						// if it's recreated, we'll retranslate it anyway
						return nil
					}
					logger.Error("error getting route", logKeyError, err, logKeyResourceRef, routeKey, logKeyRouteType, routeType)
					return err
				}
				if err := statusUpdater(route); err != nil {
					logger.Debug("error updating status for route", logKeyError, err, logKeyResourceRef, routeKey, logKeyRouteType, routeType)
					return err
				}
				return nil
			},
			retry.Attempts(maxRetryAttempts),
			retry.Delay(retryDelay),
			retry.DelayType(retry.BackOffDelay),
		)
	}

	// Helper function to build route status and update if needed
	buildAndUpdateStatus := func(route client.Object, routeType string) error {
		var status *gwv1.RouteStatus
		switch r := route.(type) {
		case *gwv1.HTTPRoute: // TODO: beta1?
			status = rm.BuildRouteStatus(ctx, r, s.controllerName)
			if status == nil || isRouteStatusEqual(&r.Status.RouteStatus, status) {
				return nil
			}
			r.Status.RouteStatus = *status
		case *gwv1alpha2.TCPRoute:
			status = rm.BuildRouteStatus(ctx, r, s.controllerName)
			if status == nil || isRouteStatusEqual(&r.Status.RouteStatus, status) {
				return nil
			}
			r.Status.RouteStatus = *status
		case *gwv1alpha2.TLSRoute:
			status = rm.BuildRouteStatus(ctx, r, s.controllerName)
			if status == nil || isRouteStatusEqual(&r.Status.RouteStatus, status) {
				return nil
			}
			r.Status.RouteStatus = *status
		case *gwv1.GRPCRoute:
			status = rm.BuildRouteStatus(ctx, r, s.controllerName)
			if status == nil || isRouteStatusEqual(&r.Status.RouteStatus, status) {
				return nil
			}
			r.Status.RouteStatus = *status
		default:
			logger.Warn("unsupported route type", logKeyRouteType, routeType, logKeyResourceRef, client.ObjectKeyFromObject(route))
			return nil
		}

		// Update the status
		return s.mgr.GetClient().Status().Update(ctx, route)
	}

	for rnn := range rm.HTTPRoutes {
		err := syncStatusWithRetry(
			wellknown.HTTPRouteKind,
			rnn,
			func() client.Object {
				return new(gwv1.HTTPRoute)
			},
			func(route client.Object) error {
				return buildAndUpdateStatus(route, wellknown.HTTPRouteKind)
			},
		)
		if err != nil {
			logger.Error("all attempts failed at updating HTTPRoute status", logKeyError, err, "route", rnn)
		}
	}
}

// syncGatewayStatus will build and update status for all Gateways in a reportMap
func (s *AgentGwSyncer) syncGatewayStatus(ctx context.Context, logger *slog.Logger, rm reports.ReportMap) {
	stopwatch := utils.NewTranslatorStopWatch("GatewayStatusSyncer")
	stopwatch.Start()

	// TODO: add gatewayStatusMetrics

	// TODO: retry within loop per GW rather that as a full block
	err := retry.Do(func() error {
		for gwnn := range rm.Gateways {
			gw := gwv1.Gateway{}
			err := s.mgr.GetClient().Get(ctx, gwnn, &gw)
			if err != nil {
				if apierrors.IsNotFound(err) {
					// the gateway is not found, we can't report status on it
					// if it's recreated, we'll retranslate it anyway
					continue
				}
				logger.Info("error getting gw", logKeyError, err, logKeyGateway, gwnn.String())
				return err
			}

			// Only process agentgateway classes - others are handled by ProxySyncer
			if string(gw.Spec.GatewayClassName) != s.agentGatewayClassName {
				logger.Debug("skipping status sync for non-agentgateway", logKeyGateway, gwnn.String())
				continue
			}

			gwStatusWithoutAddress := gw.Status
			gwStatusWithoutAddress.Addresses = nil
			if status := rm.BuildGWStatus(ctx, gw); status != nil {
				if !isGatewayStatusEqual(&gwStatusWithoutAddress, status) {
					gw.Status = *status
					if err := s.mgr.GetClient().Status().Patch(ctx, &gw, client.Merge); err != nil {
						logger.Error("error patching gateway status", logKeyError, err, logKeyGateway, gwnn.String())
						return err
					}
					logger.Info("patched gw status", logKeyGateway, gwnn.String())
				} else {
					logger.Info("skipping k8s gateway status update, status equal", logKeyGateway, gwnn.String())
				}
			}
		}
		return nil
	},
		retry.Attempts(maxRetryAttempts),
		retry.Delay(retryDelay),
		retry.DelayType(retry.BackOffDelay),
	)
	if err != nil {
		logger.Error("all attempts failed at updating gateway statuses", logKeyError, err)
	}
	duration := stopwatch.Stop(ctx)
	logger.Debug("synced gw status for gateways", "count", len(rm.Gateways), "duration", duration)
}

// syncListenerSetStatus will build and update status for all Listener Sets in a reportMap
func (s *AgentGwSyncer) syncListenerSetStatus(ctx context.Context, logger *slog.Logger, rm reports.ReportMap) {
	stopwatch := utils.NewTranslatorStopWatch("ListenerSetStatusSyncer")
	stopwatch.Start()

	// TODO: add listenerStatusMetrics

	// TODO: retry within loop per LS rathen that as a full block
	err := retry.Do(func() error {
		for lsnn := range rm.ListenerSets {
			ls := gwxv1a1.XListenerSet{}
			err := s.mgr.GetClient().Get(ctx, lsnn, &ls)
			if err != nil {
				if apierrors.IsNotFound(err) {
					// the listener set is not found, we can't report status on it
					// if it's recreated, we'll retranslate it anyway
					continue
				}
				logger.Info("error getting ls", "erro", err.Error())
				return err
			}
			lsStatus := ls.Status
			if status := rm.BuildListenerSetStatus(ctx, ls); status != nil {
				if !isListenerSetStatusEqual(&lsStatus, status) {
					ls.Status = *status
					if err := s.mgr.GetClient().Status().Patch(ctx, &ls, client.Merge); err != nil {
						logger.Error("error patching listener set status", logKeyError, err, logKeyGateway, lsnn.String())
						return err
					}
					logger.Info("patched ls status", "listenerset", lsnn.String())
				} else {
					logger.Info("skipping k8s ls status update, status equal", "listenerset", lsnn.String())
				}
			}
		}
		return nil
	},
		retry.Attempts(maxRetryAttempts),
		retry.Delay(retryDelay),
		retry.DelayType(retry.BackOffDelay),
	)
	if err != nil {
		logger.Error("all attempts failed at updating listener set statuses", logKeyError, err)
	}
	duration := stopwatch.Stop(ctx)
	logger.Debug("synced listener sets status for listener set", "count", len(rm.ListenerSets), "duration", duration.String())
}

// TODO: refactor proxy_syncer status syncing to use the same logic as agentgateway syncer

var opts = cmp.Options{
	cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"),
	cmpopts.IgnoreMapEntries(func(k string, _ any) bool {
		return k == "lastTransitionTime"
	}),
}

// isRouteStatusEqual compares two RouteStatus objects directly
func isRouteStatusEqual(objA, objB *gwv1.RouteStatus) bool {
	return cmp.Equal(objA, objB, opts)
}

func isListenerSetStatusEqual(objA, objB *gwxv1a1.ListenerSetStatus) bool {
	return cmp.Equal(objA, objB, opts)
}

func mergeProxyReports(proxies []agentGwXdsResources) reports.ReportMap {
	merged := reports.NewReportMap()

	for _, p := range proxies {
		// 1. merge GW Reports for all Proxies' status reports
		maps.Copy(merged.Gateways, p.reports.Gateways)

		// 2. merge LS Reports for all Proxies' status reports
		maps.Copy(merged.ListenerSets, p.reports.ListenerSets)

		// 3. merge route parentRefs into RouteReports for all route types
		mergeRouteReports(merged.HTTPRoutes, p.reports.HTTPRoutes)
		mergeRouteReports(merged.TCPRoutes, p.reports.TCPRoutes)
		mergeRouteReports(merged.TLSRoutes, p.reports.TLSRoutes)
		mergeRouteReports(merged.GRPCRoutes, p.reports.GRPCRoutes)

		// TODO: add back when policies are back
		//for key, report := range p.reports.Policies {
		//	// if we haven't encountered this policy, just copy it over completely
		//	old := merged.Policies[key]
		//	if old == nil {
		//		merged.Policies[key] = report
		//		continue
		//	}
		//	// else, let's merge our parentRefs into the existing map
		//	// obsGen will stay as-is...
		//	maps.Copy(merged.Policies[key].Ancestors, report.Ancestors)
		//}
	}

	return merged
}

// mergeRouteReports is a helper function to merge route reports
func mergeRouteReports(merged map[types.NamespacedName]*reports.RouteReport, source map[types.NamespacedName]*reports.RouteReport) {
	for rnn, rr := range source {
		// if we haven't encountered this route, just copy it over completely
		old := merged[rnn]
		if old == nil {
			merged[rnn] = rr
			continue
		}
		// else, this route has already been seen for a proxy, merge this proxy's parents
		// into the merged report
		maps.Copy(merged[rnn].Parents, rr.Parents)
	}
}

func isGatewayStatusEqual(objA, objB *gwv1.GatewayStatus) bool {
	return cmp.Equal(objA, objB, opts)
}
