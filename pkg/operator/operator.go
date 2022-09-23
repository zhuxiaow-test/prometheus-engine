// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package operator

import (
	"context"
	"io/ioutil"
	"net"
	"strconv"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	arv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeutil "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	monitoringv1 "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/apis/monitoring/v1"
)

const (
	// DefaultOperatorNamespace is the namespace in which all resources owned by the operator are installed.
	DefaultOperatorNamespace = "gmp-system"
	// DefaultPublicNamespace is the namespace where the operator will check for user-specified
	// configuration data.
	DefaultPublicNamespace = "gmp-public"

	// Fixed names used in various resources managed by the operator.
	NameOperator  = "gmp-operator"
	componentName = "managed_prometheus"

	// Filename for configuration files.
	configFilename = "config.yaml"

	// The well-known app name label.
	LabelAppName = "app.kubernetes.io/name"
	// The component name, will be exposed as metric name.
	AnnotationMetricName = "components.gke.io/component-name"
	// ClusterAutoscalerSafeEvictionLabel is the annotation label that determines
	// whether the cluster autoscaler can safely evict a Pod when the Pod doesn't
	// satisfy certain eviction criteria.
	ClusterAutoscalerSafeEvictionLabel = "cluster-autoscaler.kubernetes.io/safe-to-evict"

	// The k8s Application, will be exposed as component name.
	KubernetesAppName    = "app"
	RuleEvaluatorAppName = "managed-prometheus-rule-evaluator"
	AlertmanagerAppName  = "managed-prometheus-alertmanager"

	// How often to poll the targets.
	targetPollInterval = 10 * time.Second
	// The level of concurrency to use to fetch all targets.
	defaultTargetPollConcurrency = 4
)

// Operator to implement managed collection for Google Prometheus Engine.
type Operator struct {
	logger  logr.Logger
	opts    Options
	client  client.Client
	manager manager.Manager
	// Due to the RBAC, the manager can only handle a single namespace per
	// object at a time so this cache is used in cases where we want the same
	// resource from multiple namespaces (not to be confused with cluster-wide
	// resources).
	managedNamespacesCache cache.Cache
	webhookManager         WebhookManager
}

// Options for the Operator.
type Options struct {
	// ID of the project of the cluster.
	ProjectID string
	// Location of the cluster.
	Location string
	// Name of the cluster the operator acts on.
	Cluster string
	// Namespace to which the operator deploys any associated resources.
	OperatorNamespace string
	// Namespace to which the operator looks for user-specified configuration
	// data, like Secrets and ConfigMaps.
	PublicNamespace string
	// Certificate of the server in base 64.
	TLSCert string
	// Key of the server in base 64.
	TLSKey string
	// Certificate authority in base 64.
	CACert string
	// Webhook serving address.
	ListenAddr string
	// Cleanup resources without this annotation.
	CleanupAnnotKey string
	// Whether to disable target polling.
	TargetPollDisabled bool
	// The number of upper bound threads to use for target polling otherwise
	// use the default.
	TargetPollConcurrency uint16
}

func (o *Options) defaultAndValidate(logger logr.Logger) error {
	if o.OperatorNamespace == "" {
		o.OperatorNamespace = DefaultOperatorNamespace
	}
	if o.PublicNamespace == "" {
		// For non-managed deployments, default to same namespace
		// as operator, assuming cluster operators prefer consolidating
		// resources in a single namespace.
		o.PublicNamespace = DefaultOperatorNamespace
	}

	// ProjectID and Cluster must be always be set. Collectors and rule-evaluator can
	// auto-discover them but we need them in the operator to scope generated rules.
	if o.ProjectID == "" {
		return errors.New("ProjectID must be set")
	}
	if o.Cluster == "" {
		return errors.New("Cluster must be set")
	}

	if o.TargetPollConcurrency == 0 {
		o.TargetPollConcurrency = defaultTargetPollConcurrency
	}
	return nil
}

func getScheme() (*runtime.Scheme, error) {
	sc := runtime.NewScheme()

	if err := scheme.AddToScheme(sc); err != nil {
		return nil, errors.Wrap(err, "add Kubernetes core scheme")
	}
	if err := monitoringv1.AddToScheme(sc); err != nil {
		return nil, errors.Wrap(err, "add monitoringv1 scheme")
	}
	return sc, nil
}

