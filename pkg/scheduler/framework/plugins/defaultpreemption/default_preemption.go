/*
Copyright 2020 The Kubernetes Authors.

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

package defaultpreemption

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"k8s.io/klog/v2"

	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	policylisters "k8s.io/client-go/listers/policy/v1beta1"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"
	podutil "kubernetes/pkg/api/v1/pod"
	kubefeatures "kubernetes/pkg/features"
	"kubernetes/pkg/scheduler/core"
	framework "kubernetes/pkg/scheduler/framework/v1alpha1"
	"kubernetes/pkg/scheduler/internal/parallelize"
	"kubernetes/pkg/scheduler/metrics"
	"kubernetes/pkg/scheduler/util"
)

const (
	// Name of the plugin used in the plugin registry and configurations.
	Name = "DefaultPreemption"
)

// DefaultPreemption is a PostFilter plugin implements the preemption logic.
type DefaultPreemption struct {
	fh        framework.FrameworkHandle
	pdbLister policylisters.PodDisruptionBudgetLister
}

var _ framework.PostFilterPlugin = &DefaultPreemption{}

// Name returns name of the plugin. It is used in logs, etc.
func (pl *DefaultPreemption) Name() string {
	return Name
}

// New initializes a new plugin and returns it.
func New(_ runtime.Object, fh framework.FrameworkHandle) (framework.Plugin, error) {
	pl := DefaultPreemption{
		fh:        fh,
		pdbLister: getPDBLister(fh.SharedInformerFactory()),
	}
	return &pl, nil
}

// PostFilter invoked at the postFilter extension point.
func (pl *DefaultPreemption) PostFilter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, m framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	preemptionStartTime := time.Now()
	defer func() {
		metrics.PreemptionAttempts.Inc()
		metrics.SchedulingAlgorithmPreemptionEvaluationDuration.Observe(metrics.SinceInSeconds(preemptionStartTime))
		metrics.DeprecatedSchedulingDuration.WithLabelValues(metrics.PreemptionEvaluation).Observe(metrics.SinceInSeconds(preemptionStartTime))
	}()

	nnn, err := pl.preempt(ctx, state, pod, m)
	if err != nil {
		return nil, framework.NewStatus(framework.Error, err.Error())
	}
	if nnn == "" {
		return nil, framework.NewStatus(framework.Unschedulable)
	}
	return &framework.PostFilterResult{NominatedNodeName: nnn}, framework.NewStatus(framework.Success)
}

// preempt finds nodes with pods that can be preempted to make room for "pod" to
// schedule. It chooses one of the nodes and preempts the pods on the node and
// returns 1) the node name which is picked up for preemption, 2) any possible error.
// preempt does not update its snapshot. It uses the same snapshot used in the
// scheduling cycle. This is to avoid a scenario where preempt finds feasible
// nodes without preempting any pod. When there are many pending pods in the
// scheduling queue a nominated pod will go back to the queue and behind
// other pods with the same priority. The nominated pod prevents other pods from
// using the nominated resources and the nominated pod could take a long time
// before it is retried after many other pending pods.
/*
	1、调用PodEligibleToPreemptOthers方法，检查抢占者是否能够进行抢占，
       如果当前的pod已经抢占了一个node节点或者在被抢占node节点中有pod正在执行优雅退出，那么不应该执行抢占；
	2、调用FindCandidates找到所有node中能被抢占的node节点，并返回候选列表以及node节点中需要被删除的pod；
	3、若有 extender 则执行CallExtenders；
	4、调用SelectCandidate方法在所有候选列表中找出最合适的node节点执行抢占；
	5、调用PrepareCandidate方法删除被抢占的node节点中victim（牺牲者），以及清除NominatedNodeName字段信息；
*/
func (pl *DefaultPreemption) preempt(ctx context.Context, state *framework.CycleState, pod *v1.Pod, m framework.NodeToStatusMap) (string, error) {
	cs := pl.fh.ClientSet()
	ph := pl.fh.PreemptHandle()
	// 获取informer中通过list/watch监听node资源变更的listener
	nodeLister := pl.fh.SnapshotSharedLister().NodeInfos()

	// 0) Fetch the latest version of <pod>.
	// TODO(Huang-Wei): get pod from informer cache instead of API server.
	// 从apiserver获取最新的pod信息（保证新的改动可以被接收到）
	pod, err := util.GetUpdatedPod(cs, pod)
	if err != nil {
		klog.Errorf("Error getting the updated preemptor pod object: %v", err)
		return "", err
	}

	// 1) Ensure the preemptor is eligible to preempt other pods.
	// 确认该pod 适不适合执行抢占，如果这个pod 有可能已经抢占过了或被抢占的pod正在优雅退出（Graceful Termination ），就不会发生抢占
	if !PodEligibleToPreemptOthers(pod, nodeLister, m[pod.Status.NominatedNodeName]) {
		klog.V(5).Infof("Pod %v/%v is not eligible for more preemption.", pod.Namespace, pod.Name)
		return "", nil
	}

	// 2) Find all preemption candidates.
	// 用于找到合适被抢占的候选nodes
	candidates, err := FindCandidates(ctx, cs, state, pod, m, ph, nodeLister, pl.pdbLister)
	if err != nil || len(candidates) == 0 {
		return "", err
	}

	// 3) Interact with registered Extenders to filter out some candidates if needed.
	// 若有 extender 则执行
	candidates, err = CallExtenders(ph.Extenders(), pod, nodeLister, candidates)
	if err != nil {
		return "", err
	}

	// 4) Find the best candidate.
	// 选择最好的候选node，有一系列判断准则：
	//   这个 node 是不是满足 Pod 干扰预算的条件。
	//   这个 node 是不是只需要驱逐的pod 的优先级是不是最小的
	//   驱逐这些pod 这个 node affinity 有没有被影响
	//   是不是驱逐的 pod 数目最小
	// 这个方法里面会调用candidatesToVictimsMap方法做一个name和victims映射map，然后调用pickOneNodeForPreemption执行主要过滤逻辑
	bestCandidate := SelectCandidate(candidates)
	if bestCandidate == nil || len(bestCandidate.Name()) == 0 {
		return "", nil
	}

	// 5) Perform preparation work before nominating the selected candidate.
	// 执行抢占前准备工作，在Candidate node上驱逐victims pod
	if err := PrepareCandidate(bestCandidate, pl.fh, cs, pod); err != nil {
		return "", err
	}

	return bestCandidate.Name(), nil
}

