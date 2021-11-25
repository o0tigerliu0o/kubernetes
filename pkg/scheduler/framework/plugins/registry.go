/*
Copyright 2019 The Kubernetes Authors.

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

package plugins

import (
	"kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"kubernetes/pkg/scheduler/framework/plugins/defaultpreemption"
	"kubernetes/pkg/scheduler/framework/plugins/imagelocality"
	"kubernetes/pkg/scheduler/framework/plugins/interpodaffinity"
	"kubernetes/pkg/scheduler/framework/plugins/nodeaffinity"
	"kubernetes/pkg/scheduler/framework/plugins/nodelabel"
	"kubernetes/pkg/scheduler/framework/plugins/nodename"
	"kubernetes/pkg/scheduler/framework/plugins/nodeports"
	"kubernetes/pkg/scheduler/framework/plugins/nodepreferavoidpods"
	"kubernetes/pkg/scheduler/framework/plugins/noderesources"
	"kubernetes/pkg/scheduler/framework/plugins/nodeunschedulable"
	"kubernetes/pkg/scheduler/framework/plugins/nodevolumelimits"
	"kubernetes/pkg/scheduler/framework/plugins/podtopologyspread"
	"kubernetes/pkg/scheduler/framework/plugins/queuesort"
	"kubernetes/pkg/scheduler/framework/plugins/selectorspread"
	"kubernetes/pkg/scheduler/framework/plugins/serviceaffinity"
	"kubernetes/pkg/scheduler/framework/plugins/tainttoleration"
	"kubernetes/pkg/scheduler/framework/plugins/volumebinding"
	"kubernetes/pkg/scheduler/framework/plugins/volumerestrictions"
	"kubernetes/pkg/scheduler/framework/plugins/volumezone"
	"kubernetes/pkg/scheduler/framework/runtime"
)

// NewInTreeRegistry builds the registry with all the in-tree plugins.
// A scheduler that runs out of tree plugins can register additional plugins
// through the WithFrameworkOutOfTreeRegistry option.
func NewInTreeRegistry() runtime.Registry {
	return runtime.Registry{
		selectorspread.Name:                        selectorspread.New,
		imagelocality.Name:                         imagelocality.New,
		tainttoleration.Name:                       tainttoleration.New,
		nodename.Name:                              nodename.New,
		nodeports.Name:                             nodeports.New,
		nodepreferavoidpods.Name:                   nodepreferavoidpods.New,
		nodeaffinity.Name:                          nodeaffinity.New,
		podtopologyspread.Name:                     podtopologyspread.New,
		nodeunschedulable.Name:                     nodeunschedulable.New,
		noderesources.FitName:                      noderesources.NewFit,
		noderesources.BalancedAllocationName:       noderesources.NewBalancedAllocation,
		noderesources.MostAllocatedName:            noderesources.NewMostAllocated,
		noderesources.LeastAllocatedName:           noderesources.NewLeastAllocated,
		noderesources.RequestedToCapacityRatioName: noderesources.NewRequestedToCapacityRatio,
		volumebinding.Name:                         volumebinding.New,
		volumerestrictions.Name:                    volumerestrictions.New,
		volumezone.Name:                            volumezone.New,
		nodevolumelimits.CSIName:                   nodevolumelimits.NewCSI,
		nodevolumelimits.EBSName:                   nodevolumelimits.NewEBS,
		nodevolumelimits.GCEPDName:                 nodevolumelimits.NewGCEPD,
		nodevolumelimits.AzureDiskName:             nodevolumelimits.NewAzureDisk,
		nodevolumelimits.CinderName:                nodevolumelimits.NewCinder,
		interpodaffinity.Name:                      interpodaffinity.New,
		nodelabel.Name:                             nodelabel.New,
		serviceaffinity.Name:                       serviceaffinity.New,
		queuesort.Name:                             queuesort.New,
		defaultbinder.Name:                         defaultbinder.New,
		defaultpreemption.Name:                     defaultpreemption.New,
	}
}
