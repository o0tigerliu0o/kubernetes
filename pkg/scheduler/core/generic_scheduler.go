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

package core

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	corelisters "k8s.io/client-go/listers/core/v1"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"
	utiltrace "k8s.io/utils/trace"
	podutil "kubernetes/pkg/api/v1/pod"
	"kubernetes/pkg/features"
	"kubernetes/pkg/scheduler/framework/runtime"
	framework "kubernetes/pkg/scheduler/framework/v1alpha1"
	internalcache "kubernetes/pkg/scheduler/internal/cache"
	"kubernetes/pkg/scheduler/internal/parallelize"
	"kubernetes/pkg/scheduler/metrics"
	"kubernetes/pkg/scheduler/profile"
	"kubernetes/pkg/scheduler/util"
)

const (
	// minFeasibleNodesToFind is the minimum number of nodes that would be scored
	// in each scheduling cycle. This is a semi-arbitrary value to ensure that a
	// certain minimum of nodes are checked for feasibility. This in turn helps
	// ensure a minimum level of spreading.
	minFeasibleNodesToFind = 100
	// minFeasibleNodesPercentageToFind is the minimum percentage of nodes that
	// would be scored in each scheduling cycle. This is a semi-arbitrary value
	// to ensure that a certain minimum of nodes are checked for feasibility.
	// This in turn helps ensure a minimum level of spreading.
	minFeasibleNodesPercentageToFind = 5
)

// FitError describes a fit error of a pod.
type FitError struct {
	Pod                   *v1.Pod
	NumAllNodes           int
	FilteredNodesStatuses framework.NodeToStatusMap
}

// ErrNoNodesAvailable is used to describe the error that no nodes available to schedule pods.
var ErrNoNodesAvailable = fmt.Errorf("no nodes available to schedule pods")

const (
	// NoNodeAvailableMsg is used to format message when no nodes available.
	NoNodeAvailableMsg = "0/%v nodes are available"
)

// Error returns detailed information of why the pod failed to fit on each node
func (f *FitError) Error() string {
	reasons := make(map[string]int)
	for _, status := range f.FilteredNodesStatuses {
		for _, reason := range status.Reasons() {
			reasons[reason]++
		}
	}

	sortReasonsHistogram := func() []string {
		var reasonStrings []string
		for k, v := range reasons {
			reasonStrings = append(reasonStrings, fmt.Sprintf("%v %v", v, k))
		}
		sort.Strings(reasonStrings)
		return reasonStrings
	}
	reasonMsg := fmt.Sprintf(NoNodeAvailableMsg+": %v.", f.NumAllNodes, strings.Join(sortReasonsHistogram(), ", "))
	return reasonMsg
}

// ScheduleAlgorithm is an interface implemented by things that know how to schedule pods
// onto machines.
// TODO: Rename this type.
type ScheduleAlgorithm interface {
	Schedule(context.Context, *profile.Profile, *framework.CycleState, *v1.Pod) (scheduleResult ScheduleResult, err error)
	// Extenders returns a slice of extender config. This is exposed for
	// testing.
	Extenders() []framework.Extender
}

// ScheduleResult represents the result of one pod scheduled. It will contain
// the final selected Node, along with the selected intermediate information.
type ScheduleResult struct {
	// Name of the scheduler suggest host
	SuggestedHost string
	// Number of nodes scheduler evaluated on one pod scheduled
	EvaluatedNodes int
	// Number of feasible nodes on one pod scheduled
	FeasibleNodes int
}

type genericScheduler struct {
	cache                    internalcache.Cache
	extenders                []framework.Extender
	nodeInfoSnapshot         *internalcache.Snapshot
	pvcLister                corelisters.PersistentVolumeClaimLister
	disablePreemption        bool
	percentageOfNodesToScore int32
	nextStartNodeIndex       int
}

// snapshot snapshots scheduler cache and node infos for all fit and priority
// functions.
func (g *genericScheduler) snapshot() error {
	// Used for all fit and priority funcs.
	return g.cache.UpdateSnapshot(g.nodeInfoSnapshot)
}