// FindCandidates calculates a slice of preemption candidates.
// Each candidate is executable to make the given <pod> schedulable.
// FindCandidates方法首先会获取node列表，然后调用nodesWherePreemptionMightHelp方法来找出predicates 阶段失败但是通过抢占也许能够调度成功的nodes，
// 因为并不是所有的node都可以通过抢占来调度成功。最后调用dryRunPreemption方法来获取符合条件的node节点
func FindCandidates(ctx context.Context, cs kubernetes.Interface, state *framework.CycleState, pod *v1.Pod,
	m framework.NodeToStatusMap, ph framework.PreemptHandle, nodeLister framework.NodeInfoLister,
	pdbLister policylisters.PodDisruptionBudgetLister) ([]Candidate, error) {
	// 1、获取node列表
	allNodes, err := nodeLister.List()
	if err != nil {
		return nil, err
	}
	if len(allNodes) == 0 {
		return nil, core.ErrNoNodesAvailable
	}
	// 将node列表中之前该pod在预选时由于卷绑定、节点亲和性导致预选失败的node剔出
	potentialNodes := nodesWherePreemptionMightHelp(allNodes, m)
	if len(potentialNodes) == 0 {
		klog.V(3).Infof("Preemption will not help schedule pod %v/%v on any node.", pod.Namespace, pod.Name)
		// In this case, we should clean-up any existing nominated node name of the pod.
		if err := util.ClearNominatedNodeName(cs, pod); err != nil {
			klog.Errorf("Cannot clear 'NominatedNodeName' field of pod %v/%v: %v", pod.Namespace, pod.Name, err)
			// We do not return as this error is not critical.
		}
		return nil, nil
	}
	if klog.V(5).Enabled() {
		var sample []string
		for i := 0; i < 10 && i < len(potentialNodes); i++ {
			sample = append(sample, potentialNodes[i].Node().Name)
		}
		klog.Infof("%v potential nodes for preemption, first %v are: %v", len(potentialNodes), len(sample), sample)
	}
	// 获取PDB(PodDisruptionBudget)对象，PDB将限制在同一时间因自愿干扰导致的复制应用程序中宕机的 pod 数量
	// https://kubernetes.io/zh/docs/concepts/workloads/pods/disruptions/
	pdbs, err := getPodDisruptionBudgets(pdbLister)
	if err != nil {
		return nil, err
	}
	// 起多个进程进行跟前面 schedule 里面 checkNode 类似，通过 checkNode 运行 selectVictimsOnNode，
	// 这个函数会尝试找到最小被抢占 pods 的集合（只要低优先级的 pods 够，就不会抢占node 内高优先级的)
	// 返回汇总后的cadidates信息， 包括node 中被抢占 pods 的信息
	return dryRunPreemption(ctx, ph, state, pod, potentialNodes, pdbs), nil
}

