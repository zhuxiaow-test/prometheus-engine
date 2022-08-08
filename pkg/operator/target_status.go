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
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/prometheus-engine/pkg/export"
	monitoringv1 "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/apis/monitoring/v1"
	monitoringclientset "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/generated/clientset/versioned"
	monitoringinformers "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/generated/informers/externalversions"
	monitoringv1listers "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/generated/listers/monitoring/v1"
	"github.com/go-logr/logr"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/api"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/api/option"
	apihttp "google.golang.org/api/transport/http"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	k8sinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appsv1lister "k8s.io/client-go/listers/apps/v1"
	corev1lister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// TODO(TheSpiritXIII): Remove when we switch to Go 1.18 which has native TryLock support
type tryMutex struct {
	mutex    sync.Mutex
	tryMutex sync.Mutex
	locked   bool
}

func newTryMutex() tryMutex {
	return tryMutex{
		mutex:    sync.Mutex{},
		tryMutex: sync.Mutex{},
		locked:   false,
	}
}

// Unlocks the mutex.
func (m *tryMutex) Unlock() {
	m.mutex.Unlock()
	m.tryMutex.Lock()
	m.locked = false
	m.tryMutex.Unlock()
}

// Tries to lock the mutex. Returns true if the mutex was locked. The mutex could be unlocked but still return false.
func (m *tryMutex) TryLock() bool {
	var success bool
	m.tryMutex.Lock()
	if !m.locked {
		success = true
		m.locked = true
	} else {
		success = false
	}
	m.tryMutex.Unlock()

	if success {
		m.mutex.Lock()
	}
	return success
}

const (
	// How often to do a full refresh of cached Kubernetes data.
	informerResyncInterval = time.Minute * 15
	// How many targets to keep in each group.
	maxSampleTargetSize = 5
)

var (
	targetStatusDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "prometheus_engine_target_status_duration",
		Help: "A metric indicating how long it took to fetch the complete target status.",
	}, []string{})
)

// Wraps a time.Ticker so we can replace for unit testing.
type Ticker interface {
	// Stops the ticker from ticking.
	Stop()
	// Returns the channel receiving ticks.
	Channel() <-chan time.Time
}

type timeTicker time.Ticker

func NewTicker(d time.Duration) Ticker {
	data := timeTicker(*time.NewTicker(d))
	return &data
}

func (t *timeTicker) Stop() {
	ticker := time.Ticker(*t)
	ticker.Stop()
}

func (t *timeTicker) Channel() <-chan time.Time {
	ticker := time.Ticker(*t)
	return ticker.C
}

type TargetStatusProcessor interface {
	Start(ctx context.Context, tickerBuilder func() Ticker) error
}

type dataManagerBuilder interface {
	build(stopCh chan struct{}) (dataManager, error)
}

// Manages fetching and patching Kubernetes data.
type dataManager interface {
	// Returns all pods running Prometheus, mapped with port as the key.
	getPrometheusPods() (map[int32][]*corev1.Pod, error)

	getPodMonitorings() ([]*monitoringv1.PodMonitoring, error)
	getClusterPodMonitorings() ([]*monitoringv1.ClusterPodMonitoring, error)

	patchPodMonitoring(ctx context.Context, podMonitoring *monitoringv1.PodMonitoring) error
	patchClusterPodMonitoring(ctx context.Context, podMonitoring *monitoringv1.ClusterPodMonitoring) error

	// Returns the target fetcher, with an zero-index number `thread` that signifies which goroutine this is for.
	getTargetFetcher(ctx context.Context, thread uint16) (targetFetcher, error)
}

// Responsible for fetching the targets given a pod. One is created per goroutine.
type targetFetcher interface {
	getTarget(ctx context.Context, logger logr.Logger, port int32, pod *corev1.Pod) (*prometheusv1.TargetsResult, error)
}

type targetStatusProcessor struct {
	logger             logr.Logger
	opts               Options
	dataManagerBuilder dataManagerBuilder
}

func NewTargetStatusPoller(logger logr.Logger, restConfig *rest.Config, registry prometheus.Registerer, opts Options) (TargetStatusProcessor, error) {
	builder, err := newInformerDataManagerBuilder(restConfig, opts)
	if err != nil {
		return nil, err
	}

	return newTargetStatusPoller(logger, opts, registry, builder)
}