// Schedule tries to schedule the given pod to one of the nodes in the node list.
// If it succeeds, it will return the name of the node.
// If it fails, it will return a FitError error with reasons.
/*
1、对pod进行校验，检查是否声明了pvc，以及对应的pvc是否已经被删除等；
2、调用findNodesThatFitPod方法，负责选出一系列符合条件的节点；
3、如果没有找到节点或唯一节点，那么直接返回；
4、如果找到的节点数超过1，那么需要调用prioritizeNodes方法，进行打分排序；
5、最后调用selectHost选出合适的唯一节点，并返回。
*/
func (g *genericScheduler) Schedule(ctx context.Context, prof *profile.Profile, state *framework.CycleState, pod *v1.Pod) (result ScheduleResult, err error) {
	trace := utiltrace.New("Scheduling", utiltrace.Field{Key: "namespace", Value: pod.Namespace}, utiltrace.Field{Key: "name", Value: pod.Name})
	defer trace.LogIfLong(100 * time.Millisecond)
	// 1、检查pod上声明的pvc，包括pvc是否存在，是否已被删除等
	if err := podPassesBasicChecks(pod, g.pvcLister); err != nil {
		return result, err
	}
	trace.Step("Basic checks done")

	// 2、 生成这个时刻的snapshot,主要是这个时刻的 nodeInfo map 生成一份快照
	if err := g.snapshot(); err != nil {
		return result, err
	}
	trace.Step("Snapshotting scheduler cache and node infos done")

	if g.nodeInfoSnapshot.NumNodes() == 0 {
		return result, ErrNoNodesAvailable
	}

	startPredicateEvalTime := time.Now()

	// 3、预选，这里是Predicates部分的逻辑，负责选出一系列符合条件的节点
	// 起多个 goroutine 去看 nodeInfo 合不合适，NodeAffinity 也是在这里检查，返回预选 nodes 列表
	feasibleNodes, filteredNodesStatuses, err := g.findNodesThatFitPod(ctx, prof, state, pod)
	if err != nil {
		return result, err
	}
	trace.Step("Computing predicates done")
	// 表示没有找到合适的节点
	if len(feasibleNodes) == 0 {
		return result, &FitError{
			Pod:                   pod,
			NumAllNodes:           g.nodeInfoSnapshot.NumNodes(),
			FilteredNodesStatuses: filteredNodesStatuses,
		}
	}

	metrics.DeprecatedSchedulingAlgorithmPredicateEvaluationSecondsDuration.Observe(metrics.SinceInSeconds(startPredicateEvalTime))
	metrics.DeprecatedSchedulingDuration.WithLabelValues(metrics.PredicateEvaluation).Observe(metrics.SinceInSeconds(startPredicateEvalTime))

	startPriorityEvalTime := time.Now()
	// When only one node after predicate, just use it.
	// 如果只有一个 node 合适就不会进入优选环节，直接返回
	if len(feasibleNodes) == 1 {
		metrics.DeprecatedSchedulingAlgorithmPriorityEvaluationSecondsDuration.Observe(metrics.SinceInSeconds(startPriorityEvalTime))
		return ScheduleResult{
			SuggestedHost:  feasibleNodes[0].Name,
			EvaluatedNodes: 1 + len(filteredNodesStatuses),
			FeasibleNodes:  1,
		}, nil
	}
	// 4、优选，如果节点不是唯一，那么需要进行打分排序
	// 起多个 goroutine 去尝试把待调度的 pod 放到 node 上，
	// 计算每个预选的 nodes 的得分，这里会看 node 上现有的 podInfo，podAffinity 也是在这里检查
	priorityList, err := g.prioritizeNodes(ctx, prof, state, pod, feasibleNodes)
	if err != nil {
		return result, err
	}

	metrics.DeprecatedSchedulingAlgorithmPriorityEvaluationSecondsDuration.Observe(metrics.SinceInSeconds(startPriorityEvalTime))
	metrics.DeprecatedSchedulingDuration.WithLabelValues(metrics.PriorityEvaluation).Observe(metrics.SinceInSeconds(startPriorityEvalTime))
	// 5、选择最佳的节点，每个nodes 的得分汇总起来给 selectHost 选出一个合适的 nodes
	host, err := g.selectHost(priorityList)
	trace.Step("Prioritizing done")

	return ScheduleResult{
		SuggestedHost:  host,
		EvaluatedNodes: len(feasibleNodes) + len(filteredNodesStatuses),
		FeasibleNodes:  len(feasibleNodes),
	}, err
}

