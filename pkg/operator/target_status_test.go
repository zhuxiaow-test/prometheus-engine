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
	"fmt"
	"strings"
	"testing"
	"time"

	monitoringv1 "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/apis/monitoring/v1"
	v1 "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/apis/monitoring/v1"
	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
)

type targetConversionData struct {
	name                  string
	prometheusTargets     []*prometheusTargetsResultAndTime
	podMonitorings        []monitoringv1.PodMonitoring
	clusterPodMonitorings []monitoringv1.ClusterPodMonitoring
}

func count(amount int32) *int32 {
	return &amount
}

func lastError(err string) *string {
	return &err
}

var date1 = metav1.Date(2022, time.January, 4, 0, 0, 0, 0, time.UTC)

func getData() []targetConversionData {
	targetConversionDataPodMonitoring := []targetConversionData{
		// All empty -- nothing happens.
		{
			name: "empty-monitorings",
		},
		// Single target, no monitorings -- nothing happens.
		{
			name: "single-target-no-monitorings",
			prometheusTargets: []*prometheusTargetsResultAndTime{
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "up",
							LastError:  "",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 1.2,
						}},
					},
					time: date1,
				},
			},
		},
		// Single healthy target with no error, with matching PodMonitoring.
		{
			name: "single-healthy-target",
			prometheusTargets: []*prometheusTargetsResultAndTime{
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "up",
							LastError:  "",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 1.2,
						}},
					},
					time: date1,
				},
			},
			podMonitorings: []monitoringv1.PodMonitoring{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-1", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
					Status: monitoringv1.PodMonitoringStatus{
						EndpointStatuses: []v1.ScrapeEndpointStatus{
							{
								Name:             "PodMonitoring/gmp-test/prom-example-1/metrics",
								ActiveTargets:    1,
								UnhealthyTargets: 0,
								LastUpdateTime:   date1,
								SampleGroups: []v1.SampleGroup{
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health: "up",
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "a",
												},
												LastScrapeDurationSeconds: "1.2",
											},
										},
										Count: count(1),
									},
								},
								CollectorsFraction: "1",
							},
						},
					},
				}},
		},
		// Collectors failure.
		{
			name: "collectors-failure",
			prometheusTargets: []*prometheusTargetsResultAndTime{
				nil,
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "up",
							LastError:  "",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 1.2,
						}},
					},
					time: date1,
				},
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "up",
							LastError:  "",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-2/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "b",
							}),
							LastScrapeDuration: 2.4,
						}},
					},
					time: date1,
				},
				nil,
				nil,
			},
			podMonitorings: []monitoringv1.PodMonitoring{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-1", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
					Status: monitoringv1.PodMonitoringStatus{
						EndpointStatuses: []v1.ScrapeEndpointStatus{
							{
								Name:             "PodMonitoring/gmp-test/prom-example-1/metrics",
								ActiveTargets:    1,
								UnhealthyTargets: 0,
								LastUpdateTime:   date1,
								SampleGroups: []v1.SampleGroup{
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health: "up",
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "a",
												},
												LastScrapeDurationSeconds: "1.2",
											},
										},
										Count: count(1),
									},
								},
								CollectorsFraction: "0.4",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-2", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
					Status: monitoringv1.PodMonitoringStatus{
						EndpointStatuses: []v1.ScrapeEndpointStatus{
							{
								Name:             "PodMonitoring/gmp-test/prom-example-2/metrics",
								ActiveTargets:    1,
								UnhealthyTargets: 0,
								LastUpdateTime:   date1,
								SampleGroups: []v1.SampleGroup{
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health: "up",
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "b",
												},
												LastScrapeDurationSeconds: "2.4",
											},
										},
										Count: count(1),
									},
								},
								CollectorsFraction: "0.4",
							},
						},
					},
				}},
		},
		// Single healthy target with no error, with non-matching PodMonitoring.
		{
			name: "single-healthy-target-no-match",
			prometheusTargets: []*prometheusTargetsResultAndTime{
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "up",
							LastError:  "",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-2/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 1.2,
						}},
					},
					time: date1,
				},
			},
			podMonitorings: []monitoringv1.PodMonitoring{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-1", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
				}},
		},
		// Single healthy target with no error, with single matching PodMonitoring.
		{
			name: "single-healthy-target-matching",
			prometheusTargets: []*prometheusTargetsResultAndTime{
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "up",
							LastError:  "",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-2/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 1.2,
						}},
					},
					time: date1,
				},
			},
			podMonitorings: []monitoringv1.PodMonitoring{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-1", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-2", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
					Status: monitoringv1.PodMonitoringStatus{
						EndpointStatuses: []v1.ScrapeEndpointStatus{
							{
								Name:             "PodMonitoring/gmp-test/prom-example-2/metrics",
								ActiveTargets:    1,
								UnhealthyTargets: 0,
								LastUpdateTime:   date1,
								SampleGroups: []v1.SampleGroup{
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health: "up",
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "a",
												},
												LastScrapeDurationSeconds: "1.2",
											},
										},
										Count: count(1),
									},
								},
								CollectorsFraction: "1",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-3", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
				}},
		},
		// Single healthy target with an error, with matching PodMonitoring.
		{
			name: "single-healthy-target-with-error-matching",
			prometheusTargets: []*prometheusTargetsResultAndTime{
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "up",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 1.2,
						}},
					},
					time: date1,
				},
			},
			podMonitorings: []monitoringv1.PodMonitoring{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-1", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
					Status: monitoringv1.PodMonitoringStatus{
						EndpointStatuses: []v1.ScrapeEndpointStatus{
							{
								Name:             "PodMonitoring/gmp-test/prom-example-1/metrics",
								ActiveTargets:    1,
								UnhealthyTargets: 0,
								LastUpdateTime:   date1,
								SampleGroups: []v1.SampleGroup{
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "up",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "a",
												},
												LastScrapeDurationSeconds: "1.2",
											},
										},
										Count: count(1),
									},
								},
								CollectorsFraction: "1",
							},
						},
					},
				}},
		},
		// Single unhealthy target with an error, with matching PodMonitoring.
		{
			name: "single-unhealthy-target-with-error-matching",
			prometheusTargets: []*prometheusTargetsResultAndTime{
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 1.2,
						}},
					},
					time: date1,
				},
			},
			podMonitorings: []monitoringv1.PodMonitoring{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-1", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
					Status: monitoringv1.PodMonitoringStatus{
						EndpointStatuses: []v1.ScrapeEndpointStatus{
							{
								Name:             "PodMonitoring/gmp-test/prom-example-1/metrics",
								ActiveTargets:    1,
								UnhealthyTargets: 1,
								LastUpdateTime:   date1,
								SampleGroups: []v1.SampleGroup{
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "a",
												},
												LastScrapeDurationSeconds: "1.2",
											},
										},
										Count: count(1),
									},
								},
								CollectorsFraction: "1",
							},
						},
					},
				}},
		},
		// One healthy and one unhealthy target.
		{
			name: "single-healthy-single-unhealthy",
			prometheusTargets: []*prometheusTargetsResultAndTime{
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "b",
							}),
							LastScrapeDuration: 1.2,
						}, {
							Health:     "up",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 4.3,
						}},
					},
					time: date1,
				},
			},
			podMonitorings: []monitoringv1.PodMonitoring{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-1", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
					Status: monitoringv1.PodMonitoringStatus{
						EndpointStatuses: []v1.ScrapeEndpointStatus{
							{
								Name:             "PodMonitoring/gmp-test/prom-example-1/metrics",
								ActiveTargets:    2,
								UnhealthyTargets: 1,
								LastUpdateTime:   date1,
								SampleGroups: []v1.SampleGroup{
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "b",
												},
												LastScrapeDurationSeconds: "1.2",
											},
										},
										Count: count(1),
									},
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health: "up",
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "a",
												},
												LastScrapeDurationSeconds: "4.3",
											},
										},
										Count: count(1),
									},
								},
								CollectorsFraction: "1",
							},
						},
					},
				}},
		},
		// Multiple targets with multiple endpoints.
		{
			name: "multiple-targets-multiple-endpoints",
			prometheusTargets: []*prometheusTargetsResultAndTime{
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics-2",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "d",
							}),
							LastScrapeDuration: 3.6,
						}, {
							Health:     "down",
							LastError:  "err y",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics-1",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "b",
							}),
							LastScrapeDuration: 7.0,
						}, {
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics-1",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 5.3,
						}, {
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics-2",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "c",
							}),
							LastScrapeDuration: 1.2,
						}},
					},
					time: date1,
				},
			},
			podMonitorings: []monitoringv1.PodMonitoring{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-1", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics-1"),
						}, {
							Port: intstr.FromString("metrics-2"),
						}},
					},
					Status: monitoringv1.PodMonitoringStatus{
						EndpointStatuses: []v1.ScrapeEndpointStatus{
							{
								Name:             "PodMonitoring/gmp-test/prom-example-1/metrics-1",
								ActiveTargets:    2,
								UnhealthyTargets: 2,
								LastUpdateTime:   date1,
								SampleGroups: []v1.SampleGroup{
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "a",
												},
												LastScrapeDurationSeconds: "5.3",
											},
										},
										Count: count(1),
									},
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "down",
												LastError: lastError("err y"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "b",
												},
												LastScrapeDurationSeconds: "7",
											},
										},
										Count: count(1),
									},
								},
								CollectorsFraction: "1",
							},
							{
								Name:             "PodMonitoring/gmp-test/prom-example-1/metrics-2",
								ActiveTargets:    2,
								UnhealthyTargets: 2,
								LastUpdateTime:   date1,
								SampleGroups: []v1.SampleGroup{
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "c",
												},
												LastScrapeDurationSeconds: "1.2",
											},
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "d",
												},
												LastScrapeDurationSeconds: "3.6",
											},
										},
										Count: count(2),
									},
								},
								CollectorsFraction: "1",
							},
						},
					},
				}},
		},
		// Multiple unhealthy target with different errors.
		{
			name: "multiple-unhealthy-targets",
			prometheusTargets: []*prometheusTargetsResultAndTime{
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "f",
							}),
							LastScrapeDuration: 1.2,
						}, {
							Health:     "down",
							LastError:  "err y",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "c",
							}),
							LastScrapeDuration: 2.4,
						}, {
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "e",
							}),
							LastScrapeDuration: 3.6,
						}, {
							Health:     "down",
							LastError:  "err z",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "d",
							}),
							LastScrapeDuration: 4.7,
						}, {
							Health:     "down",
							LastError:  "err z",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 5.0,
						}, {
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "b",
							}),
							LastScrapeDuration: 6.8,
						}},
					},
					time: date1,
				},
			},
			podMonitorings: []monitoringv1.PodMonitoring{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-1", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
					Status: monitoringv1.PodMonitoringStatus{
						EndpointStatuses: []v1.ScrapeEndpointStatus{
							{
								Name:             "PodMonitoring/gmp-test/prom-example-1/metrics",
								ActiveTargets:    6,
								UnhealthyTargets: 6,
								LastUpdateTime:   date1,
								SampleGroups: []v1.SampleGroup{
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "b",
												},
												LastScrapeDurationSeconds: "6.8",
											},
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "e",
												},
												LastScrapeDurationSeconds: "3.6",
											},
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "f",
												},
												LastScrapeDurationSeconds: "1.2",
											},
										},
										Count: count(3),
									},
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "down",
												LastError: lastError("err y"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "c",
												},
												LastScrapeDurationSeconds: "2.4",
											},
										},
										Count: count(1),
									},
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "down",
												LastError: lastError("err z"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "a",
												},
												LastScrapeDurationSeconds: "5",
											},
											{
												Health:    "down",
												LastError: lastError("err z"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "d",
												},
												LastScrapeDurationSeconds: "4.7",
											},
										},
										Count: count(2),
									},
								},
								CollectorsFraction: "1",
							},
						},
					},
				}},
		},
		// Multiple unhealthy targets, one cut-off.
		{
			name: "multiple-unhealthy-targets-cut-off",
			prometheusTargets: []*prometheusTargetsResultAndTime{
				{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "f",
							}),
							LastScrapeDuration: 1.2,
						}, {
							Health:     "down",
							LastError:  "err y",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "c",
							}),
							LastScrapeDuration: 2.4,
						}, {
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 3.6,
						}, {
							Health:     "down",
							LastError:  "err z",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "d",
							}),
							LastScrapeDuration: 4.7,
						}, {
							Health:     "down",
							LastError:  "err z",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "a",
							}),
							LastScrapeDuration: 5.0,
						}, {
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "b",
							}),
							LastScrapeDuration: 6.8,
						}, {
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "e",
							}),
							LastScrapeDuration: 4.1,
						}, {
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "f",
							}),
							LastScrapeDuration: 7.3,
						}, {
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "c",
							}),
							LastScrapeDuration: 2.7,
						}, {
							Health:     "down",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": "d",
							}),
							LastScrapeDuration: 9.5,
						}},
					},
					time: date1,
				},
			},
			podMonitorings: []monitoringv1.PodMonitoring{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prom-example-1", Namespace: "gmp-test"},
					Spec: v1.PodMonitoringSpec{
						Endpoints: []v1.ScrapeEndpoint{{
							Port: intstr.FromString("metrics"),
						}},
					},
					Status: monitoringv1.PodMonitoringStatus{
						EndpointStatuses: []v1.ScrapeEndpointStatus{
							{
								Name:             "PodMonitoring/gmp-test/prom-example-1/metrics",
								ActiveTargets:    10,
								UnhealthyTargets: 10,
								LastUpdateTime:   date1,
								SampleGroups: []v1.SampleGroup{
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "a",
												},
												LastScrapeDurationSeconds: "3.6",
											},
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "b",
												},
												LastScrapeDurationSeconds: "6.8",
											},
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "c",
												},
												LastScrapeDurationSeconds: "2.7",
											},
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "d",
												},
												LastScrapeDurationSeconds: "9.5",
											},
											{
												Health:    "down",
												LastError: lastError("err x"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "e",
												},
												LastScrapeDurationSeconds: "4.1",
											},
										},
										Count: count(7),
									},
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "down",
												LastError: lastError("err y"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "c",
												},
												LastScrapeDurationSeconds: "2.4",
											},
										},
										Count: count(1),
									},
									{
										SampleTargets: []v1.SampleTarget{
											{
												Health:    "down",
												LastError: lastError("err z"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "a",
												},
												LastScrapeDurationSeconds: "5",
											},
											{
												Health:    "down",
												LastError: lastError("err z"),
												Labels: map[model.LabelName]model.LabelValue{
													"instance": "d",
												},
												LastScrapeDurationSeconds: "4.7",
											},
										},
										Count: count(2),
									},
								},
								CollectorsFraction: "1",
							},
						},
					},
				}},
		},
	}

	dataFinal := make([]targetConversionData, 0)
	for _, data := range targetConversionDataPodMonitoring {
		if len(data.podMonitorings) == 0 {
			continue
		}
		clusterPrometheusTargets := make([]*prometheusTargetsResultAndTime, 0, len(data.prometheusTargets))
		clusterPodMonitorings := make([]monitoringv1.ClusterPodMonitoring, 0, len(data.podMonitorings))
		for _, prometheusTarget := range data.prometheusTargets {
			if prometheusTarget == nil {
				clusterPrometheusTargets = append(clusterPrometheusTargets, nil)
				continue
			}
			clusterActive := make([]prometheusv1.ActiveTarget, 0, len(prometheusTarget.targetResults.Active))
			for _, active := range prometheusTarget.targetResults.Active {
				activeCluster := active
				activeCluster.ScrapePool = podMonitoringScrapePoolToClusterPodMonitoringScrapePool(active.ScrapePool)
				clusterActive = append(clusterActive, activeCluster)
			}
			prometheusTargetClusterPodMonitoring := &prometheusTargetsResultAndTime{
				targetResults: prometheusTarget.targetResults,
				time:          prometheusTarget.time,
			}
			prometheusTargetClusterPodMonitoring.targetResults.Active = clusterActive
			clusterPrometheusTargets = append(clusterPrometheusTargets, prometheusTargetClusterPodMonitoring)
		}
		for _, podMonitoring := range data.podMonitorings {
			copy := podMonitoring.DeepCopy()
			clusterPodMonitoring := monitoringv1.ClusterPodMonitoring{
				TypeMeta:   copy.TypeMeta,
				ObjectMeta: copy.ObjectMeta,
				Spec: monitoringv1.ClusterPodMonitoringSpec{
					Selector:     copy.Spec.Selector,
					Endpoints:    copy.Spec.Endpoints,
					TargetLabels: copy.Spec.TargetLabels,
					Limits:       copy.Spec.Limits,
				},
				Status: copy.Status,
			}
			for endpointIndex, endpointStatus := range clusterPodMonitoring.Status.EndpointStatuses {
				clusterPodMonitoring.Status.EndpointStatuses[endpointIndex].Name = podMonitoringScrapePoolToClusterPodMonitoringScrapePool(endpointStatus.Name)
			}
			clusterPodMonitorings = append(clusterPodMonitorings, clusterPodMonitoring)
		}
		dataPodMonitorings := targetConversionData{
			name:              data.name + "-pod-monitoring",
			prometheusTargets: data.prometheusTargets,
			podMonitorings:    data.podMonitorings,
		}
		dataClusterPodMonitorings := targetConversionData{
			name:                  data.name + "-cluster-pod-monitoring",
			prometheusTargets:     clusterPrometheusTargets,
			clusterPodMonitorings: clusterPodMonitorings,
		}
		prometheusTargetsBoth := append(data.prometheusTargets, clusterPrometheusTargets...)
		dataBoth := targetConversionData{
			name:                  data.name + "-both",
			prometheusTargets:     prometheusTargetsBoth,
			podMonitorings:        data.podMonitorings,
			clusterPodMonitorings: data.clusterPodMonitorings,
		}
		dataFinal = append(dataFinal, dataPodMonitorings)
		dataFinal = append(dataFinal, dataClusterPodMonitorings)
		dataFinal = append(dataFinal, dataBoth)
	}
	return dataFinal
}