// PodEligibleToPreemptOthers determines whether this pod should be considered
// for preempting other pods or not. If this pod has already preempted other
// pods and those are in their graceful termination period, it shouldn't be
// considered for preemption.
// We look at the node that is nominated for this pod and as long as there are
// terminating pods on the node, we don't consider this for preempting more pods.
// 检查该pod是否已经抢占过其他node节点，如果是的话就遍历节点上的所有pod对象，
// 如果发现节点上有pod资源对象的优先级小于待调度pod资源对象并处于终止状态，则返回false，不会发生抢占。
func PodEligibleToPreemptOthers(pod *v1.Pod, nodeInfos framework.NodeInfoLister, nominatedNodeStatus *framework.Status) bool {
	if pod.Spec.PreemptionPolicy != nil && *pod.Spec.PreemptionPolicy == v1.PreemptNever {
		klog.V(5).Infof("Pod %v/%v is not eligible for preemption because it has a preemptionPolicy of %v", pod.Namespace, pod.Name, v1.PreemptNever)
		return false
	}
	// 查看抢占者是否已经抢占过
	nomNodeName := pod.Status.NominatedNodeName
	if len(nomNodeName) > 0 {
		// If the pod's nominated node is considered as UnschedulableAndUnresolvable by the filters,
		// then the pod should be considered for preempting again.
		if nominatedNodeStatus.Code() == framework.UnschedulableAndUnresolvable {
			return true
		}

		if nodeInfo, _ := nodeInfos.Get(nomNodeName); nodeInfo != nil {
			// 查看是否存在正在被删除并且优先级比抢占者pod低的pod
			podPriority := podutil.GetPodPriority(pod)
			for _, p := range nodeInfo.Pods {
				if p.Pod.DeletionTimestamp != nil && podutil.GetPodPriority(p.Pod) < podPriority {
					// There is a terminating pod on the nominated node.
					return false
				}
			}
		}
	}
	return true
}

// nodesWherePreemptionMightHelp returns a list of nodes with failed predicates
// that may be satisfied by removing pods from the node.
func nodesWherePreemptionMightHelp(nodes []*framework.NodeInfo, m framework.NodeToStatusMap) []*framework.NodeInfo {
	var potentialNodes []*framework.NodeInfo
	for _, node := range nodes {
		name := node.Node().Name
		// We reply on the status by each plugin - 'Unschedulable' or 'UnschedulableAndUnresolvable'
		// to determine whether preemption may help or not on the node.
		if m[name].Code() == framework.UnschedulableAndUnresolvable {
			continue
		}
		potentialNodes = append(potentialNodes, node)
	}
	return potentialNodes
}

// dryRunPreemption simulates Preemption logic on <potentialNodes> in parallel,
// and returns all possible preemption candidates.
// 这里会开启16个线程调用checkNode方法，checkNode方法里面会调用selectVictimsOnNode方法来检查这个node是不是能被执行抢占，
// 如果能执行抢占返回的pods表示需要删除的节点，然后封装成candidate添加到candidates列表中返回。
func dryRunPreemption(ctx context.Context, fh framework.PreemptHandle, state *framework.CycleState,
	pod *v1.Pod, potentialNodes []*framework.NodeInfo, pdbs []*policy.PodDisruptionBudget) []Candidate {
	var resultLock sync.Mutex
	var candidates []Candidate
    // 构造抢占检查函数
	checkNode := func(i int) {
		nodeInfoCopy := potentialNodes[i].Clone()
		stateCopy := state.Clone()
		// 尝试找到最小被抢占 pods 的集合（只要低优先级的 pods 够，就不会抢占node 内高优先级的)
		// 找到node上被抢占的pod，也就是victims
		pods, numPDBViolations, fits := selectVictimsOnNode(ctx, fh, stateCopy, pod, nodeInfoCopy, pdbs)
		// 将候选驱逐节点记录到candidates中
		if fits {
			resultLock.Lock()
			victims := extenderv1.Victims{
				Pods:             pods,
				NumPDBViolations: int64(numPDBViolations),
			}
			c := candidate{
				victims: &victims,
				name:    nodeInfoCopy.Node().Name,
			}
			candidates = append(candidates, &c)
			resultLock.Unlock()
		}
	}
	parallelize.Until(ctx, len(potentialNodes), checkNode)
	return candidates
}