func (g *genericScheduler) Extenders() []framework.Extender {
	return g.extenders
}

// selectHost takes a prioritized list of nodes and then picks one
// in a reservoir sampling manner from the nodes that had the highest score.
// 就是挑选分数高的，如果分数相同，那么则随机挑选一个
func (g *genericScheduler) selectHost(nodeScoreList framework.NodeScoreList) (string, error) {
	if len(nodeScoreList) == 0 {
		return "", fmt.Errorf("empty priorityList")
	}
	maxScore := nodeScoreList[0].Score
	selected := nodeScoreList[0].Name
	cntOfMaxScore := 1
	for _, ns := range nodeScoreList[1:] {
		if ns.Score > maxScore {
			maxScore = ns.Score
			selected = ns.Name
			cntOfMaxScore = 1
		} else if ns.Score == maxScore {
			cntOfMaxScore++
			if rand.Intn(cntOfMaxScore) == 0 {
				// Replace the candidate with probability of 1/cntOfMaxScore
				selected = ns.Name
			}
		}
	}
	return selected, nil
}

// numFeasibleNodesToFind returns the number of feasible nodes that once found, the scheduler stops
// its search for more feasible nodes.
func (g *genericScheduler) numFeasibleNodesToFind(numAllNodes int32) (numNodes int32) {
	// 对于一个小于100的节点，全部节点参与调度
	// percentageOfNodesToScore参数值是一个集群中所有节点的百分比，范围是1和100之间，0表示不启用
	if numAllNodes < minFeasibleNodesToFind || g.percentageOfNodesToScore >= 100 {
		return numAllNodes
	}

	/*
		percentageOfNodesToScore参数值是一个集群中所有节点的百分比，范围是1和100之间，0表示不启用。
		如果集群节点数大于100，那么就会根据这个值来计算让合适的节点数参与调度。
		如果一个5000个节点的集群，percentageOfNodesToScore会默认设置为10%，也就是500个节点参与调度。
		因为如果一个5000节点的集群来进行调度的话，不进行控制时，每个pod调度都需要尝试5000次的节点预选过程时非常消耗资源的。
	*/
	adaptivePercentage := g.percentageOfNodesToScore
	// 当numAllNodes大于100时，如果没有设置percentageOfNodesToScore，那么这里需要计算出一个值
	if adaptivePercentage <= 0 {
		basePercentageOfNodesToScore := int32(50)
		adaptivePercentage = basePercentageOfNodesToScore - numAllNodes/125
		if adaptivePercentage < minFeasibleNodesPercentageToFind {
			adaptivePercentage = minFeasibleNodesPercentageToFind
		}
	}

	numNodes = numAllNodes * adaptivePercentage / 100
	if numNodes < minFeasibleNodesToFind {
		return minFeasibleNodesToFind
	}

	return numNodes
}

// Filters the nodes to find the ones that fit the pod based on the framework
// filter plugins and filter extenders.
func (g *genericScheduler) findNodesThatFitPod(ctx context.Context, prof *profile.Profile, state *framework.CycleState, pod *v1.Pod) ([]*v1.Node, framework.NodeToStatusMap, error) {
	filteredNodesStatuses := make(framework.NodeToStatusMap)

	// Run "prefilter" plugins.
	// 前置过滤插件用于预处理 Pod 的相关信息，或者检查集群或 Pod 必须满足的某些条件。
	// 如果 PreFilter 插件返回错误，则调度周期将终止
	// 1、运行PreFilter逻辑
	s := prof.RunPreFilterPlugins(ctx, state, pod)
	if !s.IsSuccess() {
		if !s.IsUnschedulable() {
			return nil, nil, s.AsError()
		}
		// All nodes will have the same status. Some non trivial refactoring is
		// needed to avoid this copy.
		allNodes, err := g.nodeInfoSnapshot.NodeInfos().List()
		if err != nil {
			return nil, nil, err
		}
		for _, n := range allNodes {
			filteredNodesStatuses[n.Node().Name] = s
		}
		return nil, filteredNodesStatuses, nil

	}

	// 2、过滤掉不符合条件的node
	feasibleNodes, err := g.findNodesThatPassFilters(ctx, prof, state, pod, filteredNodesStatuses)
	if err != nil {
		return nil, nil, err
	}
	// SchdulerExtender是kubernets外部扩展方式，用户可以根据需求独立构建调度服务
	// 3、运行 extender 过滤逻辑
	feasibleNodes, err = g.findNodesThatPassExtenders(pod, feasibleNodes, filteredNodesStatuses)
	if err != nil {
		return nil, nil, err
	}
	return feasibleNodes, filteredNodesStatuses, nil
}