func newTargetStatusPoller(logger logr.Logger, opts Options, registry prometheus.Registerer, builder dataManagerBuilder) (TargetStatusProcessor, error) {
	if err := registry.Register(targetStatusDuration); err != nil {
		return nil, err
	}

	instance := &targetStatusProcessor{
		logger:             logger,
		opts:               opts,
		dataManagerBuilder: builder,
	}
	return instance, nil
}

func (p *targetStatusProcessor) Start(ctx context.Context, tickerBuilder func() Ticker) error {
	stopCh := make(chan struct{})
	defer close(stopCh)

	manager, err := p.dataManagerBuilder.build(stopCh)
	if err != nil {
		return err
	}

	ticker := tickerBuilder()
	defer ticker.Stop()
	mutex := newTryMutex()

	onceLog := new(sync.Once)

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-ticker.Channel():
			// Wrapper function so we could call defer within it.
			err := func() error {
				// Don't allow multiple polling at the same time.
				if !mutex.TryLock() {
					p.logger.Info("Target status poll in progress, skipping")
					return nil
				}
				defer mutex.Unlock()

				now := time.Now()
				if err := poll(ctx, p.logger, p.opts, manager); err != nil {
					// Not having data is not an error.
					if apierrors.IsNotFound(err) {
						onceLog.Do(func() {
							p.logger.Error(err, "Kubernetes config not found")
						})
						return nil
					}
					return err
				}
				// In case Kubernetes config is removed and re-added.
				onceLog = new(sync.Once)

				duration := time.Since(now)
				targetStatusDuration.WithLabelValues().Set(float64(duration.Milliseconds()))
				return nil
			}()
			if err != nil {
				return err
			}
		}
	}
}

func poll(ctx context.Context, logger logr.Logger, opts Options, manager dataManager) error {
	targets, err := fetchTargets(ctx, logger, opts, manager)
	if err != nil {
		return err
	}

	return populateTargets(ctx, logger, manager, targets)
}

func fetchTargets(ctx context.Context, logger logr.Logger, opts Options, manager dataManager) ([]*prometheusTargetsResultAndTime, error) {
	podMap, err := manager.getPrometheusPods()
	if err != nil {
		return nil, err
	}

	// Set up pod job queue and jobs
	podDiscoveryChan := make(chan prometheusPod, opts.TargetPollConcurrency)
	wg := sync.WaitGroup{}
	wg.Add(int(opts.TargetPollConcurrency))

	// Must be unbounded or else we deadlock.
	targetChan := make(chan *prometheusTargetsResultAndTime)
	for i := uint16(0); i < opts.TargetPollConcurrency; i++ {
		// Wrapper function to handle errors.
		go func(thread uint16) {
			fetcher, err := manager.getTargetFetcher(ctx, thread)
			if err != nil {
				logger.Error(err, "error getting target fetcher")
				return
			}
			err = processTargetQueue(ctx, logger, &wg, fetcher, podDiscoveryChan, targetChan)
			if err != nil {
				logger.Error(err, "error processing target queue")
			}
		}(i)
	}

	// Unbuffered channels are blocking so make sure we end the goroutine processing them.
	go func() {
		for port, pods := range podMap {
			for _, pod := range pods {
				podDiscoveryChan <- prometheusPod{
					port: port,
					pod:  pod,
				}
			}
		}

		// Must close so jobs aren't waiting on the channel indefinitely.
		close(podDiscoveryChan)

		// Close target after we're sure all targets are queued.
		wg.Wait()
		close(targetChan)
	}()

	results := make([]*prometheusTargetsResultAndTime, 0)
	for target := range targetChan {
		results = append(results, target)
	}

	return results, nil
}