// CallExtenders calls given <extenders> to select the list of feasible candidates.
// We will only check <candidates> with extenders that support preemption.
// Extenders which do not support preemption may later prevent preemptor from being scheduled on the nominated
// node. In that case, scheduler will find a different host for the preemptor in subsequent scheduling cycles.
func CallExtenders(extenders []framework.Extender, pod *v1.Pod, nodeLister framework.NodeInfoLister,
	candidates []Candidate) ([]Candidate, error) {
	if len(extenders) == 0 {
		return candidates, nil
	}

	// Migrate candidate slice to victimsMap to adapt to the Extender interface.
	// It's only applicable for candidate slice that have unique nominated node name.
	victimsMap := candidatesToVictimsMap(candidates)
	if len(victimsMap) == 0 {
		return candidates, nil
	}
	for _, extender := range extenders {
		if !extender.SupportsPreemption() || !extender.IsInterested(pod) {
			continue
		}
		nodeNameToVictims, err := extender.ProcessPreemption(pod, victimsMap, nodeLister)
		if err != nil {
			if extender.IsIgnorable() {
				klog.Warningf("Skipping extender %v as it returned error %v and has ignorable flag set",
					extender, err)
				continue
			}
			return nil, err
		}
		// Replace victimsMap with new result after preemption. So the
		// rest of extenders can continue use it as parameter.
		victimsMap = nodeNameToVictims

		// If node list becomes empty, no preemption can happen regardless of other extenders.
		if len(victimsMap) == 0 {
			break
		}
	}

	var newCandidates []Candidate
	for nodeName := range victimsMap {
		newCandidates = append(newCandidates, &candidate{
			victims: victimsMap[nodeName],
			name:    nodeName,
		})
	}
	return newCandidates, nil
}

// This function is not applicable for out-of-tree preemption plugins that exercise
// different preemption candidates on the same nominated node.
func candidatesToVictimsMap(candidates []Candidate) map[string]*extenderv1.Victims {
	m := make(map[string]*extenderv1.Victims)
	for _, c := range candidates {
		m[c.Name()] = c.Victims()
	}
	return m
}

// SelectCandidate chooses the best-fit candidate from given <candidates> and return it.
func SelectCandidate(candidates []Candidate) Candidate {
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	victimsMap := candidatesToVictimsMap(candidates)
	// 选择1个 node 用于 schedule
	candidateNode := pickOneNodeForPreemption(victimsMap)

	// Same as candidatesToVictimsMap, this logic is not applicable for out-of-tree
	// preemption plugins that exercise different candidates on the same nominated node.
	for _, candidate := range candidates {
		if candidateNode == candidate.Name() {
			return candidate
		}
	}
	// We shouldn't reach here.
	klog.Errorf("None candidate can be picked from %v.", candidates)
	// To not break the whole flow, return the first candidate.
	return candidates[0]
}