// findNodesThatPassFilters finds the nodes that fit the filter plugins.
func (g *genericScheduler) findNodesThatPassFilters(ctx context.Context, prof *profile.Profile, state *framework.CycleState, pod *v1.Pod, statuses framework.NodeToStatusMap) ([]*v1.Node, error) {
	allNodes, err := g.nodeInfoSnapshot.NodeInfos().List()
	if err != nil {
		return nil, err
	}

	// 1、根据集群节点数量选择参与调度的节点的数量
	numNodesToFind := g.numFeasibleNodesToFind(int32(len(allNodes)))

	// Create feasible list with enough space to avoid growing it
	// and allow assigning.
	feasibleNodes := make([]*v1.Node, numNodesToFind)

	// 如果没有 filterPlugins, 直接返回所有的 nodes
	if !prof.HasFilterPlugins() {
		length := len(allNodes)
		for i := range feasibleNodes {
			feasibleNodes[i] = allNodes[(g.nextStartNodeIndex+i)%length].Node()
		}
		g.nextStartNodeIndex = (g.nextStartNodeIndex + len(feasibleNodes)) % length
		return feasibleNodes, nil
	}

	errCh := parallelize.NewErrorChannel()
	var statusesLock sync.Mutex
	var feasibleNodesLen int32
	ctx, cancel := context.WithCancel(ctx)
	// 2、定义预选函数体
	checkNode := func(i int) {
		// We check the nodes starting from where we left off in the previous scheduling cycle,
		// this is to make sure all nodes have the same chance of being examined across pods.
		// 我们从上一个调度周期中离开的节点开始检查节点，以确保所有节点在Pod中被检查的机会相同
		nodeInfo := allNodes[(g.nextStartNodeIndex+i)%len(allNodes)]
		fits, status, err := PodPassesFiltersOnNode(ctx, prof.PreemptHandle(), state, pod, nodeInfo)
		if err != nil {
			errCh.SendErrorWithCancel(err, cancel)
			return
		}
		// 如果该节点合适，那么放入到feasibleNodes列表中
		if fits {
			length := atomic.AddInt32(&feasibleNodesLen, 1)
			if length > numNodesToFind {
				cancel()
				atomic.AddInt32(&feasibleNodesLen, -1)
			} else {
				feasibleNodes[length-1] = nodeInfo.Node()
			}
		} else {
			statusesLock.Lock()
			if !status.IsSuccess() {
				statuses[nodeInfo.Node().Name] = status
			}
			statusesLock.Unlock()
		}
	}

	beginCheckNode := time.Now()
	statusCode := framework.Success
	defer func() {
		// We record Filter extension point latency here instead of in framework.go because framework.RunFilterPlugins
		// function is called for each node, whereas we want to have an overall latency for all nodes per scheduling cycle.
		// Note that this latency also includes latency for `addNominatedPods`, which calls framework.RunPreFilterAddPod.
		metrics.FrameworkExtensionPointDuration.WithLabelValues(runtime.Filter, statusCode.String(), prof.Name).Observe(metrics.SinceInSeconds(beginCheckNode))
	}()

	// Stops searching for more nodes once the configured number of feasible nodes
	// are found.
	// 3、 启动16个 goroutine 并行预选
	parallelize.Until(ctx, len(allNodes), checkNode)
	processedNodes := int(feasibleNodesLen) + len(statuses)

	g.nextStartNodeIndex = (g.nextStartNodeIndex + processedNodes) % len(allNodes)
	// 4、如果该节点合适，那么放入到feasibleNodes列表中
	feasibleNodes = feasibleNodes[:feasibleNodesLen]
	if err := errCh.ReceiveError(); err != nil {
		statusCode = framework.Error
		return nil, err
	}
	return feasibleNodes, nil
}

