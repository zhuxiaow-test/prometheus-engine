// Copyright 2021 Google LLC
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

// Code generated by informer-gen. DO NOT EDIT.

package v1

import (
	"context"
	time "time"

	monitoringv1 "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/apis/monitoring/v1"
	versioned "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/generated/clientset/versioned"
	internalinterfaces "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/generated/informers/externalversions/internalinterfaces"
	v1 "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/generated/listers/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// GlobalRulesInformer provides access to a shared informer and lister for
// GlobalRules.
type GlobalRulesInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1.GlobalRulesLister
}

type globalRulesInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
}

// NewGlobalRulesInformer constructs a new informer for GlobalRules type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewGlobalRulesInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredGlobalRulesInformer(client, resyncPeriod, indexers, nil)
}

// NewFilteredGlobalRulesInformer constructs a new informer for GlobalRules type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredGlobalRulesInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.MonitoringV1().GlobalRules().List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.MonitoringV1().GlobalRules().Watch(context.TODO(), options)
			},
		},
		&monitoringv1.GlobalRules{},
		resyncPeriod,
		indexers,
	)
}

func (f *globalRulesInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredGlobalRulesInformer(client, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *globalRulesInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&monitoringv1.GlobalRules{}, f.defaultInformer)
}

func (f *globalRulesInformer) Lister() v1.GlobalRulesLister {
	return v1.NewGlobalRulesLister(f.Informer().GetIndexer())
}