// pickOneNodeForPreemption chooses one node among the given nodes. It assumes
// pods in each map entry are ordered by decreasing priority.
// It picks a node based on the following criteria:
// 1. A node with minimum number of PDB violations.
// 2. A node with minimum highest priority victim is picked.
// 3. Ties are broken by sum of priorities of all victims.
// 4. If there are still ties, node with the minimum number of victims is picked.
// 5. If there are still ties, node with the latest start time of all highest priority victims is picked.
// 6. If there are still ties, the first such node is picked (sort of randomly).
// The 'minNodes1' and 'minNodes2' are being reused here to save the memory
// allocation and garbage collection time.
/*
 1、找出最少的的PDB violations的node节点，如果找出的node集合大于1则往下走；
 2、找出找到node里面pods 最高优先级最小的node，如果还是找出的node集合大于1则往下走；
 3、找出node里面Victims列表优先级加和最小的，如果还是找出的node集合大于1则往下走；
 4、找到node列表中需要牺牲的pod数量最小的，如果还是找出的node集合大于1则往下走；
 5、若多个 node 的 pod 数量相等，则选出高优先级 pod 启动时间最短的，然后返回。
*/
// 从候选的node中选择最合适的node
func pickOneNodeForPreemption(nodesToVictims map[string]*extenderv1.Victims) string {
	// 若该 node 没有 victims 则返回
	if len(nodesToVictims) == 0 {
		return ""
	}
	minNumPDBViolatingPods := int64(math.MaxInt32)
	var minNodes1 []string
	lenNodes1 := 0
	// 1、寻找 PDB violations 数量最少的 node
	for node, victims := range nodesToVictims {
		numPDBViolatingPods := victims.NumPDBViolations
		if numPDBViolatingPods < minNumPDBViolatingPods {
			minNumPDBViolatingPods = numPDBViolatingPods
			minNodes1 = nil
			lenNodes1 = 0
		}
		if numPDBViolatingPods == minNumPDBViolatingPods {
			minNodes1 = append(minNodes1, node)
			lenNodes1++
		}
	}
	// 如果最小的node只有一个，直接返回
	if lenNodes1 == 1 {
		return minNodes1[0]
	}

	// There are more than one node with minimum number PDB violating pods. Find
	// the one with minimum highest priority victim.
	minHighestPriority := int32(math.MaxInt32)
	var minNodes2 = make([]string, lenNodes1)
	lenNodes2 := 0
	// 2、找到node里面pods 最高优先级最小的
	for i := 0; i < lenNodes1; i++ {
		node := minNodes1[i]
		victims := nodesToVictims[node]
		// highestPodPriority is the highest priority among the victims on this node.
		highestPodPriority := podutil.GetPodPriority(victims.Pods[0])
		if highestPodPriority < minHighestPriority {
			minHighestPriority = highestPodPriority
			lenNodes2 = 0
		}
		if highestPodPriority == minHighestPriority {
			minNodes2[lenNodes2] = node
			lenNodes2++
		}
	}
	if lenNodes2 == 1 {
		return minNodes2[0]
	}

	// There are a few nodes with minimum highest priority victim. Find the
	// smallest sum of priorities.
	// 3、找出node里面Victims列表优先级加和最小的
	minSumPriorities := int64(math.MaxInt64)
	lenNodes1 = 0
	for i := 0; i < lenNodes2; i++ {
		var sumPriorities int64
		node := minNodes2[i]
		for _, pod := range nodesToVictims[node].Pods {
			// We add MaxInt32+1 to all priorities to make all of them >= 0. This is
			// needed so that a node with a few pods with negative priority is not
			// picked over a node with a smaller number of pods with the same negative
			// priority (and similar scenarios).
			sumPriorities += int64(podutil.GetPodPriority(pod)) + int64(math.MaxInt32+1)
		}
		if sumPriorities < minSumPriorities {
			minSumPriorities = sumPriorities
			lenNodes1 = 0
		}
		if sumPriorities == minSumPriorities {
			minNodes1[lenNodes1] = node
			lenNodes1++
		}
	}
	if lenNodes1 == 1 {
		return minNodes1[0]
	}

	// There are a few nodes with minimum highest priority victim and sum of priorities.
	// Find one with the minimum number of pods.
	// 4、找到node列表中需要牺牲的pod数量最小的
	minNumPods := math.MaxInt32
	lenNodes2 = 0
	for i := 0; i < lenNodes1; i++ {
		node := minNodes1[i]
		numPods := len(nodesToVictims[node].Pods)
		if numPods < minNumPods {
			minNumPods = numPods
			lenNodes2 = 0
		}
		if numPods == minNumPods {
			minNodes2[lenNodes2] = node
			lenNodes2++
		}
	}
	if lenNodes2 == 1 {
		return minNodes2[0]
	}

	// There are a few nodes with same number of pods.
	// Find the node that satisfies latest(earliestStartTime(all highest-priority pods on node))
	// 5、若多个 node 的 pod 数量相等，则选出高优先级 pod 启动时间最短的
	latestStartTime := util.GetEarliestPodStartTime(nodesToVictims[minNodes2[0]])
	if latestStartTime == nil {
		// If the earliest start time of all pods on the 1st node is nil, just return it,
		// which is not expected to happen.
		klog.Errorf("earliestStartTime is nil for node %s. Should not reach here.", minNodes2[0])
		return minNodes2[0]
	}
	nodeToReturn := minNodes2[0]
	for i := 1; i < lenNodes2; i++ {
		node := minNodes2[i]
		// Get earliest start time of all pods on the current node.
		earliestStartTimeOnNode := util.GetEarliestPodStartTime(nodesToVictims[node])
		if earliestStartTimeOnNode == nil {
			klog.Errorf("earliestStartTime is nil for node %s. Should not reach here.", node)
			continue
		}
		if earliestStartTimeOnNode.After(latestStartTime.Time) {
			latestStartTime = earliestStartTimeOnNode
			nodeToReturn = node
		}
	}

	return nodeToReturn
}

