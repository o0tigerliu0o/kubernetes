package diskaffinity

import (
	"context"
	"fmt"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"kubernetes/pkg/controller/volume/scheduling"
	pluginhelper "kubernetes/pkg/scheduler/framework/plugins/helper"
	framework "kubernetes/pkg/scheduler/framework/v1alpha1"
)

/*
	1、prefilter：判断改node下是否有为bind的pv
	2、filter：未bind的pv有一个大小*60%是否大于要求值
	3、Score：偏离值比例越小得分越高，(pv*60%-要求值/要求值)*100 如果大于100，则默认为10分，如果node中有多个pv在保留分值最高的
    labels: storageLimitGB: 2048 触发
*/

type DiskAffinity struct {
	Binder                               scheduling.SchedulerVolumeBinder
}

var _ framework.PreFilterPlugin = &DiskAffinity{}
var _ framework.FilterPlugin = &DiskAffinity{}
var _ framework.ScorePlugin = &DiskAffinity{}
var _ framework.ReservePlugin = &DiskAffinity{}
var _ framework.PreBindPlugin = &DiskAffinity{}

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "DiskAffinity"
	NodeIpKay = "kubernetes.io/hostname"
	MySQLLabelKay = "app"
	MySQLLabelValue = "mysql"
	labelKey = "storageLimitGB"
	preFilterStateKey = "PreFilter" + Name
	// ErrReason for disk affinity/selector not matching.
	ErrReason = "node(s) didn't match disk affinity"
	PreFilterErrReason = "MySQL Pod does not have storageLimitGB label"
)

type preFilterState string

// Clone the prefilter state.
func (s preFilterState) Clone() framework.StateData {
	// The state is not impacted by adding/removing existing pods, hence we don't need to make a deep copy.
	return s
}

// Name returns name of the plugin. It is used in logs, etc.
func (da *DiskAffinity) Name() string {
	return Name
}

// 实现PreFilterPlugin接口
// PreFilter invoked at the prefilter extension point.
// 判断带有MySQL标签的pod是否带有storageLimitGB标签，如果没有则该pod不进行调度
func (da *DiskAffinity) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod) *framework.Status {
	// map 读未加锁
	if value, hasMySQLLabel := pod.Labels[MySQLLabelKay];hasMySQLLabel && value == MySQLLabelValue{
		if _, hasStorageLabel := pod.Labels[labelKey];!hasStorageLabel{
			cycleState.Write(preFilterStateKey, preFilterState(ErrReason))
		}
	}
	return nil
}

// PreFilterExtensions do not exist for this plugin.
func (da *DiskAffinity) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}


// 实现FilterPlugin接口
// Filter invoked at the filter extension point.
// prefilter：判断改node下是否有为bind的pv
// filter：未bind的pv有一个大小*60%是否大于要求值
func (da *DiskAffinity) Filter(ctx context.Context, cs *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	// 如果pod没有mysql标签，则跳过本策略
	if value, hasMySQLLabel := pod.Labels[MySQLLabelKay];!hasMySQLLabel || value != MySQLLabelValue{
		return nil
	}

	node := nodeInfo.Node()

	// 判断node下是否有未bind的pvc，并获取大小
	// 1、获取node ip
	// 2、根据node ip的label获取pv
	// 3、遍历pv确定pv大小是否合适(加锁)

	return nil
}



// 实现ScorePlugin接口
// Score invoked at the Score extension point.
// 偏离值比例越小得分越高，(pv*60%-要求值/要求值)*100 如果大于100，则默认为10分，如果node中有多个pv在保留分值最高的
func (da *DiskAffinity) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	// 如果pod没有mysql标签，则跳过本策略
	if value, hasMySQLLabel := pod.Labels[MySQLLabelKay];!hasMySQLLabel || value != MySQLLabelValue{
		return 0,nil
	}
	return 0,nil
}

// NormalizeScore invoked after scoring all nodes.
func  (da *DiskAffinity)  NormalizeScore(ctx context.Context, state *framework.CycleState, pod *v1.Pod, scores framework.NodeScoreList) *framework.Status {
	return pluginhelper.DefaultNormalizeScore(framework.MaxNodeScore, false, scores)
}

// ScoreExtensions of the Score plugin.
func  (da *DiskAffinity)  ScoreExtensions() framework.ScoreExtensions {
	return da
}

// Reserve reserves volumes of pod and saves binding status in cycle state.
func (pl *DiskAffinity) Reserve(ctx context.Context, cs *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	return nil
}

// PreBind will make the API update with the assumed bindings and wait until
// the PV controller has completely finished the binding operation.
//
// If binding errors, times out or gets undone, then an error will be returned to
// retry scheduling.
func (pl *DiskAffinity) PreBind(ctx context.Context, cs *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	return nil
}

// 策略注册使用
// New initializes a new plugin and returns it.
func New(_ runtime.Object, h framework.FrameworkHandle) (framework.Plugin, error) {
	return &DiskAffinity{}, nil
}