/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package algorithmprovider

import (
	"sort"
	"strings"

	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	"kubernetes/pkg/features"
	schedulerapi "kubernetes/pkg/scheduler/apis/config"
	"kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"kubernetes/pkg/scheduler/framework/plugins/defaultpreemption"
	"kubernetes/pkg/scheduler/framework/plugins/imagelocality"
	"kubernetes/pkg/scheduler/framework/plugins/interpodaffinity"
	"kubernetes/pkg/scheduler/framework/plugins/nodeaffinity"
	"kubernetes/pkg/scheduler/framework/plugins/nodename"
	"kubernetes/pkg/scheduler/framework/plugins/nodeports"
	"kubernetes/pkg/scheduler/framework/plugins/nodepreferavoidpods"
	"kubernetes/pkg/scheduler/framework/plugins/noderesources"
	"kubernetes/pkg/scheduler/framework/plugins/nodeunschedulable"
	"kubernetes/pkg/scheduler/framework/plugins/nodevolumelimits"
	"kubernetes/pkg/scheduler/framework/plugins/podtopologyspread"
	"kubernetes/pkg/scheduler/framework/plugins/queuesort"
	"kubernetes/pkg/scheduler/framework/plugins/selectorspread"
	"kubernetes/pkg/scheduler/framework/plugins/tainttoleration"
	"kubernetes/pkg/scheduler/framework/plugins/volumebinding"
	"kubernetes/pkg/scheduler/framework/plugins/volumerestrictions"
	"kubernetes/pkg/scheduler/framework/plugins/volumezone"
)

// ClusterAutoscalerProvider defines the default autoscaler provider
const ClusterAutoscalerProvider = "ClusterAutoscalerProvider"

// Registry is a collection of all available algorithm providers.
type Registry map[string]*schedulerapi.Plugins

// NewRegistry returns an algorithm provider registry instance.
func NewRegistry() Registry {
	defaultConfig := getDefaultConfig()
	applyFeatureGates(defaultConfig)

	caConfig := getClusterAutoscalerConfig()
	applyFeatureGates(caConfig)

	return Registry{
		schedulerapi.SchedulerDefaultProviderName: defaultConfig,
		ClusterAutoscalerProvider:                 caConfig,
	}
}

// ListAlgorithmProviders lists registered algorithm providers.
func ListAlgorithmProviders() string {
	r := NewRegistry()
	var providers []string
	for k := range r {
		providers = append(providers, k)
	}
	sort.Strings(providers)
	return strings.Join(providers, " | ")
}

func getDefaultConfig() *schedulerapi.Plugins {
	return &schedulerapi.Plugins{
		QueueSort: &schedulerapi.PluginSet{
			Enabled: []schedulerapi.Plugin{
				{Name: queuesort.Name},
			},
		},
		PreFilter: &schedulerapi.PluginSet{
			Enabled: []schedulerapi.Plugin{
				{Name: noderesources.FitName},
				{Name: nodeports.Name},
				{Name: podtopologyspread.Name},
				{Name: interpodaffinity.Name},
				{Name: volumebinding.Name},
			},
		},
		Filter: &schedulerapi.PluginSet{
			Enabled: []schedulerapi.Plugin{
				{Name: nodeunschedulable.Name},
				{Name: noderesources.FitName},
				{Name: nodename.Name},
				{Name: nodeports.Name},
				{Name: nodeaffinity.Name},
				{Name: volumerestrictions.Name},
				{Name: tainttoleration.Name},
				{Name: nodevolumelimits.EBSName},
				{Name: nodevolumelimits.GCEPDName},
				{Name: nodevolumelimits.CSIName},
				{Name: nodevolumelimits.AzureDiskName},
				{Name: volumebinding.Name},
				{Name: volumezone.Name},
				{Name: podtopologyspread.Name},
				{Name: interpodaffinity.Name},
			},
		},
		PostFilter: &schedulerapi.PluginSet{
			Enabled: []schedulerapi.Plugin{
				{Name: defaultpreemption.Name},
			},
		},
		PreScore: &schedulerapi.PluginSet{
			Enabled: []schedulerapi.Plugin{
				{Name: interpodaffinity.Name},
				{Name: podtopologyspread.Name},
				{Name: tainttoleration.Name},
			},
		},
		Score: &schedulerapi.PluginSet{
			Enabled: []schedulerapi.Plugin{
				{Name: noderesources.BalancedAllocationName, Weight: 1},
				{Name: imagelocality.Name, Weight: 1},
				{Name: interpodaffinity.Name, Weight: 1},
				{Name: noderesources.LeastAllocatedName, Weight: 1},
				{Name: nodeaffinity.Name, Weight: 1},
				{Name: nodepreferavoidpods.Name, Weight: 10000},
				// Weight is doubled because:
				// - This is a score coming from user preference.
				// - It makes its signal comparable to NodeResourcesLeastAllocated.
				{Name: podtopologyspread.Name, Weight: 2},
				{Name: tainttoleration.Name, Weight: 1},
			},
		},
		Reserve: &schedulerapi.PluginSet{
			Enabled: []schedulerapi.Plugin{
				{Name: volumebinding.Name},
			},
		},
		PreBind: &schedulerapi.PluginSet{
			Enabled: []schedulerapi.Plugin{
				{Name: volumebinding.Name},
			},
		},
		Bind: &schedulerapi.PluginSet{
			Enabled: []schedulerapi.Plugin{
				{Name: defaultbinder.Name},
			},
		},
	}
}

func getClusterAutoscalerConfig() *schedulerapi.Plugins {
	caConfig := getDefaultConfig()
	// Replace least with most requested.
	for i := range caConfig.Score.Enabled {
		if caConfig.Score.Enabled[i].Name == noderesources.LeastAllocatedName {
			caConfig.Score.Enabled[i].Name = noderesources.MostAllocatedName
		}
	}
	return caConfig
}

func applyFeatureGates(config *schedulerapi.Plugins) {
	if !utilfeature.DefaultFeatureGate.Enabled(features.DefaultPodTopologySpread) {
		// When feature is enabled, the default spreading is done by
		// PodTopologySpread plugin, which is enabled by default.
		klog.Infof("Registering SelectorSpread plugin")
		s := schedulerapi.Plugin{Name: selectorspread.Name}
		config.PreScore.Enabled = append(config.PreScore.Enabled, s)
		s.Weight = 1
		config.Score.Enabled = append(config.Score.Enabled, s)
	}
}