// selectVictimsOnNode finds minimum set of pods on the given node that should
// be preempted in order to make enough room for "pod" to be scheduled. The
// minimum set selected is subject to the constraint that a higher-priority pod
// is never preempted when a lower-priority pod could be (higher/lower relative
// to one another, not relative to the preemptor "pod").
// The algorithm first checks if the pod can be scheduled on the node when all the
// lower priority pods are gone. If so, it sorts all the lower priority pods by
// their priority and then puts them into two groups of those whose PodDisruptionBudget
// will be violated if preempted and other non-violating pods. Both groups are
// sorted by priority. It first tries to reprieve as many PDB violating pods as
// possible and then does them same for non-PDB-violating pods while checking
// that the "pod" can still fit on the node.
// NOTE: This function assumes that it is never called if "pod" cannot be scheduled
// due to pod affinity, node affinity, or node anti-affinity reasons. None of
// these predicates can be satisfied by removing more pods from the node.
func selectVictimsOnNode(
	ctx context.Context,
	ph framework.PreemptHandle,
	state *framework.CycleState,
	pod *v1.Pod,
	nodeInfo *framework.NodeInfo,
	pdbs []*policy.PodDisruptionBudget,
) ([]*v1.Pod, int, bool) {
	// 存储node上优先级低于要调度pod的pod
	var potentialVictims []*v1.Pod
	// 移除node节点的pod
	// 将pod从node中移除，然后修改node一些属性，
	// 如将Requested.MilliCPU、Requested.Memory中减去，表示已用资源大小，将该pod从node节点的Pods列表中移除等等
	removePod := func(rp *v1.Pod) error {
		if err := nodeInfo.RemovePod(rp); err != nil {
			return err
		}
		status := ph.RunPreFilterExtensionRemovePod(ctx, state, pod, rp, nodeInfo)
		if !status.IsSuccess() {
			return status.AsError()
		}
		return nil
	}
	// 将node节点添加pod
	addPod := func(ap *v1.Pod) error {
		nodeInfo.AddPod(ap)
		status := ph.RunPreFilterExtensionAddPod(ctx, state, pod, ap, nodeInfo)
		if !status.IsSuccess() {
			return status.AsError()
		}
		return nil
	}
	// As the first step, remove all the lower priority pods from the node and
	// check if the given pod can be scheduled.
	// 筛选被抢占的 pod
	// 获取pod的优先级，并将node中所有优先级低于该pod的调用removePod方法pod移除
	podPriority := podutil.GetPodPriority(pod)
	// 会遍历node里面的pod列表，如果找到优先级小于抢占pod的pod就加入到potentialVictims集合中，并调用removePod方法，将当前被遍历的pod从node中移除。
	for _, p := range nodeInfo.Pods {
		if podutil.GetPodPriority(p.Pod) < podPriority {
			potentialVictims = append(potentialVictims, p.Pod)
			if err := removePod(p.Pod); err != nil {
				return nil, 0, false
			}
		}
	}

	// No potential victims are found, and so we don't need to evaluate the node again since its state didn't change.
	// 没有优先级低的node，直接返回
	if len(potentialVictims) == 0 {
		return nil, 0, false
	}

	// If the new pod does not fit after removing all the lower priority pods,
	// we are almost done and this node is not suitable for preemption. The only
	// condition that we could check is if the "pod" is failing to schedule due to
	// inter-pod affinity to one or more victims, but we have decided not to
	// support this case for performance reasons. Having affinity to lower
	// priority pods is not a recommended configuration anyway.
	// 接着会调用PodPassesFiltersOnNode方法，这个方法会运行两次。
	// 第一次会调用addNominatedPods方法将调度队列中找到节点上优先级大于或等于当前pod资源对象的nominatedPods加入到nodeInfo对象中，然后执行FilterPlugin列表；
	// 第二次则直接执行FilterPlugins列表。之所以要这么做，是由于亲和性的关系，k8s需要判断当前调度的pod亲和性是否依赖了nominatedPods。
	// * 此步骤通过，则说明驱逐低优pod后，当前要调度的pod可以在该node上运行
	if fits, _, err := core.PodPassesFiltersOnNode(ctx, ph, state, pod, nodeInfo); !fits {
		if err != nil {
			klog.Warningf("Encountered error while selecting victims on node %v: %v", nodeInfo.Node().Name, err)
		}

		return nil, 0, false
	}

	// 下面的逻辑是要确定要驱逐的pod
	var victims []*v1.Pod // 存储要驱逐的pod候选切片
	numViolatingVictim := 0
	// 将potentialVictims集合里的pod按照优先级进行排序
	sort.Slice(potentialVictims, func(i, j int) bool { return util.MoreImportantPod(potentialVictims[i], potentialVictims[j]) })
	// Try to reprieve as many pods as possible. We first try to reprieve the PDB
	// violating victims and then other non-violating ones. In both cases, we start
	// from the highest priority victims.
	// 将低优先级的pod 分两类，
	// violatingVictims：抢占会造成违反了 PodDisruptionBudget(PDB) 的 pod（violatingVictims）
	// nonViolatingVictims：抢占不会造成违反了 PodDisruptionBudget(PDB) 的 pod（violatingVictims）
	// https://kubernetes.io/zh/docs/concepts/scheduling-eviction/pod-priority-preemption/#poddisruptionbudget-%E6%98%AF%E8%A2%AB%E6%94%AF%E6%8C%81%E7%9A%84-%E4%BD%86%E4%B8%8D%E6%8F%90%E4%BE%9B%E4%BF%9D%E8%AF%81
	violatingVictims, nonViolatingVictims := filterPodsWithPDBViolation(potentialVictims, pdbs)
	reprievePod := func(p *v1.Pod) (bool, error) {
		if err := addPod(p); err != nil {
			return false, err
		}
		fits, _, _ := core.PodPassesFiltersOnNode(ctx, ph, state, pod, nodeInfo)
		// 如果预选失败，则说明传入的pod与需要调度的pod不能在同一node上，将传入的pod从node中移到驱逐候选pod中
		if !fits {
			if err := removePod(p); err != nil {
				return false, err
			}
			victims = append(victims, p)
			klog.V(5).Infof("Pod %v/%v is a potential preemption victim on node %v.", p.Namespace, p.Name, nodeInfo.Node().Name)
		}
		return fits, nil
	}
	// 将violatingVictims中可以被驱逐的pod记录victims中并计数
	for _, p := range violatingVictims {
		if fits, err := reprievePod(p); err != nil {
			klog.Warningf("Failed to reprieve pod %q: %v", p.Name, err)
			return nil, 0, false
		} else if !fits {
			numViolatingVictim++
		}
	}
	// Now we try to reprieve non-violating victims.
	// 将nonViolatingVictims中可以被驱逐的pod记录victims中
	for _, p := range nonViolatingVictims {
		if _, err := reprievePod(p); err != nil {
			klog.Warningf("Failed to reprieve pod %q: %v", p.Name, err)
			return nil, 0, false
		}
	}
	return victims, numViolatingVictim, true
}