func podMonitoringScrapePoolToClusterPodMonitoringScrapePool(podMonitoringScrapePool string) string {
	scrapePool := podMonitoringScrapePool[len("PodMonitoring/"):]
	scrapePool = scrapePool[strings.Index(scrapePool, "/")+1:]
	return "ClusterPodMonitoring/" + scrapePool
}

type mockDataManager struct {
	podMonitorings        []*monitoringv1.PodMonitoring
	clusterPodMonitorings []*monitoringv1.ClusterPodMonitoring
	prometheusPods        map[int32][]*corev1.Pod
	prometheusTargets     map[string]*prometheusv1.TargetsResult
}

func (m *mockDataManager) getPrometheusPods() (map[int32][]*corev1.Pod, error) {
	return m.prometheusPods, nil
}

func (m *mockDataManager) getPodMonitorings() ([]*monitoringv1.PodMonitoring, error) {
	return m.podMonitorings, nil
}

func (m *mockDataManager) getClusterPodMonitorings() ([]*monitoringv1.ClusterPodMonitoring, error) {
	return m.clusterPodMonitorings, nil
}

func (m *mockDataManager) patchPodMonitoring(ctx context.Context, podMonitoring *monitoringv1.PodMonitoring) error {
	pm := m.getPodMonitoring(podMonitoring)
	if pm == nil {
		return apierrors.NewNotFound(v1.Resource("podmonitoring"), podMonitoring.Name)
	}
	*pm = *podMonitoring
	return nil
}

