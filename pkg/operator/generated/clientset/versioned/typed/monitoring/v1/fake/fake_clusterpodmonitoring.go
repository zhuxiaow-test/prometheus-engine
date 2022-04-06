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

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	"context"

	monitoringv1 "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/apis/monitoring/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeClusterPodMonitorings implements ClusterPodMonitoringInterface
type FakeClusterPodMonitorings struct {
	Fake *FakeMonitoringV1
}

var clusterpodmonitoringsResource = schema.GroupVersionResource{Group: "monitoring.googleapis.com", Version: "v1", Resource: "clusterpodmonitorings"}

var clusterpodmonitoringsKind = schema.GroupVersionKind{Group: "monitoring.googleapis.com", Version: "v1", Kind: "ClusterPodMonitoring"}

// Get takes name of the clusterPodMonitoring, and returns the corresponding clusterPodMonitoring object, and an error if there is any.
func (c *FakeClusterPodMonitorings) Get(ctx context.Context, name string, options v1.GetOptions) (result *monitoringv1.ClusterPodMonitoring, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootGetAction(clusterpodmonitoringsResource, name), &monitoringv1.ClusterPodMonitoring{})
	if obj == nil {
		return nil, err
	}
	return obj.(*monitoringv1.ClusterPodMonitoring), err
}

// List takes label and field selectors, and returns the list of ClusterPodMonitorings that match those selectors.
func (c *FakeClusterPodMonitorings) List(ctx context.Context, opts v1.ListOptions) (result *monitoringv1.ClusterPodMonitoringList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootListAction(clusterpodmonitoringsResource, clusterpodmonitoringsKind, opts), &monitoringv1.ClusterPodMonitoringList{})
	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &monitoringv1.ClusterPodMonitoringList{ListMeta: obj.(*monitoringv1.ClusterPodMonitoringList).ListMeta}
	for _, item := range obj.(*monitoringv1.ClusterPodMonitoringList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested clusterPodMonitorings.
func (c *FakeClusterPodMonitorings) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchAction(clusterpodmonitoringsResource, opts))
}

// Create takes the representation of a clusterPodMonitoring and creates it.  Returns the server's representation of the clusterPodMonitoring, and an error, if there is any.
func (c *FakeClusterPodMonitorings) Create(ctx context.Context, clusterPodMonitoring *monitoringv1.ClusterPodMonitoring, opts v1.CreateOptions) (result *monitoringv1.ClusterPodMonitoring, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootCreateAction(clusterpodmonitoringsResource, clusterPodMonitoring), &monitoringv1.ClusterPodMonitoring{})
	if obj == nil {
		return nil, err
	}
	return obj.(*monitoringv1.ClusterPodMonitoring), err
}

// Update takes the representation of a clusterPodMonitoring and updates it. Returns the server's representation of the clusterPodMonitoring, and an error, if there is any.
func (c *FakeClusterPodMonitorings) Update(ctx context.Context, clusterPodMonitoring *monitoringv1.ClusterPodMonitoring, opts v1.UpdateOptions) (result *monitoringv1.ClusterPodMonitoring, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateAction(clusterpodmonitoringsResource, clusterPodMonitoring), &monitoringv1.ClusterPodMonitoring{})
	if obj == nil {
		return nil, err
	}
	return obj.(*monitoringv1.ClusterPodMonitoring), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeClusterPodMonitorings) UpdateStatus(ctx context.Context, clusterPodMonitoring *monitoringv1.ClusterPodMonitoring, opts v1.UpdateOptions) (*monitoringv1.ClusterPodMonitoring, error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateSubresourceAction(clusterpodmonitoringsResource, "status", clusterPodMonitoring), &monitoringv1.ClusterPodMonitoring{})
	if obj == nil {
		return nil, err
	}
	return obj.(*monitoringv1.ClusterPodMonitoring), err
}

// Delete takes name of the clusterPodMonitoring and deletes it. Returns an error if one occurs.
func (c *FakeClusterPodMonitorings) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewRootDeleteAction(clusterpodmonitoringsResource, name), &monitoringv1.ClusterPodMonitoring{})
	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeClusterPodMonitorings) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewRootDeleteCollectionAction(clusterpodmonitoringsResource, listOpts)

	_, err := c.Fake.Invokes(action, &monitoringv1.ClusterPodMonitoringList{})
	return err
}

// Patch applies the patch and returns the patched clusterPodMonitoring.
func (c *FakeClusterPodMonitorings) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *monitoringv1.ClusterPodMonitoring, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootPatchSubresourceAction(clusterpodmonitoringsResource, name, pt, data, subresources...), &monitoringv1.ClusterPodMonitoring{})
	if obj == nil {
		return nil, err
	}
	return obj.(*monitoringv1.ClusterPodMonitoring), err
}