func (g *genericScheduler) findNodesThatPassExtenders(pod *v1.Pod, feasibleNodes []*v1.Node, statuses framework.NodeToStatusMap) ([]*v1.Node, error) {
	for _, extender := range g.extenders {
		if len(feasibleNodes) == 0 {
			break
		}
		if !extender.IsInterested(pod) {
			continue
		}
		feasibleList, failedMap, err := extender.Filter(pod, feasibleNodes)
		if err != nil {
			if extender.IsIgnorable() {
				klog.Warningf("Skipping extender %v as it returned error %v and has ignorable flag set",
					extender, err)
				continue
			}
			return nil, err
		}

		for failedNodeName, failedMsg := range failedMap {
			if _, found := statuses[failedNodeName]; !found {
				statuses[failedNodeName] = framework.NewStatus(framework.Unschedulable, failedMsg)
			} else {
				statuses[failedNodeName].AppendReason(failedMsg)
			}
		}
		feasibleNodes = feasibleList
	}
	return feasibleNodes, nil
}

// addNominatedPods adds pods with equal or greater priority which are nominated
// to run on the node. It returns 1) whether any pod was added, 2) augmented cycleState,
// 3) augmented nodeInfo.
// 在addNominatedPods中，会将node上的nominatedPod列举出来，并选出比将要调度的pod Priority相等或者更高的pod，
// 然后再执行RunPreFilterExtensionAddPod，如果所有的结果都为true，则返回true
func addNominatedPods(ctx context.Context, ph framework.PreemptHandle, pod *v1.Pod, state *framework.CycleState, nodeInfo *framework.NodeInfo) (bool, *framework.CycleState, *framework.NodeInfo, error) {
	if ph == nil || nodeInfo == nil || nodeInfo.Node() == nil {
		// This may happen only in tests.
		return false, state, nodeInfo, nil
	}
	nominatedPods := ph.NominatedPodsForNode(nodeInfo.Node().Name)
	if len(nominatedPods) == 0 {
		return false, state, nodeInfo, nil
	}
	nodeInfoOut := nodeInfo.Clone()
	stateOut := state.Clone()
	podsAdded := false
	for _, p := range nominatedPods {
		if podutil.GetPodPriority(p) >= podutil.GetPodPriority(pod) && p.UID != pod.UID {
			nodeInfoOut.AddPod(p)
			status := ph.RunPreFilterExtensionAddPod(ctx, stateOut, pod, p, nodeInfoOut)
			if !status.IsSuccess() {
				return false, state, nodeInfo, status.AsError()
			}
			podsAdded = true
		}
	}
	return podsAdded, stateOut, nodeInfoOut, nil
}