func (m *mockDataManager) patchClusterPodMonitoring(ctx context.Context, podMonitoring *monitoringv1.ClusterPodMonitoring) error {
	pm := m.getClusterPodMonitoring(podMonitoring)
	if pm == nil {
		return apierrors.NewNotFound(v1.Resource("clusterpodmonitoring"), podMonitoring.Name)
	}
	*pm = *podMonitoring
	return nil
}

func (m *mockDataManager) getPodMonitoring(podMonitoring *monitoringv1.PodMonitoring) *monitoringv1.PodMonitoring {
	for _, pm := range m.podMonitorings {
		if pm.Namespace == podMonitoring.Namespace && pm.Name == podMonitoring.Name {
			return pm
		}
	}
	return nil
}

func (m *mockDataManager) getClusterPodMonitoring(podMonitoring *monitoringv1.ClusterPodMonitoring) *monitoringv1.ClusterPodMonitoring {
	for _, pm := range m.clusterPodMonitorings {
		if pm.Name == podMonitoring.Name {
			return pm
		}
	}
	return nil
}

func (m *mockDataManager) getTarget(ctx context.Context, logger logr.Logger, port int32, pod *corev1.Pod) (*prometheusv1.TargetsResult, error) {
	targetsResult, exists := m.prometheusTargets[fmt.Sprintf("%s:%d", pod.Status.PodIP, port)]
	if !exists {
		return nil, errors.New("Does not exist")
	}
	return targetsResult, nil
}

