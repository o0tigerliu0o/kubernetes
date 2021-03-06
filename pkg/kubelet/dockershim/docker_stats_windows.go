// +build windows,!dockerless

/*
Copyright 2017 The Kubernetes Authors.

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

package dockershim

import (
	"context"
	"strings"
	"time"

	"github.com/Microsoft/hcsshim"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog/v2"
)

func (ds *dockerService) getContainerStats(containerID string) (*runtimeapi.ContainerStats, error) {
	info, err := ds.client.Info()
	if err != nil {
		return nil, err
	}

	hcsshim_container, err := hcsshim.OpenContainer(containerID)
	if err != nil {
		// As we moved from using Docker stats to hcsshim directly, we may query HCS with already exited container IDs.
		// That will typically happen with init-containers in Exited state. Docker still knows about them but the HCS does not.
		// As we don't want to block stats retrieval for other containers, we only log errors.
		if !hcsshim.IsNotExist(err) && !hcsshim.IsAlreadyStopped(err) {
			klog.V(4).Infof("Error opening container (stats will be missing) '%s': %v", containerID, err)
		}
		return nil, nil
	}
	defer func() {
		closeErr := hcsshim_container.Close()
		if closeErr != nil {
			klog.Errorf("Error closing container '%s': %v", containerID, closeErr)
		}
	}()

	stats, err := hcsshim_container.Statistics()
	if err != nil {
		if strings.Contains(err.Error(), "0x5") || strings.Contains(err.Error(), "0xc0370105") {
			// When the container is just created, querying for stats causes access errors because it hasn't started yet
			// This is transient; skip container for now
			//
			// These hcs errors do not have helpers exposed in public package so need to query for the known codes
			// https://github.com/microsoft/hcsshim/blob/master/internal/hcs/errors.go
			// PR to expose helpers in hcsshim: https://github.com/microsoft/hcsshim/pull/933
			klog.V(4).Infof("Container is not in a state that stats can be accessed '%s': %v. This occurs when the container is created but not started.", containerID, err)
			return nil, nil
		}
		return nil, err
	}

	containerJSON, err := ds.client.InspectContainerWithSize(containerID)
	if err != nil {
		return nil, err
	}

	statusResp, err := ds.ContainerStatus(context.Background(), &runtimeapi.ContainerStatusRequest{ContainerId: containerID})
	if err != nil {
		return nil, err
	}
	status := statusResp.GetStatus()

	timestamp := time.Now().UnixNano()
	containerStats := &runtimeapi.ContainerStats{
		Attributes: &runtimeapi.ContainerAttributes{
			Id:          containerID,
			Metadata:    status.Metadata,
			Labels:      status.Labels,
			Annotations: status.Annotations,
		},
		Cpu: &runtimeapi.CpuUsage{
			Timestamp: timestamp,
			// have to multiply cpu usage by 100 since stats units is in 100's of nano seconds for Windows
			UsageCoreNanoSeconds: &runtimeapi.UInt64Value{Value: stats.Processor.TotalRuntime100ns * 100},
		},
		Memory: &runtimeapi.MemoryUsage{
			Timestamp:       timestamp,
			WorkingSetBytes: &runtimeapi.UInt64Value{Value: stats.Memory.UsagePrivateWorkingSetBytes},
		},
		WritableLayer: &runtimeapi.FilesystemUsage{
			Timestamp: timestamp,
			FsId:      &runtimeapi.FilesystemIdentifier{Mountpoint: info.DockerRootDir},
			UsedBytes: &runtimeapi.UInt64Value{Value: uint64(*containerJSON.SizeRw)},
		},
	}
	return containerStats, nil
}