// PodPassesFiltersOnNode checks whether a node given by NodeInfo satisfies the
// filter plugins.
// This function is called from two different places: Schedule and Preempt.
// When it is called from Schedule, we want to test whether the pod is
// schedulable on the node with all the existing pods on the node plus higher
// and equal priority pods nominated to run on the node.
// When it is called from Preempt, we should remove the victims of preemption
// and add the nominated pods. Removal of the victims is done by
// SelectVictimsOnNode(). Preempt removes victims from PreFilter state and
// NodeInfo before calling this function.
// TODO: move this out so that plugins don't need to depend on <core> pkg.
// 这个函数会被filter和Preempt两个阶段调用
// 第filter阶段会检查这个pod是否满足已经存在node节点上的pods的需要，
// 并且满足节点相等或更高优先级的nominated pods满足filter需求。
// 在Preempt阶段，将会去掉将要驱逐的pods,并加上nominated pods。
// 在这里会执行两遍逻辑，其中主要是为了检查nominated pods存在与否产生的不同结果
func PodPassesFiltersOnNode(
	ctx context.Context,
	ph framework.PreemptHandle,
	state *framework.CycleState,
	pod *v1.Pod,
	info *framework.NodeInfo,
) (bool, *framework.Status, error) {
	var status *framework.Status

	podsAdded := false
	// We run filters twice in some cases. If the node has greater or equal priority
	// nominated pods, we run them when those pods are added to PreFilter state and nodeInfo.
	// If all filters succeed in this pass, we run them again when these
	// nominated pods are not added. This second pass is necessary because some
	// filters such as inter-pod affinity may not pass without the nominated pods.
	// If there are no nominated pods for the node or if the first run of the
	// filters fail, we don't run the second pass.
	// We consider only equal or higher priority pods in the first pass, because
	// those are the current "pod" must yield to them and not take a space opened
	// for running them. It is ok if the current "pod" take resources freed for
	// lower priority pods.
	// Requiring that the new pod is schedulable in both circumstances ensures that
	// we are making a conservative decision: filters like resources and inter-pod
	// anti-affinity are more likely to fail when the nominated pods are treated
	// as running, while filters like pod affinity are more likely to fail when
	// the nominated pods are treated as not running. We can't just assume the
	// nominated pods are running because they are not running right now and in fact,
	// they may end up getting scheduled to a different node.
	//待检查的 Node 是一个即将被抢占的节点，调度器就会对这个 Node ，将同样的 Predicates 算法运行两遍。
	for i := 0; i < 2; i++ {
		stateToUse := state
		nodeInfoToUse := info
		// 处理优先级pod的逻辑
		if i == 0 {
			var err error
			// 查找在schedulerQueue中是否有优先级大于或等于当前pod的NominatedPods，然后加入到nodeInfoToUse中
			podsAdded, stateToUse, nodeInfoToUse, err = addNominatedPods(ctx, ph, pod, state, info)
			if err != nil {
				return false, nil, err
			}
		// 满足以下任意条件循环仅执行一次：
		// 1、!podsAdded：node上没有比正在调度的pod优先级更高的pod
		// 2、!status.IsSuccess() pod不满足部分plugin的filter策略
		} else if !podsAdded || !status.IsSuccess() {
			break
		}
		// 运行过滤器检查该pod是否能运行在该节点上
		statusMap := ph.RunFilterPlugins(ctx, stateToUse, pod, nodeInfoToUse)
		status = statusMap.Merge()
		if !status.IsSuccess() && !status.IsUnschedulable() {
			return false, status, status.AsError()
		}
	}

	return status.IsSuccess(), status, nil
}