// getTargetFetcher implements dataManager
func (m *mockDataManager) getTargetFetcher(ctx context.Context, thread uint16) (targetFetcher, error) {
	return m, nil
}

type mockDataManagerBuilder struct {
	manager *mockDataManager
}

func (b *mockDataManagerBuilder) build(stopCh chan struct{}) (dataManager, error) {
	return b.manager, nil
}

func TestTargetStatusConversion(t *testing.T) {
	for _, data := range getData() {
		t.Run(fmt.Sprintf("target-status-conversion-%s", data.name), func(t *testing.T) {
			podMonitoringsInitial := make([]*monitoringv1.PodMonitoring, 0)
			for _, podMonitoring := range data.podMonitorings {
				copy := podMonitoring.DeepCopy()
				copy.GetStatus().EndpointStatuses = nil
				podMonitoringsInitial = append(podMonitoringsInitial, copy)
			}
			clusterPodMonitoringsInitial := make([]*monitoringv1.ClusterPodMonitoring, 0)
			for _, clusterPodMonitoring := range data.clusterPodMonitorings {
				copy := clusterPodMonitoring.DeepCopy()
				copy.GetStatus().EndpointStatuses = nil
				clusterPodMonitoringsInitial = append(clusterPodMonitoringsInitial, copy)
			}
			dataManager := &mockDataManager{
				podMonitorings:        podMonitoringsInitial,
				clusterPodMonitorings: clusterPodMonitoringsInitial,
			}
			err := populateTargets(context.Background(), testr.New(t), dataManager, data.prometheusTargets)
			if err != nil {
				t.Fatalf("Failed to populate targets: %s", err)
			}

			for _, podMonitoring := range data.podMonitorings {
				after := dataManager.getPodMonitoring(&podMonitoring)
				if !cmp.Equal(&podMonitoring, after) {
					t.Errorf("PodMonitoring does not match: %s\n%s", podMonitoring.GetKey(), cmp.Diff(&podMonitoring, after))
				}
			}
			if len(data.podMonitorings) != len(dataManager.podMonitorings) {
				t.Errorf("Mismatched PodMonitorings amount: %d vs %d", len(data.podMonitorings), len(dataManager.podMonitorings))
			}
			for _, clusterPodMonitoring := range data.clusterPodMonitorings {
				after := dataManager.getClusterPodMonitoring(&clusterPodMonitoring)
				if !cmp.Equal(&clusterPodMonitoring, after) {
					t.Errorf("ClusterPodMonitoring does not match: %s\n%s", clusterPodMonitoring.GetKey(), cmp.Diff(&clusterPodMonitoring, after))
				}
			}
			if len(data.clusterPodMonitorings) != len(dataManager.clusterPodMonitorings) {
				t.Errorf("Mismatched ClusterPodMonitorings amount: %d vs %d", len(data.clusterPodMonitorings), len(dataManager.clusterPodMonitorings))
			}
		})
	}
}