func populateTargets(ctx context.Context, logger logr.Logger, manager dataManager, targets []*prometheusTargetsResultAndTime) error {
	endpointBuilder := newScrapeEndpointBuilder()
	for _, target := range targets {
		if err := endpointBuilder.Add(target); err != nil {
			return err
		}
	}

	endpointMap := endpointBuilder.Build()

	podMonitorings, err := manager.getPodMonitorings()
	if err != nil {
		return err
	}
	for _, podMonitoring := range podMonitorings {
		job := podMonitoring.GetKey()

		endpointStatuses, exists := endpointMap[job]
		if exists {
			// Delete the entry so we get what's not included at the end.
			delete(endpointMap, job)
		}
		podMonitoring.GetStatus().EndpointStatuses = endpointStatuses

		if err := manager.patchPodMonitoring(ctx, podMonitoring); err != nil {
			logger.Error(err, "Unable to patch pod monitoring", "pod", podMonitoring.GetKey())
		}
	}

	clusterPodMonitorings, err := manager.getClusterPodMonitorings()
	if err != nil {
		return err
	}
	for _, clusterPodMonitoring := range clusterPodMonitorings {
		job := clusterPodMonitoring.GetKey()

		endpointStatuses, exists := endpointMap[job]
		if exists {
			// Delete the entry so we get what's not included at the end.
			delete(endpointMap, job)
		}
		clusterPodMonitoring.GetStatus().EndpointStatuses = endpointStatuses

		if err := manager.patchClusterPodMonitoring(ctx, clusterPodMonitoring); err != nil {
			logger.Error(err, "Unable to patch cluster pod monitoring", "pod", clusterPodMonitoring.GetKey())
		}
	}

	// Remove hardcoded kubelet job before checking for dropped target jobs.
	delete(endpointMap, "kubelet")

	if len(endpointMap) != 0 {
		// Should rarely happen but not fatal if it does (e.g. we fetched a target but the pod was deleted)
		for job := range endpointMap {
			logger.Error(errors.New("No matching configuration for target job name"), "unpatched objects", "job", job)
		}
	}

	return nil
}

type scrapeEndpointBuilder struct {
	mapByJobByEndpoint map[string]*map[string]*scrapeEndpointStatusBuilder
	total              uint32
	failed             uint32
}

func newScrapeEndpointBuilder() scrapeEndpointBuilder {
	return scrapeEndpointBuilder{
		mapByJobByEndpoint: make(map[string]*map[string]*scrapeEndpointStatusBuilder),
	}
}

func (b *scrapeEndpointBuilder) Add(target *prometheusTargetsResultAndTime) error {
	b.total += 1
	if target != nil {
		for _, activeTarget := range target.targetResults.Active {
			if err := b.addActiveTarget(activeTarget, target.time); err != nil {
				return err
			}
		}
	} else {
		b.failed += 1
	}
	return nil
}

func (b *scrapeEndpointBuilder) addActiveTarget(activeTarget prometheusv1.ActiveTarget, time metav1.Time) error {
	portIndex := strings.LastIndex(activeTarget.ScrapePool, "/")
	if portIndex == -1 {
		return errors.New("Malformed scrape pool: " + activeTarget.ScrapePool)
	}
	job := activeTarget.ScrapePool[:portIndex]
	endpoint := activeTarget.ScrapePool[portIndex+1:]
	mapByEndpoint, exists := b.mapByJobByEndpoint[job]
	if !exists {
		tmp := make(map[string]*scrapeEndpointStatusBuilder)
		mapByEndpoint = &tmp
		b.mapByJobByEndpoint[job] = mapByEndpoint
	}

	statusBuilder, exists := (*mapByEndpoint)[endpoint]
	if !exists {
		statusBuilder = newScrapeEndpointStatusBuilder(&activeTarget, time)
		(*mapByEndpoint)[endpoint] = statusBuilder
	}
	statusBuilder.AddSampleTarget(&activeTarget)
	return nil
}