// PrepareCandidate does some preparation work before nominating the selected candidate:
// - Evict the victim pods
// - Reject the victim pods if they are in waitingPod map
// - Clear the low-priority pods' nominatedNodeName status if needed
// 这个方法会调用DeletePod删除Victims列表里面的pod，然后将这些pod中的Status.NominatedNodeName属性置空
func PrepareCandidate(c Candidate, fh framework.FrameworkHandle, cs kubernetes.Interface, pod *v1.Pod) error {
	// 驱逐候选node中的pod
	for _, victim := range c.Victims().Pods {
		// 向api server发送pod delete命令
		if err := util.DeletePod(cs, victim); err != nil {
			klog.Errorf("Error preempting pod %v/%v: %v", victim.Namespace, victim.Name, err)
			return err
		}
		// If the victim is a WaitingPod, send a reject message to the PermitPlugin
		// 如果被驱逐的pod处于waiting状态，则声明该pod为Unschedulable
		if waitingPod := fh.GetWaitingPod(victim.UID); waitingPod != nil {
			waitingPod.Reject("preempted")
		}
		fh.EventRecorder().Eventf(victim, pod, v1.EventTypeNormal, "Preempted", "Preempting", "Preempted by %v/%v on node %v",
			pod.Namespace, pod.Name, c.Name())
	}
	metrics.PreemptionVictims.Observe(float64(len(c.Victims().Pods)))

	// Lower priority pods nominated to run on this node, may no longer fit on
	// this node. So, we should remove their nomination. Removing their
	// nomination updates these pods and moves them to the active queue. It
	// lets scheduler find another place for them.
	// 获取本scheduler中记录其他通过抢占计划调度到该节点但还没有绑定 且优先级低于当前pod的pod
	nominatedPods := getLowerPriorityNominatedPods(fh.PreemptHandle(), pod, c.Name())
	// 通过向apiserver更新pod信息的方式 将这些 pod重新加入到 activeQ 队列中，让调度器为这些 pod 重新 bind node
	if err := util.ClearNominatedNodeName(cs, nominatedPods...); err != nil {
		klog.Errorf("Cannot clear 'NominatedNodeName' field: %v", err)
		// We do not return as this error is not critical.
	}

	return nil
}