type testTicker struct {
	c chan time.Time
}

func newTestTicker() *testTicker {
	return &testTicker{
		// Expect a single tick at a time.
		c: make(chan time.Time, 0),
	}
}

func (t *testTicker) Stop() {
	// noop
}

func (t *testTicker) Channel() <-chan time.Time {
	return t.c
}

func (t *testTicker) tick() {
	t.c <- time.Time{}
}

func getPodKey(pod *corev1.Pod, port int32) string {
	return fmt.Sprintf("%s:%d", pod.Status.PodIP, port)
}

// Test that polling propagates all the way through and only on ticks.
func TestPolling(t *testing.T) {
	opts := Options{
		ProjectID:             "test-proj",
		Location:              "test-loc",
		Cluster:               "test-cluster",
		OperatorNamespace:     "gmp-system",
		TargetPollConcurrency: 4,
	}

	ticker := newTestTicker()

	port := int32(19090)
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-a",
			Namespace: "gmp-test",
		},
		Status: corev1.PodStatus{
			PodIP: "127.0.0.1",
		},
	}
	prometheusPods := map[int32][]*corev1.Pod{}
	prometheusPods[port] = []*corev1.Pod{pod}

	podMonitorings := []*monitoringv1.PodMonitoring{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "prom-example-1", Namespace: "gmp-test"},
			Spec: v1.PodMonitoringSpec{
				Endpoints: []v1.ScrapeEndpoint{{
					Port: intstr.FromString("metrics"),
				}},
			},
		}}

	prometheusTargetMap := make(map[string]*prometheusv1.TargetsResult, 1)
	key := getPodKey(pod, port)
	prometheusTargetMap[key] = &prometheusv1.TargetsResult{
		Active: []prometheusv1.ActiveTarget{{
			Health: "up",
			Labels: map[model.LabelName]model.LabelValue{
				"instance": model.LabelValue("a"),
			},
			ScrapePool:         "PodMonitoring/gmp-test/prom-example-1/metrics",
			LastError:          "err x",
			LastScrapeDuration: 1.2,
		}},
		Dropped: []prometheusv1.DroppedTarget{},
	}

	dataManager := &mockDataManager{
		podMonitorings:    podMonitorings,
		prometheusPods:    prometheusPods,
		prometheusTargets: prometheusTargetMap,
	}

	dataManagerBuilder := &mockDataManagerBuilder{manager: dataManager}
	poller, err := newTargetStatusPoller(testr.New(t), opts, prometheus.NewRegistry(), dataManagerBuilder)
	if err != nil {
		t.Fatal("Unable to create target poller", err)
	}
	go func() {
		poller.Start(context.Background(), func() Ticker { return ticker })
	}()

	normalizeEndpointStatuses := func(endpointStatuses []monitoringv1.ScrapeEndpointStatus) {
		for i := range endpointStatuses {
			endpointStatuses[i].LastUpdateTime = metav1.Time{}
		}
	}

	expectStatus := func(t *testing.T, expected []monitoringv1.ScrapeEndpointStatus) {
		if len(dataManager.podMonitorings) != 1 {
			t.Error("Expected to find single PodMonitoring")
			return
		}

		// Must poll because status is updated via other thread.
		err := wait.Poll(100*time.Millisecond, 5*time.Second, func() (done bool, err error) {
			// Clear last update time.
			status := dataManager.podMonitorings[0].Status.EndpointStatuses
			normalizeEndpointStatuses(status)
			return cmp.Equal(status, expected), nil
		})
		if err != nil {
			status := dataManager.podMonitorings[0].Status.EndpointStatuses
			normalizeEndpointStatuses(status)
			t.Error("Expected endpoint statuses to be:", cmp.Diff(status, expected))
		}
	}

	// Status should be empty initially but we don't know if we started yet.
	expectStatus(t, nil)

	// First tick.
	ticker.tick()
	statusTick1 := []v1.ScrapeEndpointStatus{
		{
			Name:             "PodMonitoring/gmp-test/prom-example-1/metrics",
			ActiveTargets:    1,
			UnhealthyTargets: 0,
			SampleGroups: []v1.SampleGroup{
				{
					SampleTargets: []v1.SampleTarget{
						{
							Health: "up",
							Labels: map[model.LabelName]model.LabelValue{
								"instance": "a",
							},
							LastError:                 lastError("err x"),
							LastScrapeDurationSeconds: "1.2",
						},
					},
					Count: count(1),
				},
			},
			CollectorsFraction: "1",
		},
	}
	expectStatus(t, statusTick1)

	active := &prometheusTargetMap[key].Active[0]
	active.Health = "down"
	active.LastError = "err y"
	active.LastScrapeDuration = 5.4
	// We didn't tick yet so we don't expect a change yet.
	expectStatus(t, statusTick1)

	// Second tick.
	ticker.tick()
	statusTick2 := []v1.ScrapeEndpointStatus{
		{
			Name:             "PodMonitoring/gmp-test/prom-example-1/metrics",
			ActiveTargets:    1,
			UnhealthyTargets: 1,
			SampleGroups: []v1.SampleGroup{
				{
					SampleTargets: []v1.SampleTarget{
						{
							Health: "down",
							Labels: map[model.LabelName]model.LabelValue{
								"instance": "a",
							},
							LastError:                 lastError("err y"),
							LastScrapeDurationSeconds: "5.4",
						},
					},
					Count: count(1),
				},
			},
			CollectorsFraction: "1",
		},
	}
	expectStatus(t, statusTick2)

	active = &prometheusTargetMap[key].Active[0]
	active.Health = "up"
	active.LastError = "err z"
	active.LastScrapeDuration = 8.3
	// We didn't tick yet so we don't expect a change yet.
	expectStatus(t, statusTick2)

	ticker.tick()
	statusTick3 := []v1.ScrapeEndpointStatus{
		{
			Name:             "PodMonitoring/gmp-test/prom-example-1/metrics",
			ActiveTargets:    1,
			UnhealthyTargets: 0,
			SampleGroups: []v1.SampleGroup{
				{
					SampleTargets: []v1.SampleTarget{
						{
							Health: "up",
							Labels: map[model.LabelName]model.LabelValue{
								"instance": "a",
							},
							LastError:                 lastError("err z"),
							LastScrapeDurationSeconds: "8.3",
						},
					},
					Count: count(1),
				},
			},
			CollectorsFraction: "1",
		},
	}
	expectStatus(t, statusTick3)
}