// New instantiates a new Operator.
func New(logger logr.Logger, clientConfig *rest.Config, opts Options) (*Operator, error) {
	if err := opts.defaultAndValidate(logger); err != nil {
		return nil, errors.Wrap(err, "invalid options")
	}
	// Create temporary directory to store webhook serving cert files.
	certDir, err := ioutil.TempDir("", "operator-cert")
	if err != nil {
		return nil, errors.Wrap(err, "create temporary certificate dir")
	}

	sc, err := getScheme()
	if err != nil {
		return nil, errors.Wrap(err, "unable to initialize Kubernetes scheme")
	}

	host, portStr, err := net.SplitHostPort(opts.ListenAddr)
	if err != nil {
		return nil, errors.Wrap(err, "invalid listen address")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, errors.Wrap(err, "invalid port")
	}
	manager, err := ctrl.NewManager(clientConfig, manager.Options{
		Scheme: sc,
		Host:   host,
		Port:   port,
		// Don't run a metrics server with the manager. Metrics are being served
		// explicitly in the main routine.
		MetricsBindAddress: "0",
		// Manage cluster-wide and namespace resources at the same time.
		NewCache: cache.NewCacheFunc(func(config *rest.Config, options cache.Options) (cache.Cache, error) {
			return cache.New(clientConfig, cache.Options{
				Scheme: options.Scheme,
				// The presence of metadata.namespace has special handling internally causing the
				// cache's watch-list to only watch that namespace.
				SelectorsByObject: cache.SelectorsByObject{
					&corev1.Pod{}: {
						Field: fields.SelectorFromSet(fields.Set{"metadata.namespace": opts.OperatorNamespace}),
					},
					&monitoringv1.PodMonitoring{}: {
						Field: fields.Everything(),
					},
					&monitoringv1.ClusterPodMonitoring{}: {
						Field: fields.Everything(),
					},
					&monitoringv1.GlobalRules{}: {
						Field: fields.Everything(),
					},
					&monitoringv1.ClusterRules{}: {
						Field: fields.Everything(),
					},
					&monitoringv1.Rules{}: {
						Field: fields.Everything(),
					},
					&corev1.Secret{}: {
						// We can only have 1 namespace specified here. While we
						// need to access secrets from multiple namespaces, we
						// specify one here so that the manager's client
						// accesses secrets from this namespace through a cache.
						Field: fields.SelectorFromSet(fields.Set{
							"metadata.namespace": opts.PublicNamespace,
						}),
					},
					&monitoringv1.OperatorConfig{}: {
						Field: fields.SelectorFromSet(fields.Set{"metadata.namespace": opts.PublicNamespace}),
					},
					&corev1.Service{}: {
						Field: fields.SelectorFromSet(fields.Set{
							"metadata.namespace": opts.OperatorNamespace,
							"metadata.name":      NameAlertmanager,
						}),
					},
					&corev1.ConfigMap{}: {
						Field: fields.SelectorFromSet(fields.Set{"metadata.namespace": opts.OperatorNamespace}),
					},
					&appsv1.DaemonSet{}: {
						Field: fields.SelectorFromSet(fields.Set{
							"metadata.namespace": opts.OperatorNamespace,
							"metadata.name":      NameCollector,
						}),
					},
					&appsv1.Deployment{}: {
						Field: fields.SelectorFromSet(fields.Set{
							"metadata.namespace": opts.OperatorNamespace,
							"metadata.name":      NameRuleEvaluator,
						}),
					},
				}})
		}),
		CertDir: certDir,
	})
	if err != nil {
		return nil, errors.Wrap(err, "create controller manager")
	}

	namespaces := []string{opts.OperatorNamespace, opts.PublicNamespace}
	managedNamespacesCache, err := cache.MultiNamespacedCacheBuilder(namespaces)(clientConfig, cache.Options{
		Scheme: sc,
	})
	if err != nil {
		return nil, errors.Wrap(err, "create controller manager")
	}

	client, err := client.New(clientConfig, client.Options{Scheme: sc})
	if err != nil {
		return nil, errors.Wrap(err, "create client")
	}

	op := &Operator{
		logger:                 logger,
		opts:                   opts,
		client:                 client,
		manager:                manager,
		managedNamespacesCache: managedNamespacesCache,
		webhookManager: WebhookManager{
			logger: logger,
			opts:   opts,
			client: client,
		},
	}
	return op, nil
}