func (b *scrapeEndpointBuilder) Build() map[string][]monitoringv1.ScrapeEndpointStatus {
	fraction := float64(b.total-b.failed) / float64(b.total)
	collectorsFraction := strconv.FormatFloat(fraction, 'f', -1, 64)
	resultMap := make(map[string][]monitoringv1.ScrapeEndpointStatus)

	for job, endpointMap := range b.mapByJobByEndpoint {
		endpointStatuses := make([]monitoringv1.ScrapeEndpointStatus, 0)
		for _, statusBuilder := range *endpointMap {
			endpointStatus := statusBuilder.Build()
			endpointStatus.CollectorsFraction = collectorsFraction
			endpointStatuses = append(endpointStatuses, endpointStatus)
		}

		// Make endpoint status deterministic.
		sort.SliceStable(endpointStatuses, func(i, j int) bool {
			// Assumes that every sample target in a group has the same error.
			lhsName := endpointStatuses[i].Name
			rhsName := endpointStatuses[j].Name
			return lhsName < rhsName
		})
		resultMap[job] = endpointStatuses
	}
	return resultMap
}

type scrapeEndpointStatusBuilder struct {
	status       monitoringv1.ScrapeEndpointStatus
	groupByError map[string]*monitoringv1.SampleGroup
}

func newScrapeEndpointStatusBuilder(target *prometheusv1.ActiveTarget, time metav1.Time) *scrapeEndpointStatusBuilder {
	return &scrapeEndpointStatusBuilder{
		status: monitoringv1.ScrapeEndpointStatus{
			Name:               target.ScrapePool,
			ActiveTargets:      0,
			UnhealthyTargets:   0,
			LastUpdateTime:     time,
			CollectorsFraction: "0",
		},
		groupByError: make(map[string]*monitoringv1.SampleGroup),
	}
}

// Adds a sample target, potentially merging with a pre-existing one.
func (b *scrapeEndpointStatusBuilder) AddSampleTarget(target *prometheusv1.ActiveTarget) {
	b.status.ActiveTargets += 1
	errorType := target.LastError
	lastError := &errorType
	if target.Health == "up" {
		if len(target.LastError) == 0 {
			lastError = nil
		}
	} else {
		b.status.UnhealthyTargets += 1
	}

	sampleGroup, exists := b.groupByError[errorType]
	sampleTarget := monitoringv1.SampleTarget{
		Health:                    string(target.Health),
		LastError:                 lastError,
		Labels:                    target.Labels,
		LastScrapeDurationSeconds: strconv.FormatFloat(target.LastScrapeDuration, 'f', -1, 64),
	}
	if !exists {
		sampleGroup = &monitoringv1.SampleGroup{
			SampleTargets: []monitoringv1.SampleTarget{},
			Count:         new(int32),
		}
		b.groupByError[errorType] = sampleGroup
	}
	*sampleGroup.Count += 1
	sampleGroup.SampleTargets = append(sampleGroup.SampleTargets, sampleTarget)
}

// Build a deterministic (regarding array ordering) status object.
func (b *scrapeEndpointStatusBuilder) Build() monitoringv1.ScrapeEndpointStatus {
	// Deterministic sample group by error.
	for _, sampleGroup := range b.groupByError {
		sort.SliceStable(sampleGroup.SampleTargets, func(i, j int) bool {
			// Every sample target is guarenteed to have an instance label.
			lhsInstance := sampleGroup.SampleTargets[i].Labels["instance"]
			rhsInstance := sampleGroup.SampleTargets[j].Labels["instance"]
			return lhsInstance < rhsInstance
		})
		sampleTargetsSize := len(sampleGroup.SampleTargets)
		if sampleTargetsSize > maxSampleTargetSize {
			sampleTargetsSize = maxSampleTargetSize
		}
		sampleGroup.SampleTargets = sampleGroup.SampleTargets[0:sampleTargetsSize]
		b.status.SampleGroups = append(b.status.SampleGroups, *sampleGroup)
	}
	sort.SliceStable(b.status.SampleGroups, func(i, j int) bool {
		// Assumes that every sample target in a group has the same error.
		lhsError := b.status.SampleGroups[i].SampleTargets[0].LastError
		rhsError := b.status.SampleGroups[j].SampleTargets[0].LastError
		if lhsError == nil {
			return false
		} else if rhsError == nil {
			return true
		}
		return *lhsError < *rhsError
	})
	return b.status
}