// getLowerPriorityNominatedPods returns pods whose priority is smaller than the
// priority of the given "pod" and are nominated to run on the given node.
// Note: We could possibly check if the nominated lower priority pods still fit
// and return those that no longer fit, but that would require lots of
// manipulation of NodeInfo and PreFilter state per nominated pod. It may not be
// worth the complexity, especially because we generally expect to have a very
// small number of nominated pods per node.
func getLowerPriorityNominatedPods(pn framework.PodNominator, pod *v1.Pod, nodeName string) []*v1.Pod {
	pods := pn.NominatedPodsForNode(nodeName)

	if len(pods) == 0 {
		return nil
	}

	var lowerPriorityPods []*v1.Pod
	podPriority := podutil.GetPodPriority(pod)
	for _, p := range pods {
		if podutil.GetPodPriority(p) < podPriority {
			lowerPriorityPods = append(lowerPriorityPods, p)
		}
	}
	return lowerPriorityPods
}

// filterPodsWithPDBViolation groups the given "pods" into two groups of "violatingPods"
// and "nonViolatingPods" based on whether their PDBs will be violated if they are
// preempted.
// This function is stable and does not change the order of received pods. So, if it
// receives a sorted list, grouping will preserve the order of the input list.
func filterPodsWithPDBViolation(pods []*v1.Pod, pdbs []*policy.PodDisruptionBudget) (violatingPods, nonViolatingPods []*v1.Pod) {
	pdbsAllowed := make([]int32, len(pdbs))
	for i, pdb := range pdbs {
		pdbsAllowed[i] = pdb.Status.DisruptionsAllowed
	}

	for _, obj := range pods {
		pod := obj
		pdbForPodIsViolated := false
		// A pod with no labels will not match any PDB. So, no need to check.
		if len(pod.Labels) != 0 {
			for i, pdb := range pdbs {
				if pdb.Namespace != pod.Namespace {
					continue
				}
				selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
				if err != nil {
					continue
				}
				// A PDB with a nil or empty selector matches nothing.
				if selector.Empty() || !selector.Matches(labels.Set(pod.Labels)) {
					continue
				}

				// Existing in DisruptedPods means it has been processed in API server,
				// we don't treat it as a violating case.
				if _, exist := pdb.Status.DisruptedPods[pod.Name]; exist {
					continue
				}
				// Only decrement the matched pdb when it's not in its <DisruptedPods>;
				// otherwise we may over-decrement the budget number.
				pdbsAllowed[i]--
				// We have found a matching PDB.
				if pdbsAllowed[i] < 0 {
					pdbForPodIsViolated = true
				}
			}
		}
		if pdbForPodIsViolated {
			violatingPods = append(violatingPods, pod)
		} else {
			nonViolatingPods = append(nonViolatingPods, pod)
		}
	}
	return violatingPods, nonViolatingPods
}

func getPDBLister(informerFactory informers.SharedInformerFactory) policylisters.PodDisruptionBudgetLister {
	if utilfeature.DefaultFeatureGate.Enabled(kubefeatures.PodDisruptionBudget) {
		return informerFactory.Policy().V1beta1().PodDisruptionBudgets().Lister()
	}
	return nil
}

func getPodDisruptionBudgets(pdbLister policylisters.PodDisruptionBudgetLister) ([]*policy.PodDisruptionBudget, error) {
	if pdbLister != nil {
		return pdbLister.List(labels.Everything())
	}
	return nil, nil
}