// Run the reconciliation loop of the operator.
// The passed owner references are set on cluster-wide resources created by the
// operator.
func (o *Operator) Run(ctx context.Context, registry prometheus.Registerer) error {
	defer runtimeutil.HandleCrash()

	if err := o.webhookManager.Cleanup(ctx); err != nil {
		return errors.Wrap(err, "init admission resources")
	}
	if err := o.cleanupOldResources(ctx); err != nil {
		return errors.Wrap(err, "cleanup old webhook resources")
	}
	if err := o.webhookManager.Run(ctx, o.manager.GetWebhookServer()); err != nil {
		return errors.Wrap(err, "init admission resources")
	}
	if err := setupCollectionControllers(o); err != nil {
		return errors.Wrap(err, "setup collection controllers")
	}
	if err := setupRulesControllers(o); err != nil {
		return errors.Wrap(err, "setup rules controllers")
	}
	if err := setupOperatorConfigControllers(o); err != nil {
		return errors.Wrap(err, "setup rule-evaluator controllers")
	}

	if !o.opts.TargetPollDisabled {
		if err := setupTargetStatusPoller(o, registry); err != nil {
			return errors.Wrap(err, "setup target status processor")
		}
	}

	o.logger.Info("starting GMP operator")

	go func() {
		o.managedNamespacesCache.Start(ctx)
	}()
	return o.manager.Start(ctx)
}

func (o *Operator) cleanupOldResources(ctx context.Context) error {
	// Delete old ValidatingWebhookConfiguration that was installed directly by the operator
	// in previous versions.
	err := o.client.Delete(ctx, &arv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "gmp-operator"},
	})
	if err != nil && !apierrors.IsNotFound(err) {
		o.logger.Error(err, "deleting legacy ValidatingWebhookConfiguration")
		return err
	}

	// If cleanup annotations are not provided, do not clean up any further.
	if o.opts.CleanupAnnotKey == "" {
		return nil
	}

	// Cleanup resources without the provided annotation.
	// Check the collector DaemonSet.
	var ds appsv1.DaemonSet
	if err := o.client.Get(ctx, client.ObjectKey{
		Name:      NameCollector,
		Namespace: o.opts.OperatorNamespace,
	}, &ds); err != nil && apierrors.IsNotFound(err) {
		o.logger.Error(err, "Getting collector DaemonSet failed")
		return err
	}
	if _, ok := ds.Annotations[o.opts.CleanupAnnotKey]; !ok {
		if err := o.client.Delete(ctx, &ds); err != nil && !apierrors.IsNotFound(err) {
			o.logger.Error(err, "cleaning up collector DaemonSet")
			return err
		}
	}

	// Check the rule-evaluator Deployment.
	var deploy appsv1.Deployment
	if err := o.client.Get(ctx, client.ObjectKey{
		Name:      NameRuleEvaluator,
		Namespace: o.opts.OperatorNamespace,
	}, &deploy); err != nil && apierrors.IsNotFound(err) {
		o.logger.Error(err, "Getting rule-evaluator deployment failed")
		return err
	}
	if _, ok := deploy.Annotations[o.opts.CleanupAnnotKey]; !ok {
		if err := o.client.Delete(ctx, &deploy); err != nil && !apierrors.IsNotFound(err) {
			o.logger.Error(err, "cleaning up rule-evaluator Deployment")
			return err
		}
	}

	return nil
}

// namespacedNamePredicate is an event filter predicate that only allows events with
// a single object.
type namespacedNamePredicate struct {
	namespace string
	name      string
}

func (o namespacedNamePredicate) Create(e event.CreateEvent) bool {
	return e.Object.GetNamespace() == o.namespace && e.Object.GetName() == o.name
}
func (o namespacedNamePredicate) Update(e event.UpdateEvent) bool {
	return e.ObjectNew.GetNamespace() == o.namespace && e.ObjectNew.GetName() == o.name
}
func (o namespacedNamePredicate) Delete(e event.DeleteEvent) bool {
	return e.Object.GetNamespace() == o.namespace && e.Object.GetName() == o.name
}
func (o namespacedNamePredicate) Generic(e event.GenericEvent) bool {
	return e.Object.GetNamespace() == o.namespace && e.Object.GetName() == o.name
}

// enqueueConst always enqueues the same request regardless of the event.
type enqueueConst reconcile.Request

func (e enqueueConst) Create(_ event.CreateEvent, q workqueue.RateLimitingInterface) {
	q.Add(reconcile.Request(e))
}

func (e enqueueConst) Update(_ event.UpdateEvent, q workqueue.RateLimitingInterface) {
	q.Add(reconcile.Request(e))
}

func (e enqueueConst) Delete(_ event.DeleteEvent, q workqueue.RateLimitingInterface) {
	q.Add(reconcile.Request(e))
}

func (e enqueueConst) Generic(_ event.GenericEvent, q workqueue.RateLimitingInterface) {
	q.Add(reconcile.Request(e))
}