func selectorToRequirements(selector *metav1.LabelSelector) (labels.Requirements, error) {
	requirements := make(labels.Requirements, 0)
	matchLabelsRequirements, err := mapToRequirements(selector.MatchLabels)
	if err != nil {
		return nil, err
	}
	requirements = append(requirements, matchLabelsRequirements...)
	matchExpressionRequirements, err := expressionsToRequirements(selector.MatchExpressions)
	if err != nil {
		return nil, err
	}
	requirements = append(requirements, matchExpressionRequirements...)

	return requirements, nil
}

func mapToRequirements(podLabels map[string]string) (labels.Requirements, error) {
	requirements := make(labels.Requirements, 0, len(podLabels))
	for labelName, labelValue := range podLabels {
		requirement, err := labels.NewRequirement(labelName, selection.Equals, []string{labelValue})
		if err != nil {
			return nil, err
		}
		requirements = append(requirements, *requirement)
	}
	return requirements, nil
}

func expressionsToRequirements(selectorRequirements []metav1.LabelSelectorRequirement) (labels.Requirements, error) {
	requirements := make(labels.Requirements, 0, len(selectorRequirements))
	for _, selectorRequirement := range selectorRequirements {
		var operator selection.Operator
		switch selectorRequirement.Operator {
		case metav1.LabelSelectorOpIn:
			operator = selection.In
		case metav1.LabelSelectorOpNotIn:
			operator = selection.NotIn
		case metav1.LabelSelectorOpExists:
			operator = selection.Exists
		case metav1.LabelSelectorOpDoesNotExist:
			operator = selection.DoesNotExist
		default:
			return nil, errors.New("Unknown label selector operator: " + string(selectorRequirement.Operator))
		}
		requirement, err := labels.NewRequirement(selectorRequirement.Key, operator, selectorRequirement.Values)
		if err != nil {
			return nil, err
		}
		requirements = append(requirements, *requirement)
	}
	return requirements, nil
}

type informerDataManagerBuilder struct {
	restConfig         *rest.Config
	opts               Options
	kubeClientset      *kubernetes.Clientset
	versionedClientset *monitoringclientset.Clientset
}

func newInformerDataManagerBuilder(restConfig *rest.Config, opts Options) (*informerDataManagerBuilder, error) {
	kubeClientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	versionedClientset, err := monitoringclientset.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	builder := &informerDataManagerBuilder{
		restConfig:         restConfig,
		opts:               opts,
		kubeClientset:      kubeClientset,
		versionedClientset: versionedClientset,
	}
	return builder, nil
}

type commonInformerFactory interface {
	Start(stopCh <-chan struct{})
	WaitForCacheSync(stopCh <-chan struct{}) map[reflect.Type]bool
}

func (b *informerDataManagerBuilder) build(stopCh chan struct{}) (dataManager, error) {
	kubeInformerFactory := k8sinformers.NewSharedInformerFactoryWithOptions(b.kubeClientset, informerResyncInterval, k8sinformers.WithNamespace(b.opts.OperatorNamespace))
	podInformer := kubeInformerFactory.Core().V1().Pods()

	daemonSetInformerFactory := k8sinformers.NewSharedInformerFactoryWithOptions(b.kubeClientset, informerResyncInterval, k8sinformers.WithNamespace(b.opts.OperatorNamespace), k8sinformers.WithTweakListOptions(func(listOptions *metav1.ListOptions) {
		listOptions.FieldSelector = fmt.Sprintf("metadata.name=%s", NameCollector)
	}))
	daemonSetInformer := daemonSetInformerFactory.Apps().V1().DaemonSets()

	operatorConfigInformerFactory := monitoringinformers.NewSharedInformerFactoryWithOptions(b.versionedClientset, informerResyncInterval, monitoringinformers.WithNamespace(b.opts.PublicNamespace), monitoringinformers.WithTweakListOptions(func(listOptions *metav1.ListOptions) {
		listOptions.FieldSelector = fmt.Sprintf("metadata.name=%s", NameOperatorConfig)
	}))
	operatorConfigInformer := operatorConfigInformerFactory.Monitoring().V1().OperatorConfigs()

	versionedInformerFactory := monitoringinformers.NewSharedInformerFactory(b.versionedClientset, informerResyncInterval)
	podMonitoringInformer := versionedInformerFactory.Monitoring().V1().PodMonitorings()
	clusterPodMonitoringInformer := versionedInformerFactory.Monitoring().V1().ClusterPodMonitorings()

	// Must create all informed objects before starting so they can start populating.
	podLister := podInformer.Lister()
	daemonSetLister := daemonSetInformer.Lister()
	operatorConfigLister := operatorConfigInformer.Lister()
	podMonitoringLister := podMonitoringInformer.Lister()
	clusterPodMonitoringLister := clusterPodMonitoringInformer.Lister()

	informerFactories := []commonInformerFactory{
		kubeInformerFactory,
		daemonSetInformerFactory,
		operatorConfigInformerFactory,
		versionedInformerFactory,
	}

	// Start all informers at asynchronously before waiting on them.
	for _, informerFactory := range informerFactories {
		informerFactory.Start(stopCh)
	}

	// Must wait for cache sync after starting all informers to ensure they're populated before we call them.
	for _, informerFactory := range informerFactories {
		for ty, success := range informerFactory.WaitForCacheSync(stopCh) {
			if !success {
				return nil, errors.New(fmt.Sprintf("Timed out setting up informer for type: %s", ty.String()))
			}
		}
	}

	manager := &informerDataManager{
		opts:               b.opts,
		restConfig:         b.restConfig,
		kubeClientset:      b.kubeClientset,
		versionedClientset: b.versionedClientset,

		podLister:                  &podLister,
		daemonSetLister:            &daemonSetLister,
		operatorConfigLister:       &operatorConfigLister,
		podMonitoringLister:        &podMonitoringLister,
		clusterPodMonitoringLister: &clusterPodMonitoringLister,
	}
	return manager, nil
}