// prioritizeNodes prioritizes the nodes by running the score plugins,
// which return a score for each node from the call to RunScorePlugins().
// The scores from each plugin are added together to make the score for that node, then
// any extenders are run as well.
// All scores are finally combined (added) to get the total weighted scores of all nodes
func (g *genericScheduler) prioritizeNodes(
	ctx context.Context,
	prof *profile.Profile,
	state *framework.CycleState,
	pod *v1.Pod,
	nodes []*v1.Node,
) (framework.NodeScoreList, error) {
	// If no priority configs are provided, then all nodes will have a score of one.
	// This is required to generate the priority list in the required format
	if len(g.extenders) == 0 && !prof.HasScorePlugins() {
		result := make(framework.NodeScoreList, 0, len(nodes))
		for i := range nodes {
			result = append(result, framework.NodeScore{
				Name:  nodes[i].Name,
				Score: 1,
			})
		}
		return result, nil
	}

	// Run PreScore plugins.
	preScoreStatus := prof.RunPreScorePlugins(ctx, state, pod, nodes)
	if !preScoreStatus.IsSuccess() {
		return nil, preScoreStatus.AsError()
	}

	// Run the Score plugins.
	// 遍历插件的方式为node打分。然后遍历scoresMap将结果按照node维度进行聚合
	// podPassesFiltersOnNode，返回的 scoreMap key 是 plugin,
	// 元素是一个 nodescore list，表示对每个节点的评分。
	scoresMap, scoreStatus := prof.RunScorePlugins(ctx, state, pod, nodes)
	if !scoreStatus.IsSuccess() {
		return nil, scoreStatus.AsError()
	}

	if klog.V(10).Enabled() {
		for plugin, nodeScoreList := range scoresMap {
			klog.Infof("Plugin %s scores on %v/%v => %v", plugin, pod.Namespace, pod.Name, nodeScoreList)
		}
	}

	// Summarize all scores.
	result := make(framework.NodeScoreList, 0, len(nodes))
	// 将分数按照node维度进行汇总
	for i := range nodes {
		result = append(result, framework.NodeScore{Name: nodes[i].Name, Score: 0})
		for j := range scoresMap {
			result[i].Score += scoresMap[j][i].Score
		}
	}

	// 不同的extender 使用不同的goroutine 调用Prioritize函数
	if len(g.extenders) != 0 && nodes != nil {
		var mu sync.Mutex
		var wg sync.WaitGroup
		combinedScores := make(map[string]int64, len(nodes))
		for i := range g.extenders {
			if !g.extenders[i].IsInterested(pod) {
				continue
			}
			wg.Add(1)
			go func(extIndex int) {
				metrics.SchedulerGoroutines.WithLabelValues("prioritizing_extender").Inc()
				defer func() {
					metrics.SchedulerGoroutines.WithLabelValues("prioritizing_extender").Dec()
					wg.Done()
				}()
				prioritizedList, weight, err := g.extenders[extIndex].Prioritize(pod, nodes)
				if err != nil {
					// Prioritization errors from extender can be ignored, let k8s/other extenders determine the priorities
					return
				}
				mu.Lock()
				for i := range *prioritizedList {
					host, score := (*prioritizedList)[i].Host, (*prioritizedList)[i].Score
					if klog.V(10).Enabled() {
						klog.Infof("%v -> %v: %v, Score: (%d)", util.GetPodFullName(pod), host, g.extenders[extIndex].Name(), score)
					}
					combinedScores[host] += score * weight
				}
				mu.Unlock()
			}(i)
		}
		// wait for all go routines to finish
		wg.Wait()
		for i := range result {
			// MaxExtenderPriority may diverge from the max priority used in the scheduler and defined by MaxNodeScore,
			// therefore we need to scale the score returned by extenders to the score range used by the scheduler.
			result[i].Score += combinedScores[result[i].Name] * (framework.MaxNodeScore / extenderv1.MaxExtenderPriority)
		}
	}

	if klog.V(10).Enabled() {
		for i := range result {
			klog.Infof("Host %s => Score %d", result[i].Name, result[i].Score)
		}
	}
	return result, nil
}

// podPassesBasicChecks makes sanity checks on the pod if it can be scheduled.
func podPassesBasicChecks(pod *v1.Pod, pvcLister corelisters.PersistentVolumeClaimLister) error {
	// Check PVCs used by the pod
	namespace := pod.Namespace
	manifest := &(pod.Spec)
	for i := range manifest.Volumes {
		volume := &manifest.Volumes[i]
		var pvcName string
		ephemeral := false
		switch {
		case volume.PersistentVolumeClaim != nil:
			pvcName = volume.PersistentVolumeClaim.ClaimName
		case volume.Ephemeral != nil &&
			utilfeature.DefaultFeatureGate.Enabled(features.GenericEphemeralVolume):
			pvcName = pod.Name + "-" + volume.Name
			ephemeral = true
		default:
			// Volume is not using a PVC, ignore
			continue
		}
		pvc, err := pvcLister.PersistentVolumeClaims(namespace).Get(pvcName)
		if err != nil {
			// The error has already enough context ("persistentvolumeclaim "myclaim" not found")
			return err
		}

		if pvc.DeletionTimestamp != nil {
			return fmt.Errorf("persistentvolumeclaim %q is being deleted", pvc.Name)
		}

		if ephemeral &&
			!metav1.IsControlledBy(pvc, pod) {
			return fmt.Errorf("persistentvolumeclaim %q was not created for the pod", pvc.Name)
		}
	}

	return nil
}

// NewGenericScheduler creates a genericScheduler object.
func NewGenericScheduler(
	cache internalcache.Cache,
	nodeInfoSnapshot *internalcache.Snapshot,
	extenders []framework.Extender,
	pvcLister corelisters.PersistentVolumeClaimLister,
	disablePreemption bool,
	percentageOfNodesToScore int32) ScheduleAlgorithm {
	return &genericScheduler{
		cache:                    cache,
		extenders:                extenders,
		nodeInfoSnapshot:         nodeInfoSnapshot,
		pvcLister:                pvcLister,
		disablePreemption:        disablePreemption,
		percentageOfNodesToScore: percentageOfNodesToScore,
	}
}