// Tests that for pod, targets are fetched correctly (concurrently).
func TestFetching(t *testing.T) {
	concurrency := uint16(4)
	opts := Options{
		TargetPollConcurrency: concurrency,
	}

	testData := []uint16{0, 1, 2, concurrency - 1, concurrency, concurrency + 1, concurrency * 3}
	for amount := range testData {
		t.Run(fmt.Sprintf("fetch-%d-pods", amount), func(t *testing.T) {
			port := int32(19090)
			pods := make([]*corev1.Pod, 0, amount)
			prometheusTargetMap := make(map[string]*prometheusv1.TargetsResult, amount)
			targetsExpected := make([]*prometheusTargetsResultAndTime, 0, amount)
			for i := 0; i < amount; i++ {
				pod := &corev1.Pod{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("pod-%d", i),
						Namespace: "gmp-test",
					},
					Status: corev1.PodStatus{
						PodIP: fmt.Sprint(i),
					},
				}
				pods = append(pods, pod)

				prometheusTargetMap[getPodKey(pod, port)] = &prometheusv1.TargetsResult{
					Active: []prometheusv1.ActiveTarget{{
						Health: "up",
						Labels: map[model.LabelName]model.LabelValue{
							"instance": model.LabelValue(fmt.Sprint(amount)),
						},
						ScrapePool:         "PodMonitoring/gmp-test/prom-example-1/metrics",
						LastError:          "err x",
						LastScrapeDuration: 1.2,
					}},
					Dropped: []prometheusv1.DroppedTarget{},
				}

				target := &prometheusTargetsResultAndTime{
					targetResults: prometheusv1.TargetsResult{
						Active: []prometheusv1.ActiveTarget{{
							Health:     "up",
							LastError:  "err x",
							ScrapePool: "PodMonitoring/gmp-test/prom-example-1/metrics",
							Labels: model.LabelSet(map[model.LabelName]model.LabelValue{
								"instance": model.LabelValue(fmt.Sprint(amount)),
							}),
							LastScrapeDuration: 1.2,
						}},
						Dropped: []prometheusv1.DroppedTarget{},
					},
					time: date1,
				}
				targetsExpected = append(targetsExpected, target)
			}

			podMap := make(map[int32][]*corev1.Pod, 1)
			podMap[port] = pods
			dataManager := mockDataManager{
				prometheusPods:    podMap,
				prometheusTargets: prometheusTargetMap,
			}

			targets, err := fetchTargets(context.Background(), testr.New(t), opts, &dataManager)
			if err != nil {
				t.Fatal("Unable to fetch targets", err)
			}

			if len(targets) != amount {
				t.Fatalf("Wrong target amount: %s", cmp.Diff(len(targets), amount))
			}

		Outer:
			for targetIndex, targetExpected := range targetsExpected {
				for _, foundTarget := range targets {
					if cmp.Equal(targetExpected.targetResults, foundTarget.targetResults) {
						break Outer
					}
				}
				t.Fatal("Targets mismatch:", targetIndex)
			}
		})
	}
}