// Data manager backed by Kubernetes informers.
type informerDataManager struct {
	restConfig         *rest.Config
	opts               Options
	kubeClientset      *kubernetes.Clientset
	versionedClientset *monitoringclientset.Clientset

	podLister                  *corev1lister.PodLister
	daemonSetLister            *appsv1lister.DaemonSetLister
	operatorConfigLister       *monitoringv1listers.OperatorConfigLister
	podMonitoringLister        *monitoringv1listers.PodMonitoringLister
	clusterPodMonitoringLister *monitoringv1listers.ClusterPodMonitoringLister
}

func (m *informerDataManager) getPrometheusPods() (map[int32][]*corev1.Pod, error) {
	namespace := m.opts.OperatorNamespace
	ds, err := (*m.daemonSetLister).DaemonSets(namespace).Get(NameCollector)
	if err != nil {
		return nil, err
	}

	requirements, err := selectorToRequirements(ds.Spec.Selector)
	if err != nil {
		return nil, err
	}
	podsSelector := labels.NewSelector().Add(requirements...)
	podsMap := make(map[int32][]*corev1.Pod)

	pods, err := (*m.podLister).Pods(namespace).List(podsSelector)
	if err != nil {
		return nil, err
	}

	podsFiltered := make([]*corev1.Pod, 0)
	for _, pod := range pods {
		if isPodReady(pod) {
			podsFiltered = append(podsFiltered, pod)
		}
	}

	var port *int32
	for _, container := range ds.Spec.Template.Spec.Containers {
		if isPrometheusContainer(&container) {
			port = getPrometheusPort(&container)
			if port != nil {
				break
			}
		}
	}
	if port == nil {
		return nil, errors.New("Unable to detect Prometheus port")
	}

	podsMap[*port] = podsFiltered
	return podsMap, nil
}

func (m *informerDataManager) getPodMonitorings() ([]*monitoringv1.PodMonitoring, error) {
	return (*m.podMonitoringLister).List(labels.Everything())
}

func (m *informerDataManager) getClusterPodMonitorings() ([]*monitoringv1.ClusterPodMonitoring, error) {
	return (*m.clusterPodMonitoringLister).List(labels.Everything())
}

func (m *informerDataManager) patchPodMonitoring(ctx context.Context, podMonitoring *monitoringv1.PodMonitoring) error {
	// TODO(TheSpiritXIII): In the future, change this to server side apply as opposed to patch.
	patchStatus := map[string]interface{}{"endpointStatuses": podMonitoring.Status.EndpointStatuses}
	patch := map[string]interface{}{"status": patchStatus}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	if _, err := m.versionedClientset.MonitoringV1().PodMonitorings(podMonitoring.Namespace).Patch(ctx, podMonitoring.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{}, "status"); err != nil {
		return err
	}
	return nil
}

func (m *informerDataManager) patchClusterPodMonitoring(ctx context.Context, podMonitoring *monitoringv1.ClusterPodMonitoring) error {
	// TODO(TheSpiritXIII): In the future, change this to server side apply as opposed to patch.
	patchStatus := map[string]interface{}{"endpointStatuses": podMonitoring.Status.EndpointStatuses}
	patch := map[string]interface{}{"status": patchStatus}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	if _, err := m.versionedClientset.MonitoringV1().ClusterPodMonitorings().Patch(ctx, podMonitoring.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{}, "status"); err != nil {
		return err
	}
	return nil
}

func (m *informerDataManager) getTargetFetcher(ctx context.Context, thread uint16) (targetFetcher, error) {
	config, err := (*m.operatorConfigLister).OperatorConfigs(m.opts.PublicNamespace).Get(NameOperatorConfig)
	if err != nil {
		return nil, err
	}

	filepath := ""
	if config.Collection.Credentials != nil {
		filepath = pathForSelector(m.opts.PublicNamespace, &monitoringv1.SecretOrConfigMap{Secret: config.Collection.Credentials})
	}

	var transport http.RoundTripper
	if !m.opts.TargetPollNoGoogleCloudHTTP {
		transport, err = googleRoundTripper(ctx, filepath)
		if err != nil {
			return nil, err
		}
	}

	if m.opts.TargetPollPortForwardEnabled {
		port := m.opts.TargetPollPortForwardPort + int32(thread)
		return newPortForwardedTargetFetcher(ctx, m.restConfig, port, transport)
	}

	return newPassthroughTargetFetcher(transport)
}

type passthroughTargetFetcher struct {
	transport http.RoundTripper
}

func newPassthroughTargetFetcher(transport http.RoundTripper) (*passthroughTargetFetcher, error) {
	fetcher := &passthroughTargetFetcher{
		transport: transport,
	}
	return fetcher, nil
}

func googleRoundTripper(ctx context.Context, queryCredentialsFile string) (http.RoundTripper, error) {
	opts := []option.ClientOption{
		option.WithScopes("https://www.googleapis.com/auth/monitoring.read"),
		option.WithUserAgent(fmt.Sprintf("operator-target-poll/%s", export.Version)),
		option.WithGRPCDialOption(grpc.WithUnaryInterceptor(grpc_prometheus.UnaryClientInterceptor)),
	}
	if queryCredentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(queryCredentialsFile))
	}

	transport, err := apihttp.NewTransport(ctx, http.DefaultTransport, opts...)
	if err != nil {
		return nil, fmt.Errorf("unable to initialize Google API transport: %w", err)
	}

	return transport, nil
}

type InfoWriter logr.Logger

func (w *InfoWriter) Write(b []byte) (int, error) {
	(*logr.Logger)(w).Info(fmt.Sprintf("Port forward: %s", string(b)))
	return len(b), nil
}

type ErrorWriter logr.Logger

func (w *ErrorWriter) Write(b []byte) (int, error) {
	(*logr.Logger)(w).Error(errors.New(fmt.Sprintf("Port forward")), string(b))
	return len(b), nil
}

func (f *passthroughTargetFetcher) getTarget(ctx context.Context, logger logr.Logger, port int32, pod *corev1.Pod) (*prometheusv1.TargetsResult, error) {
	podUrl := fmt.Sprintf("http://%s:%d", pod.Status.PodIP, port)
	return fetchTarget(ctx, podUrl, f.transport)
}

func fetchTarget(ctx context.Context, podUrl string, roundTripper http.RoundTripper) (*prometheusv1.TargetsResult, error) {
	client, err := api.NewClient(api.Config{
		Address:      podUrl,
		RoundTripper: roundTripper,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create Prometheus client: %w", err)
	}
	v1api := prometheusv1.NewAPI(client)
	targetsResult, err := v1api.Targets(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch targets: %w", err)
	}

	return &targetsResult, nil
}

type portForwardedTargetFetcher struct {
	restConfig                   *rest.Config
	prometheusClientRoundTripper http.RoundTripper
	portForwardRoundTripper      http.RoundTripper
	portForwardUpgrader          spdy.Upgrader
	port                         int32
}

func newPortForwardedTargetFetcher(ctx context.Context, restConfig *rest.Config, port int32, transport http.RoundTripper) (*portForwardedTargetFetcher, error) {
	portForwardRoundTripper, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to create port forward round tripper: %w", err)
	}

	fetcher := &portForwardedTargetFetcher{
		restConfig:                   restConfig,
		prometheusClientRoundTripper: transport,
		portForwardRoundTripper:      portForwardRoundTripper,
		portForwardUpgrader:          upgrader,
		port:                         port,
	}
	return fetcher, nil
}

func (f *portForwardedTargetFetcher) getTarget(ctx context.Context, logger logr.Logger, port int32, pod *corev1.Pod) (*prometheusv1.TargetsResult, error) {
	podUrl := fmt.Sprintf("http://%s:%d", "localhost", *&f.port)
	host := f.restConfig.Host
	if host == "" {
		host = "localhost"
	} else {
		host = strings.TrimPrefix(host, "https://")
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", pod.Namespace, pod.Name)
	serverURL := url.URL{Scheme: "https", Path: path, Host: host}
	dialer := spdy.NewDialer(f.portForwardUpgrader, &http.Client{Transport: f.portForwardRoundTripper}, http.MethodPost, &serverURL)

	stopChan := make(chan struct{}, 1)
	readyChan := make(chan struct{}, 1)

	infoWriter := InfoWriter(logger)
	errorWriter := ErrorWriter(logger)
	portForwarder, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", f.port, port)}, stopChan, readyChan, &infoWriter, &errorWriter)
	if err != nil {
		return nil, fmt.Errorf("unable to create Kubernetes port forwarder: %w", err)
	}
	go func() {
		err := portForwarder.ForwardPorts()
		if err != nil {
			logger.Error(err, "Failed to forward ports")
			// So we don't wait indefinitely.
			close(stopChan)
		}
	}()
	select {
	case <-stopChan:
		return nil, errors.New("port forward stopped before we could fetch targets")
	case <-readyChan:
		defer close(stopChan)
		return fetchTarget(ctx, podUrl, nil)
	}
}

type prometheusPod struct {
	port int32
	pod  *corev1.Pod
}
type prometheusTargetsResultAndTime struct {
	targetResults prometheusv1.TargetsResult
	time          metav1.Time
}

func isPodReady(pod *corev1.Pod) bool {
	switch pod.Status.Phase {
	case v1.PodRunning:
		for containerIndex, containerStatus := range pod.Status.ContainerStatuses {
			container := pod.Spec.Containers[containerIndex]
			if !containerStatus.Ready {
				continue
			}
			if isPrometheusContainer(&container) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func isPrometheusContainer(container *corev1.Container) bool {
	return container.Name == "prometheus"
}

func getPrometheusPort(container *corev1.Container) *int32 {
	for _, containerPort := range container.Ports {
		// In the future, we could fall back to reading the command line args.
		if containerPort.Name == "prom-metrics" {
			// Make a copy.
			port := containerPort.ContainerPort
			return &port
		}
	}
	return nil
}

// A job that consumes pod channels and for each pod, retrieve the targets.
func processTargetQueue(ctx context.Context, logger logr.Logger, wg *sync.WaitGroup, fetcher targetFetcher, podChan <-chan prometheusPod, targetChan chan *prometheusTargetsResultAndTime) error {
	defer wg.Done()

	for prometheusPod := range podChan {
		now := metav1.NewTime(time.Now())
		target, err := fetcher.getTarget(ctx, logger, prometheusPod.port, prometheusPod.pod)
		if err != nil {
			logger.Error(err, "failed to fetch target")
		}
		if target == nil {
			targetChan <- nil
		} else {
			targetChan <- &prometheusTargetsResultAndTime{
				targetResults: *target,
				time:          now,
			}
		}
	}

	return nil
}
