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

package runtime

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"kubernetes/pkg/scheduler/apis/config"
	"kubernetes/pkg/scheduler/framework/v1alpha1"
	"kubernetes/pkg/scheduler/metrics"
)

const (
	queueSortPlugin                   = "no-op-queue-sort-plugin"
	scoreWithNormalizePlugin1         = "score-with-normalize-plugin-1"
	scoreWithNormalizePlugin2         = "score-with-normalize-plugin-2"
	scorePlugin1                      = "score-plugin-1"
	pluginNotImplementingScore        = "plugin-not-implementing-score"
	preFilterPluginName               = "prefilter-plugin"
	preFilterWithExtensionsPluginName = "prefilter-with-extensions-plugin"
	duplicatePluginName               = "duplicate-plugin"
	testPlugin                        = "test-plugin"
	permitPlugin                      = "permit-plugin"
	bindPlugin                        = "bind-plugin"

	testProfileName = "test-profile"
)

// TestScoreWithNormalizePlugin implements ScoreWithNormalizePlugin interface.
// TestScorePlugin only implements ScorePlugin interface.
var _ v1alpha1.ScorePlugin = &TestScoreWithNormalizePlugin{}
var _ v1alpha1.ScorePlugin = &TestScorePlugin{}

func newScoreWithNormalizePlugin1(injArgs runtime.Object, f v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
	var inj injectedResult
	if err := DecodeInto(injArgs, &inj); err != nil {
		return nil, err
	}
	return &TestScoreWithNormalizePlugin{scoreWithNormalizePlugin1, inj}, nil
}

func newScoreWithNormalizePlugin2(injArgs runtime.Object, f v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
	var inj injectedResult
	if err := DecodeInto(injArgs, &inj); err != nil {
		return nil, err
	}
	return &TestScoreWithNormalizePlugin{scoreWithNormalizePlugin2, inj}, nil
}

func newScorePlugin1(injArgs runtime.Object, f v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
	var inj injectedResult
	if err := DecodeInto(injArgs, &inj); err != nil {
		return nil, err
	}
	return &TestScorePlugin{scorePlugin1, inj}, nil
}

func newPluginNotImplementingScore(_ runtime.Object, _ v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
	return &PluginNotImplementingScore{}, nil
}

type TestScoreWithNormalizePlugin struct {
	name string
	inj  injectedResult
}

func (pl *TestScoreWithNormalizePlugin) Name() string {
	return pl.name
}

func (pl *TestScoreWithNormalizePlugin) NormalizeScore(ctx context.Context, state *v1alpha1.CycleState, pod *v1.Pod, scores v1alpha1.NodeScoreList) *v1alpha1.Status {
	return injectNormalizeRes(pl.inj, scores)
}

func (pl *TestScoreWithNormalizePlugin) Score(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod, nodeName string) (int64, *v1alpha1.Status) {
	return setScoreRes(pl.inj)
}

func (pl *TestScoreWithNormalizePlugin) ScoreExtensions() v1alpha1.ScoreExtensions {
	return pl
}

// TestScorePlugin only implements ScorePlugin interface.
type TestScorePlugin struct {
	name string
	inj  injectedResult
}

func (pl *TestScorePlugin) Name() string {
	return pl.name
}

func (pl *TestScorePlugin) Score(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod, nodeName string) (int64, *v1alpha1.Status) {
	return setScoreRes(pl.inj)
}

func (pl *TestScorePlugin) ScoreExtensions() v1alpha1.ScoreExtensions {
	return nil
}

// PluginNotImplementingScore doesn't implement the ScorePlugin interface.
type PluginNotImplementingScore struct{}

func (pl *PluginNotImplementingScore) Name() string {
	return pluginNotImplementingScore
}

// TestPlugin implements all Plugin interfaces.
type TestPlugin struct {
	name string
	inj  injectedResult
}

func (pl *TestPlugin) AddPod(ctx context.Context, state *v1alpha1.CycleState, podToSchedule *v1.Pod, podToAdd *v1.Pod, nodeInfo *v1alpha1.NodeInfo) *v1alpha1.Status {
	return v1alpha1.NewStatus(v1alpha1.Code(pl.inj.PreFilterAddPodStatus), "injected status")
}
func (pl *TestPlugin) RemovePod(ctx context.Context, state *v1alpha1.CycleState, podToSchedule *v1.Pod, podToRemove *v1.Pod, nodeInfo *v1alpha1.NodeInfo) *v1alpha1.Status {
	return v1alpha1.NewStatus(v1alpha1.Code(pl.inj.PreFilterRemovePodStatus), "injected status")
}

func (pl *TestPlugin) Name() string {
	return pl.name
}

func (pl *TestPlugin) Less(*v1alpha1.QueuedPodInfo, *v1alpha1.QueuedPodInfo) bool {
	return false
}

func (pl *TestPlugin) Score(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod, nodeName string) (int64, *v1alpha1.Status) {
	return 0, v1alpha1.NewStatus(v1alpha1.Code(pl.inj.ScoreStatus), "injected status")
}

func (pl *TestPlugin) ScoreExtensions() v1alpha1.ScoreExtensions {
	return nil
}

func (pl *TestPlugin) PreFilter(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod) *v1alpha1.Status {
	return v1alpha1.NewStatus(v1alpha1.Code(pl.inj.PreFilterStatus), "injected status")
}

func (pl *TestPlugin) PreFilterExtensions() v1alpha1.PreFilterExtensions {
	return pl
}

func (pl *TestPlugin) Filter(ctx context.Context, state *v1alpha1.CycleState, pod *v1.Pod, nodeInfo *v1alpha1.NodeInfo) *v1alpha1.Status {
	return v1alpha1.NewStatus(v1alpha1.Code(pl.inj.FilterStatus), "injected filter status")
}

func (pl *TestPlugin) PostFilter(_ context.Context, _ *v1alpha1.CycleState, _ *v1.Pod, _ v1alpha1.NodeToStatusMap) (*v1alpha1.PostFilterResult, *v1alpha1.Status) {
	return nil, v1alpha1.NewStatus(v1alpha1.Code(pl.inj.PostFilterStatus), "injected status")
}

func (pl *TestPlugin) PreScore(ctx context.Context, state *v1alpha1.CycleState, pod *v1.Pod, nodes []*v1.Node) *v1alpha1.Status {
	return v1alpha1.NewStatus(v1alpha1.Code(pl.inj.PreScoreStatus), "injected status")
}

func (pl *TestPlugin) Reserve(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod, nodeName string) *v1alpha1.Status {
	return v1alpha1.NewStatus(v1alpha1.Code(pl.inj.ReserveStatus), "injected status")
}

func (pl *TestPlugin) Unreserve(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod, nodeName string) {
}

func (pl *TestPlugin) PreBind(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod, nodeName string) *v1alpha1.Status {
	return v1alpha1.NewStatus(v1alpha1.Code(pl.inj.PreBindStatus), "injected status")
}

func (pl *TestPlugin) PostBind(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod, nodeName string) {
}

func (pl *TestPlugin) Permit(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod, nodeName string) (*v1alpha1.Status, time.Duration) {
	return v1alpha1.NewStatus(v1alpha1.Code(pl.inj.PermitStatus), "injected status"), time.Duration(0)
}

func (pl *TestPlugin) Bind(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod, nodeName string) *v1alpha1.Status {
	return v1alpha1.NewStatus(v1alpha1.Code(pl.inj.BindStatus), "injected status")
}

// TestPreFilterPlugin only implements PreFilterPlugin interface.
type TestPreFilterPlugin struct {
	PreFilterCalled int
}

func (pl *TestPreFilterPlugin) Name() string {
	return preFilterPluginName
}

func (pl *TestPreFilterPlugin) PreFilter(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod) *v1alpha1.Status {
	pl.PreFilterCalled++
	return nil
}

func (pl *TestPreFilterPlugin) PreFilterExtensions() v1alpha1.PreFilterExtensions {
	return nil
}

// TestPreFilterWithExtensionsPlugin implements Add/Remove interfaces.
type TestPreFilterWithExtensionsPlugin struct {
	PreFilterCalled int
	AddCalled       int
	RemoveCalled    int
}

func (pl *TestPreFilterWithExtensionsPlugin) Name() string {
	return preFilterWithExtensionsPluginName
}

func (pl *TestPreFilterWithExtensionsPlugin) PreFilter(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod) *v1alpha1.Status {
	pl.PreFilterCalled++
	return nil
}

func (pl *TestPreFilterWithExtensionsPlugin) AddPod(ctx context.Context, state *v1alpha1.CycleState, podToSchedule *v1.Pod,
	podToAdd *v1.Pod, nodeInfo *v1alpha1.NodeInfo) *v1alpha1.Status {
	pl.AddCalled++
	return nil
}

func (pl *TestPreFilterWithExtensionsPlugin) RemovePod(ctx context.Context, state *v1alpha1.CycleState, podToSchedule *v1.Pod,
	podToRemove *v1.Pod, nodeInfo *v1alpha1.NodeInfo) *v1alpha1.Status {
	pl.RemoveCalled++
	return nil
}

func (pl *TestPreFilterWithExtensionsPlugin) PreFilterExtensions() v1alpha1.PreFilterExtensions {
	return pl
}

type TestDuplicatePlugin struct {
}

func (dp *TestDuplicatePlugin) Name() string {
	return duplicatePluginName
}

func (dp *TestDuplicatePlugin) PreFilter(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod) *v1alpha1.Status {
	return nil
}

func (dp *TestDuplicatePlugin) PreFilterExtensions() v1alpha1.PreFilterExtensions {
	return nil
}

var _ v1alpha1.PreFilterPlugin = &TestDuplicatePlugin{}

func newDuplicatePlugin(_ runtime.Object, _ v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
	return &TestDuplicatePlugin{}, nil
}

// TestPermitPlugin only implements PermitPlugin interface.
type TestPermitPlugin struct {
	PreFilterCalled int
}

func (pp *TestPermitPlugin) Name() string {
	return permitPlugin
}
func (pp *TestPermitPlugin) Permit(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod, nodeName string) (*v1alpha1.Status, time.Duration) {
	return v1alpha1.NewStatus(v1alpha1.Wait, ""), time.Duration(10 * time.Second)
}

var _ v1alpha1.QueueSortPlugin = &TestQueueSortPlugin{}

func newQueueSortPlugin(_ runtime.Object, _ v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
	return &TestQueueSortPlugin{}, nil
}

// TestQueueSortPlugin is a no-op implementation for QueueSort extension point.
type TestQueueSortPlugin struct{}

func (pl *TestQueueSortPlugin) Name() string {
	return queueSortPlugin
}

func (pl *TestQueueSortPlugin) Less(_, _ *v1alpha1.QueuedPodInfo) bool {
	return false
}

var _ v1alpha1.BindPlugin = &TestBindPlugin{}

func newBindPlugin(_ runtime.Object, _ v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
	return &TestBindPlugin{}, nil
}

// TestBindPlugin is a no-op implementation for Bind extension point.
type TestBindPlugin struct{}

func (t TestBindPlugin) Name() string {
	return bindPlugin
}

func (t TestBindPlugin) Bind(ctx context.Context, state *v1alpha1.CycleState, p *v1.Pod, nodeName string) *v1alpha1.Status {
	return nil
}

var registry = func() Registry {
	r := make(Registry)
	r.Register(scoreWithNormalizePlugin1, newScoreWithNormalizePlugin1)
	r.Register(scoreWithNormalizePlugin2, newScoreWithNormalizePlugin2)
	r.Register(scorePlugin1, newScorePlugin1)
	r.Register(pluginNotImplementingScore, newPluginNotImplementingScore)
	r.Register(duplicatePluginName, newDuplicatePlugin)
	return r
}()

var defaultWeights = map[string]int32{
	scoreWithNormalizePlugin1: 1,
	scoreWithNormalizePlugin2: 2,
	scorePlugin1:              1,
}

var emptyArgs = make([]config.PluginConfig, 0)
var state = &v1alpha1.CycleState{}

// Pod is only used for logging errors.
var pod = &v1.Pod{}
var nodes = []*v1.Node{
	{ObjectMeta: metav1.ObjectMeta{Name: "node1"}},
	{ObjectMeta: metav1.ObjectMeta{Name: "node2"}},
}

func newFrameworkWithQueueSortAndBind(r Registry, pl *config.Plugins, plc []config.PluginConfig, opts ...Option) (v1alpha1.Framework, error) {
	if _, ok := r[queueSortPlugin]; !ok {
		r[queueSortPlugin] = newQueueSortPlugin
	}
	if _, ok := r[bindPlugin]; !ok {
		r[bindPlugin] = newBindPlugin
	}
	plugins := &config.Plugins{}
	plugins.Append(pl)
	if plugins.QueueSort == nil || len(plugins.QueueSort.Enabled) == 0 {
		plugins.Append(&config.Plugins{
			QueueSort: &config.PluginSet{
				Enabled: []config.Plugin{{Name: queueSortPlugin}},
			},
		})
	}
	if plugins.Bind == nil || len(plugins.Bind.Enabled) == 0 {
		plugins.Append(&config.Plugins{
			Bind: &config.PluginSet{
				Enabled: []config.Plugin{{Name: bindPlugin}},
			},
		})
	}
	return NewFramework(r, plugins, plc, opts...)
}

func TestInitFrameworkWithScorePlugins(t *testing.T) {
	tests := []struct {
		name    string
		plugins *config.Plugins
		// If initErr is true, we expect framework initialization to fail.
		initErr bool
	}{
		{
			name:    "enabled Score plugin doesn't exist in registry",
			plugins: buildScoreConfigDefaultWeights("notExist"),
			initErr: true,
		},
		{
			name:    "enabled Score plugin doesn't extend the ScorePlugin interface",
			plugins: buildScoreConfigDefaultWeights(pluginNotImplementingScore),
			initErr: true,
		},
		{
			name:    "Score plugins are nil",
			plugins: &config.Plugins{Score: nil},
		},
		{
			name:    "enabled Score plugin list is empty",
			plugins: buildScoreConfigDefaultWeights(),
		},
		{
			name:    "enabled plugin only implements ScorePlugin interface",
			plugins: buildScoreConfigDefaultWeights(scorePlugin1),
		},
		{
			name:    "enabled plugin implements ScoreWithNormalizePlugin interface",
			plugins: buildScoreConfigDefaultWeights(scoreWithNormalizePlugin1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newFrameworkWithQueueSortAndBind(registry, tt.plugins, emptyArgs)
			if tt.initErr && err == nil {
				t.Fatal("Framework initialization should fail")
			}
			if !tt.initErr && err != nil {
				t.Fatalf("Failed to create framework for testing: %v", err)
			}
		})
	}
}

func TestNewFrameworkErrors(t *testing.T) {
	tests := []struct {
		name      string
		plugins   *config.Plugins
		pluginCfg []config.PluginConfig
		wantErr   string
	}{
		{
			name: "duplicate plugin name",
			plugins: &config.Plugins{
				PreFilter: &config.PluginSet{
					Enabled: []config.Plugin{
						{Name: duplicatePluginName, Weight: 1},
						{Name: duplicatePluginName, Weight: 1},
					},
				},
			},
			pluginCfg: []config.PluginConfig{
				{Name: duplicatePluginName},
			},
			wantErr: "already registered",
		},
		{
			name: "duplicate plugin config",
			plugins: &config.Plugins{
				PreFilter: &config.PluginSet{
					Enabled: []config.Plugin{
						{Name: duplicatePluginName, Weight: 1},
					},
				},
			},
			pluginCfg: []config.PluginConfig{
				{Name: duplicatePluginName},
				{Name: duplicatePluginName},
			},
			wantErr: "repeated config for plugin",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewFramework(registry, tc.plugins, tc.pluginCfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Unexpected error, got %v, expect: %s", err, tc.wantErr)
			}
		})
	}
}

func recordingPluginFactory(name string, result map[string]runtime.Object) PluginFactory {
	return func(args runtime.Object, f v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
		result[name] = args
		return &TestPlugin{
			name: name,
		}, nil
	}
}

func TestNewFrameworkPluginDefaults(t *testing.T) {
	// In-tree plugins that use args.
	pluginsWithArgs := []string{
		"InterPodAffinity",
		"NodeLabel",
		"NodeResourcesFit",
		"NodeResourcesLeastAllocated",
		"NodeResourcesMostAllocated",
		"PodTopologySpread",
		"RequestedToCapacityRatio",
		"VolumeBinding",
	}
	plugins := config.Plugins{
		Filter: &config.PluginSet{},
	}
	// Use all plugins in Filter.
	// NOTE: This does not mean those plugins implemented `Filter` interfaces.
	// `TestPlugin` is created in this test to fake the behavior for test purpose.
	for _, name := range pluginsWithArgs {
		plugins.Filter.Enabled = append(plugins.Filter.Enabled, config.Plugin{Name: name})
	}
	// Set required extension points.
	onePlugin := &config.PluginSet{
		Enabled: []config.Plugin{{Name: pluginsWithArgs[0]}},
	}
	plugins.QueueSort = onePlugin
	plugins.Bind = onePlugin

	tests := []struct {
		name      string
		pluginCfg []config.PluginConfig
		wantCfg   map[string]runtime.Object
	}{
		{
			name: "empty plugin config",
			wantCfg: map[string]runtime.Object{
				"InterPodAffinity": &config.InterPodAffinityArgs{
					HardPodAffinityWeight: 1,
				},
				"NodeLabel":        &config.NodeLabelArgs{},
				"NodeResourcesFit": &config.NodeResourcesFitArgs{},
				"NodeResourcesLeastAllocated": &config.NodeResourcesLeastAllocatedArgs{
					Resources: []config.ResourceSpec{{Name: "cpu", Weight: 1}, {Name: "memory", Weight: 1}},
				},
				"NodeResourcesMostAllocated": &config.NodeResourcesMostAllocatedArgs{
					Resources: []config.ResourceSpec{{Name: "cpu", Weight: 1}, {Name: "memory", Weight: 1}},
				},
				"RequestedToCapacityRatio": &config.RequestedToCapacityRatioArgs{
					Resources: []config.ResourceSpec{{Name: "cpu", Weight: 1}, {Name: "memory", Weight: 1}},
				},
				"PodTopologySpread": &config.PodTopologySpreadArgs{},
				"VolumeBinding": &config.VolumeBindingArgs{
					BindTimeoutSeconds: 600,
				},
			},
		},
		{
			name: "some overridden plugin config",
			pluginCfg: []config.PluginConfig{
				{
					Name: "InterPodAffinity",
					Args: &config.InterPodAffinityArgs{
						HardPodAffinityWeight: 3,
					},
				},
				{
					Name: "NodeResourcesFit",
					Args: &config.NodeResourcesFitArgs{
						IgnoredResources: []string{"example.com/foo"},
					},
				},
				{
					Name: "NodeResourcesLeastAllocated",
					Args: &config.NodeResourcesLeastAllocatedArgs{
						Resources: []config.ResourceSpec{{Name: "resource", Weight: 4}},
					},
				},
				{
					Name: "NodeResourcesMostAllocated",
					Args: &config.NodeResourcesMostAllocatedArgs{
						Resources: []config.ResourceSpec{{Name: "resource", Weight: 3}},
					},
				},
				{
					Name: "RequestedToCapacityRatio",
					Args: &config.RequestedToCapacityRatioArgs{
						Resources: []config.ResourceSpec{{Name: "resource", Weight: 2}},
					},
				},
				{
					Name: "VolumeBinding",
					Args: &config.VolumeBindingArgs{
						BindTimeoutSeconds: 300,
					},
				},
			},
			wantCfg: map[string]runtime.Object{
				"InterPodAffinity": &config.InterPodAffinityArgs{
					HardPodAffinityWeight: 3,
				},
				"NodeLabel": &config.NodeLabelArgs{},
				"NodeResourcesFit": &config.NodeResourcesFitArgs{
					IgnoredResources: []string{"example.com/foo"},
				},
				"NodeResourcesLeastAllocated": &config.NodeResourcesLeastAllocatedArgs{
					Resources: []config.ResourceSpec{{Name: "resource", Weight: 4}},
				},
				"NodeResourcesMostAllocated": &config.NodeResourcesMostAllocatedArgs{
					Resources: []config.ResourceSpec{{Name: "resource", Weight: 3}},
				},
				"PodTopologySpread": &config.PodTopologySpreadArgs{},
				"RequestedToCapacityRatio": &config.RequestedToCapacityRatioArgs{
					Resources: []config.ResourceSpec{{Name: "resource", Weight: 2}},
				},
				"VolumeBinding": &config.VolumeBindingArgs{
					BindTimeoutSeconds: 300,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// result will hold plugin args passed to factories.
			result := make(map[string]runtime.Object)
			registry := make(Registry, len(pluginsWithArgs))
			for _, name := range pluginsWithArgs {
				registry[name] = recordingPluginFactory(name, result)
			}
			_, err := NewFramework(registry, &plugins, tt.pluginCfg)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.wantCfg, result); diff != "" {
				t.Errorf("unexpected plugin args (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestRunScorePlugins(t *testing.T) {
	tests := []struct {
		name          string
		registry      Registry
		plugins       *config.Plugins
		pluginConfigs []config.PluginConfig
		want          v1alpha1.PluginToNodeScores
		// If err is true, we expect RunScorePlugin to fail.
		err bool
	}{
		{
			name:    "no Score plugins",
			plugins: buildScoreConfigDefaultWeights(),
			want:    v1alpha1.PluginToNodeScores{},
		},
		{
			name:    "single Score plugin",
			plugins: buildScoreConfigDefaultWeights(scorePlugin1),
			pluginConfigs: []config.PluginConfig{
				{
					Name: scorePlugin1,
					Args: &runtime.Unknown{
						Raw: []byte(`{ "scoreRes": 1 }`),
					},
				},
			},
			// scorePlugin1 Score returns 1, weight=1, so want=1.
			want: v1alpha1.PluginToNodeScores{
				scorePlugin1: {{Name: "node1", Score: 1}, {Name: "node2", Score: 1}},
			},
		},
		{
			name: "single ScoreWithNormalize plugin",
			//registry: registry,
			plugins: buildScoreConfigDefaultWeights(scoreWithNormalizePlugin1),
			pluginConfigs: []config.PluginConfig{
				{
					Name: scoreWithNormalizePlugin1,
					Args: &runtime.Unknown{
						Raw: []byte(`{ "scoreRes": 10, "normalizeRes": 5 }`),
					},
				},
			},
			// scoreWithNormalizePlugin1 Score returns 10, but NormalizeScore overrides to 5, weight=1, so want=5
			want: v1alpha1.PluginToNodeScores{
				scoreWithNormalizePlugin1: {{Name: "node1", Score: 5}, {Name: "node2", Score: 5}},
			},
		},
		{
			name:    "2 Score plugins, 2 NormalizeScore plugins",
			plugins: buildScoreConfigDefaultWeights(scorePlugin1, scoreWithNormalizePlugin1, scoreWithNormalizePlugin2),
			pluginConfigs: []config.PluginConfig{
				{
					Name: scorePlugin1,
					Args: &runtime.Unknown{
						Raw: []byte(`{ "scoreRes": 1 }`),
					},
				},
				{
					Name: scoreWithNormalizePlugin1,
					Args: &runtime.Unknown{
						Raw: []byte(`{ "scoreRes": 3, "normalizeRes": 4}`),
					},
				},
				{
					Name: scoreWithNormalizePlugin2,
					Args: &runtime.Unknown{
						Raw: []byte(`{ "scoreRes": 4, "normalizeRes": 5}`),
					},
				},
			},
			// scorePlugin1 Score returns 1, weight =1, so want=1.
			// scoreWithNormalizePlugin1 Score returns 3, but NormalizeScore overrides to 4, weight=1, so want=4.
			// scoreWithNormalizePlugin2 Score returns 4, but NormalizeScore overrides to 5, weight=2, so want=10.
			want: v1alpha1.PluginToNodeScores{
				scorePlugin1:              {{Name: "node1", Score: 1}, {Name: "node2", Score: 1}},
				scoreWithNormalizePlugin1: {{Name: "node1", Score: 4}, {Name: "node2", Score: 4}},
				scoreWithNormalizePlugin2: {{Name: "node1", Score: 10}, {Name: "node2", Score: 10}},
			},
		},
		{
			name: "score fails",
			pluginConfigs: []config.PluginConfig{
				{
					Name: scoreWithNormalizePlugin1,
					Args: &runtime.Unknown{
						Raw: []byte(`{ "scoreStatus": 1 }`),
					},
				},
			},
			plugins: buildScoreConfigDefaultWeights(scorePlugin1, scoreWithNormalizePlugin1),
			err:     true,
		},
		{
			name: "normalize fails",
			pluginConfigs: []config.PluginConfig{
				{
					Name: scoreWithNormalizePlugin1,
					Args: &runtime.Unknown{
						Raw: []byte(`{ "normalizeStatus": 1 }`),
					},
				},
			},
			plugins: buildScoreConfigDefaultWeights(scorePlugin1, scoreWithNormalizePlugin1),
			err:     true,
		},
		{
			name:    "Score plugin return score greater than MaxNodeScore",
			plugins: buildScoreConfigDefaultWeights(scorePlugin1),
			pluginConfigs: []config.PluginConfig{
				{
					Name: scorePlugin1,
					Args: &runtime.Unknown{
						Raw: []byte(fmt.Sprintf(`{ "scoreRes": %d }`, v1alpha1.MaxNodeScore+1)),
					},
				},
			},
			err: true,
		},
		{
			name:    "Score plugin return score less than MinNodeScore",
			plugins: buildScoreConfigDefaultWeights(scorePlugin1),
			pluginConfigs: []config.PluginConfig{
				{
					Name: scorePlugin1,
					Args: &runtime.Unknown{
						Raw: []byte(fmt.Sprintf(`{ "scoreRes": %d }`, v1alpha1.MinNodeScore-1)),
					},
				},
			},
			err: true,
		},
		{
			name:    "ScoreWithNormalize plugin return score greater than MaxNodeScore",
			plugins: buildScoreConfigDefaultWeights(scoreWithNormalizePlugin1),
			pluginConfigs: []config.PluginConfig{
				{
					Name: scoreWithNormalizePlugin1,
					Args: &runtime.Unknown{
						Raw: []byte(fmt.Sprintf(`{ "normalizeRes": %d }`, v1alpha1.MaxNodeScore+1)),
					},
				},
			},
			err: true,
		},
		{
			name:    "ScoreWithNormalize plugin return score less than MinNodeScore",
			plugins: buildScoreConfigDefaultWeights(scoreWithNormalizePlugin1),
			pluginConfigs: []config.PluginConfig{
				{
					Name: scoreWithNormalizePlugin1,
					Args: &runtime.Unknown{
						Raw: []byte(fmt.Sprintf(`{ "normalizeRes": %d }`, v1alpha1.MinNodeScore-1)),
					},
				},
			},
			err: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Inject the results via Args in PluginConfig.
			f, err := newFrameworkWithQueueSortAndBind(registry, tt.plugins, tt.pluginConfigs)
			if err != nil {
				t.Fatalf("Failed to create framework for testing: %v", err)
			}

			res, status := f.RunScorePlugins(context.Background(), state, pod, nodes)

			if tt.err {
				if status.IsSuccess() {
					t.Errorf("Expected status to be non-success. got: %v", status.Code().String())
				}
				return
			}

			if !status.IsSuccess() {
				t.Errorf("Expected status to be success.")
			}
			if !reflect.DeepEqual(res, tt.want) {
				t.Errorf("Score map after RunScorePlugin: %+v, want: %+v.", res, tt.want)
			}
		})
	}
}

func TestPreFilterPlugins(t *testing.T) {
	preFilter1 := &TestPreFilterPlugin{}
	preFilter2 := &TestPreFilterWithExtensionsPlugin{}
	r := make(Registry)
	r.Register(preFilterPluginName,
		func(_ runtime.Object, fh v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
			return preFilter1, nil
		})
	r.Register(preFilterWithExtensionsPluginName,
		func(_ runtime.Object, fh v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
			return preFilter2, nil
		})
	plugins := &config.Plugins{PreFilter: &config.PluginSet{Enabled: []config.Plugin{{Name: preFilterWithExtensionsPluginName}, {Name: preFilterPluginName}}}}
	t.Run("TestPreFilterPlugin", func(t *testing.T) {
		f, err := newFrameworkWithQueueSortAndBind(r, plugins, emptyArgs)
		if err != nil {
			t.Fatalf("Failed to create framework for testing: %v", err)
		}
		f.RunPreFilterPlugins(context.Background(), nil, nil)
		f.RunPreFilterExtensionAddPod(context.Background(), nil, nil, nil, nil)
		f.RunPreFilterExtensionRemovePod(context.Background(), nil, nil, nil, nil)

		if preFilter1.PreFilterCalled != 1 {
			t.Errorf("preFilter1 called %v, expected: 1", preFilter1.PreFilterCalled)
		}
		if preFilter2.PreFilterCalled != 1 {
			t.Errorf("preFilter2 called %v, expected: 1", preFilter2.PreFilterCalled)
		}
		if preFilter2.AddCalled != 1 {
			t.Errorf("AddPod called %v, expected: 1", preFilter2.AddCalled)
		}
		if preFilter2.RemoveCalled != 1 {
			t.Errorf("AddPod called %v, expected: 1", preFilter2.RemoveCalled)
		}
	})
}

func TestFilterPlugins(t *testing.T) {
	tests := []struct {
		name          string
		plugins       []*TestPlugin
		wantStatus    *v1alpha1.Status
		wantStatusMap v1alpha1.PluginToStatus
		runAllFilters bool
	}{
		{
			name: "SuccessFilter",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus:    nil,
			wantStatusMap: v1alpha1.PluginToStatus{},
		},
		{
			name: "ErrorFilter",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Error)},
				},
			},
			wantStatus:    v1alpha1.NewStatus(v1alpha1.Error, `running "TestPlugin" filter plugin for pod "": injected filter status`),
			wantStatusMap: v1alpha1.PluginToStatus{"TestPlugin": v1alpha1.NewStatus(v1alpha1.Error, `running "TestPlugin" filter plugin for pod "": injected filter status`)},
		},
		{
			name: "UnschedulableFilter",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Unschedulable)},
				},
			},
			wantStatus:    v1alpha1.NewStatus(v1alpha1.Unschedulable, "injected filter status"),
			wantStatusMap: v1alpha1.PluginToStatus{"TestPlugin": v1alpha1.NewStatus(v1alpha1.Unschedulable, "injected filter status")},
		},
		{
			name: "UnschedulableAndUnresolvableFilter",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj: injectedResult{
						FilterStatus: int(v1alpha1.UnschedulableAndUnresolvable)},
				},
			},
			wantStatus:    v1alpha1.NewStatus(v1alpha1.UnschedulableAndUnresolvable, "injected filter status"),
			wantStatusMap: v1alpha1.PluginToStatus{"TestPlugin": v1alpha1.NewStatus(v1alpha1.UnschedulableAndUnresolvable, "injected filter status")},
		},
		// followings tests cover multiple-plugins scenarios
		{
			name: "ErrorAndErrorFilters",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin1",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Error)},
				},

				{
					name: "TestPlugin2",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Error)},
				},
			},
			wantStatus:    v1alpha1.NewStatus(v1alpha1.Error, `running "TestPlugin1" filter plugin for pod "": injected filter status`),
			wantStatusMap: v1alpha1.PluginToStatus{"TestPlugin1": v1alpha1.NewStatus(v1alpha1.Error, `running "TestPlugin1" filter plugin for pod "": injected filter status`)},
		},
		{
			name: "SuccessAndSuccessFilters",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin1",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Success)},
				},

				{
					name: "TestPlugin2",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus:    nil,
			wantStatusMap: v1alpha1.PluginToStatus{},
		},
		{
			name: "ErrorAndSuccessFilters",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin1",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Error)},
				},
				{
					name: "TestPlugin2",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus:    v1alpha1.NewStatus(v1alpha1.Error, `running "TestPlugin1" filter plugin for pod "": injected filter status`),
			wantStatusMap: v1alpha1.PluginToStatus{"TestPlugin1": v1alpha1.NewStatus(v1alpha1.Error, `running "TestPlugin1" filter plugin for pod "": injected filter status`)},
		},
		{
			name: "SuccessAndErrorFilters",
			plugins: []*TestPlugin{
				{

					name: "TestPlugin1",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Success)},
				},
				{
					name: "TestPlugin2",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Error)},
				},
			},
			wantStatus:    v1alpha1.NewStatus(v1alpha1.Error, `running "TestPlugin2" filter plugin for pod "": injected filter status`),
			wantStatusMap: v1alpha1.PluginToStatus{"TestPlugin2": v1alpha1.NewStatus(v1alpha1.Error, `running "TestPlugin2" filter plugin for pod "": injected filter status`)},
		},
		{
			name: "SuccessAndUnschedulableFilters",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin1",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Success)},
				},

				{
					name: "TestPlugin2",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Unschedulable)},
				},
			},
			wantStatus:    v1alpha1.NewStatus(v1alpha1.Unschedulable, "injected filter status"),
			wantStatusMap: v1alpha1.PluginToStatus{"TestPlugin2": v1alpha1.NewStatus(v1alpha1.Unschedulable, "injected filter status")},
		},
		{
			name: "SuccessFilterWithRunAllFilters",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Success)},
				},
			},
			runAllFilters: true,
			wantStatus:    nil,
			wantStatusMap: v1alpha1.PluginToStatus{},
		},
		{
			name: "ErrorAndErrorFilters",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin1",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Error)},
				},

				{
					name: "TestPlugin2",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Error)},
				},
			},
			runAllFilters: true,
			wantStatus:    v1alpha1.NewStatus(v1alpha1.Error, `running "TestPlugin1" filter plugin for pod "": injected filter status`),
			wantStatusMap: v1alpha1.PluginToStatus{"TestPlugin1": v1alpha1.NewStatus(v1alpha1.Error, `running "TestPlugin1" filter plugin for pod "": injected filter status`)},
		},
		{
			name: "ErrorAndErrorFilters",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin1",
					inj:  injectedResult{FilterStatus: int(v1alpha1.UnschedulableAndUnresolvable)},
				},
				{
					name: "TestPlugin2",
					inj:  injectedResult{FilterStatus: int(v1alpha1.Unschedulable)},
				},
			},
			runAllFilters: true,
			wantStatus:    v1alpha1.NewStatus(v1alpha1.UnschedulableAndUnresolvable, "injected filter status", "injected filter status"),
			wantStatusMap: v1alpha1.PluginToStatus{
				"TestPlugin1": v1alpha1.NewStatus(v1alpha1.UnschedulableAndUnresolvable, "injected filter status"),
				"TestPlugin2": v1alpha1.NewStatus(v1alpha1.Unschedulable, "injected filter status"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := Registry{}
			cfgPls := &config.Plugins{Filter: &config.PluginSet{}}
			for _, pl := range tt.plugins {
				// register all plugins
				tmpPl := pl
				if err := registry.Register(pl.name,
					func(_ runtime.Object, _ v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
						return tmpPl, nil
					}); err != nil {
					t.Fatalf("fail to register filter plugin (%s)", pl.name)
				}
				// append plugins to filter pluginset
				cfgPls.Filter.Enabled = append(
					cfgPls.Filter.Enabled,
					config.Plugin{Name: pl.name})
			}

			f, err := newFrameworkWithQueueSortAndBind(registry, cfgPls, emptyArgs, WithRunAllFilters(tt.runAllFilters))
			if err != nil {
				t.Fatalf("fail to create framework: %s", err)
			}
			gotStatusMap := f.RunFilterPlugins(context.TODO(), nil, pod, nil)
			gotStatus := gotStatusMap.Merge()
			if !reflect.DeepEqual(gotStatus, tt.wantStatus) {
				t.Errorf("wrong status code. got: %v, want:%v", gotStatus, tt.wantStatus)
			}
			if !reflect.DeepEqual(gotStatusMap, tt.wantStatusMap) {
				t.Errorf("wrong status map. got: %+v, want: %+v", gotStatusMap, tt.wantStatusMap)
			}

		})
	}
}

func TestPostFilterPlugins(t *testing.T) {
	tests := []struct {
		name       string
		plugins    []*TestPlugin
		wantStatus *v1alpha1.Status
	}{
		{
			name: "a single plugin makes a Pod schedulable",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PostFilterStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Success, "injected status"),
		},
		{
			name: "plugin1 failed to make a Pod schedulable, followed by plugin2 which makes the Pod schedulable",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin1",
					inj:  injectedResult{PostFilterStatus: int(v1alpha1.Unschedulable)},
				},
				{
					name: "TestPlugin2",
					inj:  injectedResult{PostFilterStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Success, "injected status"),
		},
		{
			name: "plugin1 makes a Pod schedulable, followed by plugin2 which cannot make the Pod schedulable",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin1",
					inj:  injectedResult{PostFilterStatus: int(v1alpha1.Success)},
				},
				{
					name: "TestPlugin2",
					inj:  injectedResult{PostFilterStatus: int(v1alpha1.Unschedulable)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Success, "injected status"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := Registry{}
			cfgPls := &config.Plugins{PostFilter: &config.PluginSet{}}
			for _, pl := range tt.plugins {
				// register all plugins
				tmpPl := pl
				if err := registry.Register(pl.name,
					func(_ runtime.Object, _ v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
						return tmpPl, nil
					}); err != nil {
					t.Fatalf("fail to register postFilter plugin (%s)", pl.name)
				}
				// append plugins to filter pluginset
				cfgPls.PostFilter.Enabled = append(
					cfgPls.PostFilter.Enabled,
					config.Plugin{Name: pl.name},
				)
			}

			f, err := newFrameworkWithQueueSortAndBind(registry, cfgPls, emptyArgs)
			if err != nil {
				t.Fatalf("fail to create framework: %s", err)
			}
			_, gotStatus := f.RunPostFilterPlugins(context.TODO(), nil, pod, nil)
			if !reflect.DeepEqual(gotStatus, tt.wantStatus) {
				t.Errorf("Unexpected status. got: %v, want: %v", gotStatus, tt.wantStatus)
			}
		})
	}
}

func TestPreBindPlugins(t *testing.T) {
	tests := []struct {
		name       string
		plugins    []*TestPlugin
		wantStatus *v1alpha1.Status
	}{
		{
			name:       "NoPreBindPlugin",
			plugins:    []*TestPlugin{},
			wantStatus: nil,
		},
		{
			name: "SuccessPreBindPlugins",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus: nil,
		},
		{
			name: "UnshedulablePreBindPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Unschedulable)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running "TestPlugin" prebind plugin for pod "": injected status`),
		},
		{
			name: "ErrorPreBindPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Error)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running "TestPlugin" prebind plugin for pod "": injected status`),
		},
		{
			name: "UnschedulablePreBindPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.UnschedulableAndUnresolvable)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running "TestPlugin" prebind plugin for pod "": injected status`),
		},
		{
			name: "SuccessErrorPreBindPlugins",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Success)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Error)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running "TestPlugin 1" prebind plugin for pod "": injected status`),
		},
		{
			name: "ErrorSuccessPreBindPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Error)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running "TestPlugin" prebind plugin for pod "": injected status`),
		},
		{
			name: "SuccessSuccessPreBindPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Success)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus: nil,
		},
		{
			name: "ErrorAndErrorPlugins",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Error)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Error)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running "TestPlugin" prebind plugin for pod "": injected status`),
		},
		{
			name: "UnschedulableAndSuccessPreBindPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Unschedulable)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{PreBindStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running "TestPlugin" prebind plugin for pod "": injected status`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := Registry{}
			configPlugins := &config.Plugins{PreBind: &config.PluginSet{}}

			for _, pl := range tt.plugins {
				tmpPl := pl
				if err := registry.Register(pl.name, func(_ runtime.Object, _ v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
					return tmpPl, nil
				}); err != nil {
					t.Fatalf("Unable to register pre bind plugins: %s", pl.name)
				}

				configPlugins.PreBind.Enabled = append(
					configPlugins.PreBind.Enabled,
					config.Plugin{Name: pl.name},
				)
			}

			f, err := newFrameworkWithQueueSortAndBind(registry, configPlugins, emptyArgs)
			if err != nil {
				t.Fatalf("fail to create framework: %s", err)
			}

			status := f.RunPreBindPlugins(context.TODO(), nil, pod, "")

			if !reflect.DeepEqual(status, tt.wantStatus) {
				t.Errorf("wrong status code. got %v, want %v", status, tt.wantStatus)
			}
		})
	}
}

func TestReservePlugins(t *testing.T) {
	tests := []struct {
		name       string
		plugins    []*TestPlugin
		wantStatus *v1alpha1.Status
	}{
		{
			name:       "NoReservePlugin",
			plugins:    []*TestPlugin{},
			wantStatus: nil,
		},
		{
			name: "SuccessReservePlugins",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus: nil,
		},
		{
			name: "UnshedulableReservePlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Unschedulable)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running Reserve in "TestPlugin" reserve plugin for pod "": injected status`),
		},
		{
			name: "ErrorReservePlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Error)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running Reserve in "TestPlugin" reserve plugin for pod "": injected status`),
		},
		{
			name: "UnschedulableReservePlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.UnschedulableAndUnresolvable)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running Reserve in "TestPlugin" reserve plugin for pod "": injected status`),
		},
		{
			name: "SuccessSuccessReservePlugins",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Success)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus: nil,
		},
		{
			name: "ErrorErrorReservePlugins",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Error)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Error)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running Reserve in "TestPlugin" reserve plugin for pod "": injected status`),
		},
		{
			name: "SuccessErrorReservePlugins",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Success)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Error)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running Reserve in "TestPlugin 1" reserve plugin for pod "": injected status`),
		},
		{
			name: "ErrorSuccessReservePlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Error)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running Reserve in "TestPlugin" reserve plugin for pod "": injected status`),
		},
		{
			name: "UnschedulableAndSuccessReservePlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Unschedulable)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{ReserveStatus: int(v1alpha1.Success)},
				},
			},
			wantStatus: v1alpha1.NewStatus(v1alpha1.Error, `error while running Reserve in "TestPlugin" reserve plugin for pod "": injected status`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := Registry{}
			configPlugins := &config.Plugins{Reserve: &config.PluginSet{}}

			for _, pl := range tt.plugins {
				tmpPl := pl
				if err := registry.Register(pl.name, func(_ runtime.Object, _ v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
					return tmpPl, nil
				}); err != nil {
					t.Fatalf("Unable to register pre bind plugins: %s", pl.name)
				}

				configPlugins.Reserve.Enabled = append(
					configPlugins.Reserve.Enabled,
					config.Plugin{Name: pl.name},
				)
			}

			f, err := newFrameworkWithQueueSortAndBind(registry, configPlugins, emptyArgs)
			if err != nil {
				t.Fatalf("fail to create framework: %s", err)
			}

			status := f.RunReservePluginsReserve(context.TODO(), nil, pod, "")

			if !reflect.DeepEqual(status, tt.wantStatus) {
				t.Errorf("wrong status code. got %v, want %v", status, tt.wantStatus)
			}
		})
	}
}

func TestPermitPlugins(t *testing.T) {
	tests := []struct {
		name    string
		plugins []*TestPlugin
		want    *v1alpha1.Status
	}{
		{
			name:    "NilPermitPlugin",
			plugins: []*TestPlugin{},
			want:    nil,
		},
		{
			name: "SuccessPermitPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PermitStatus: int(v1alpha1.Success)},
				},
			},
			want: nil,
		},
		{
			name: "UnschedulablePermitPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PermitStatus: int(v1alpha1.Unschedulable)},
				},
			},
			want: v1alpha1.NewStatus(v1alpha1.Unschedulable, `rejected pod "" by permit plugin "TestPlugin": injected status`),
		},
		{
			name: "ErrorPermitPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PermitStatus: int(v1alpha1.Error)},
				},
			},
			want: v1alpha1.NewStatus(v1alpha1.Error, `error while running "TestPlugin" permit plugin for pod "": injected status`),
		},
		{
			name: "UnschedulableAndUnresolvablePermitPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PermitStatus: int(v1alpha1.UnschedulableAndUnresolvable)},
				},
			},
			want: v1alpha1.NewStatus(v1alpha1.UnschedulableAndUnresolvable, `rejected pod "" by permit plugin "TestPlugin": injected status`),
		},
		{
			name: "WaitPermitPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PermitStatus: int(v1alpha1.Wait)},
				},
			},
			want: v1alpha1.NewStatus(v1alpha1.Wait, `one or more plugins asked to wait and no plugin rejected pod ""`),
		},
		{
			name: "SuccessSuccessPermitPlugin",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PermitStatus: int(v1alpha1.Success)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{PermitStatus: int(v1alpha1.Success)},
				},
			},
			want: nil,
		},
		{
			name: "ErrorAndErrorPlugins",
			plugins: []*TestPlugin{
				{
					name: "TestPlugin",
					inj:  injectedResult{PermitStatus: int(v1alpha1.Error)},
				},
				{
					name: "TestPlugin 1",
					inj:  injectedResult{PermitStatus: int(v1alpha1.Error)},
				},
			},
			want: v1alpha1.NewStatus(v1alpha1.Error, `error while running "TestPlugin" permit plugin for pod "": injected status`),
		},
	}

	for _, tt := range tests {
		registry := Registry{}
		configPlugins := &config.Plugins{Permit: &config.PluginSet{}}

		for _, pl := range tt.plugins {
			tmpPl := pl
			if err := registry.Register(pl.name, func(_ runtime.Object, _ v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
				return tmpPl, nil
			}); err != nil {
				t.Fatalf("Unable to register Permit plugin: %s", pl.name)
			}

			configPlugins.Permit.Enabled = append(
				configPlugins.Permit.Enabled,
				config.Plugin{Name: pl.name},
			)
		}

		f, err := newFrameworkWithQueueSortAndBind(registry, configPlugins, emptyArgs)
		if err != nil {
			t.Fatalf("fail to create framework: %s", err)
		}

		status := f.RunPermitPlugins(context.TODO(), nil, pod, "")

		if !reflect.DeepEqual(status, tt.want) {
			t.Errorf("wrong status code. got %v, want %v", status, tt.want)
		}
	}
}

func TestRecordingMetrics(t *testing.T) {
	state := &v1alpha1.CycleState{}
	state.SetRecordPluginMetrics(true)

	tests := []struct {
		name               string
		action             func(f v1alpha1.Framework)
		inject             injectedResult
		wantExtensionPoint string
		wantStatus         v1alpha1.Code
	}{
		{
			name:               "PreFilter - Success",
			action:             func(f v1alpha1.Framework) { f.RunPreFilterPlugins(context.Background(), state, pod) },
			wantExtensionPoint: "PreFilter",
			wantStatus:         v1alpha1.Success,
		},
		{
			name:               "PreScore - Success",
			action:             func(f v1alpha1.Framework) { f.RunPreScorePlugins(context.Background(), state, pod, nil) },
			wantExtensionPoint: "PreScore",
			wantStatus:         v1alpha1.Success,
		},
		{
			name:               "Score - Success",
			action:             func(f v1alpha1.Framework) { f.RunScorePlugins(context.Background(), state, pod, nodes) },
			wantExtensionPoint: "Score",
			wantStatus:         v1alpha1.Success,
		},
		{
			name:               "Reserve - Success",
			action:             func(f v1alpha1.Framework) { f.RunReservePluginsReserve(context.Background(), state, pod, "") },
			wantExtensionPoint: "Reserve",
			wantStatus:         v1alpha1.Success,
		},
		{
			name:               "Unreserve - Success",
			action:             func(f v1alpha1.Framework) { f.RunReservePluginsUnreserve(context.Background(), state, pod, "") },
			wantExtensionPoint: "Unreserve",
			wantStatus:         v1alpha1.Success,
		},
		{
			name:               "PreBind - Success",
			action:             func(f v1alpha1.Framework) { f.RunPreBindPlugins(context.Background(), state, pod, "") },
			wantExtensionPoint: "PreBind",
			wantStatus:         v1alpha1.Success,
		},
		{
			name:               "Bind - Success",
			action:             func(f v1alpha1.Framework) { f.RunBindPlugins(context.Background(), state, pod, "") },
			wantExtensionPoint: "Bind",
			wantStatus:         v1alpha1.Success,
		},
		{
			name:               "PostBind - Success",
			action:             func(f v1alpha1.Framework) { f.RunPostBindPlugins(context.Background(), state, pod, "") },
			wantExtensionPoint: "PostBind",
			wantStatus:         v1alpha1.Success,
		},
		{
			name:               "Permit - Success",
			action:             func(f v1alpha1.Framework) { f.RunPermitPlugins(context.Background(), state, pod, "") },
			wantExtensionPoint: "Permit",
			wantStatus:         v1alpha1.Success,
		},

		{
			name:               "PreFilter - Error",
			action:             func(f v1alpha1.Framework) { f.RunPreFilterPlugins(context.Background(), state, pod) },
			inject:             injectedResult{PreFilterStatus: int(v1alpha1.Error)},
			wantExtensionPoint: "PreFilter",
			wantStatus:         v1alpha1.Error,
		},
		{
			name:               "PreScore - Error",
			action:             func(f v1alpha1.Framework) { f.RunPreScorePlugins(context.Background(), state, pod, nil) },
			inject:             injectedResult{PreScoreStatus: int(v1alpha1.Error)},
			wantExtensionPoint: "PreScore",
			wantStatus:         v1alpha1.Error,
		},
		{
			name:               "Score - Error",
			action:             func(f v1alpha1.Framework) { f.RunScorePlugins(context.Background(), state, pod, nodes) },
			inject:             injectedResult{ScoreStatus: int(v1alpha1.Error)},
			wantExtensionPoint: "Score",
			wantStatus:         v1alpha1.Error,
		},
		{
			name:               "Reserve - Error",
			action:             func(f v1alpha1.Framework) { f.RunReservePluginsReserve(context.Background(), state, pod, "") },
			inject:             injectedResult{ReserveStatus: int(v1alpha1.Error)},
			wantExtensionPoint: "Reserve",
			wantStatus:         v1alpha1.Error,
		},
		{
			name:               "PreBind - Error",
			action:             func(f v1alpha1.Framework) { f.RunPreBindPlugins(context.Background(), state, pod, "") },
			inject:             injectedResult{PreBindStatus: int(v1alpha1.Error)},
			wantExtensionPoint: "PreBind",
			wantStatus:         v1alpha1.Error,
		},
		{
			name:               "Bind - Error",
			action:             func(f v1alpha1.Framework) { f.RunBindPlugins(context.Background(), state, pod, "") },
			inject:             injectedResult{BindStatus: int(v1alpha1.Error)},
			wantExtensionPoint: "Bind",
			wantStatus:         v1alpha1.Error,
		},
		{
			name:               "Permit - Error",
			action:             func(f v1alpha1.Framework) { f.RunPermitPlugins(context.Background(), state, pod, "") },
			inject:             injectedResult{PermitStatus: int(v1alpha1.Error)},
			wantExtensionPoint: "Permit",
			wantStatus:         v1alpha1.Error,
		},
		{
			name:               "Permit - Wait",
			action:             func(f v1alpha1.Framework) { f.RunPermitPlugins(context.Background(), state, pod, "") },
			inject:             injectedResult{PermitStatus: int(v1alpha1.Wait)},
			wantExtensionPoint: "Permit",
			wantStatus:         v1alpha1.Wait,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics.Register()
			metrics.FrameworkExtensionPointDuration.Reset()
			metrics.PluginExecutionDuration.Reset()

			plugin := &TestPlugin{name: testPlugin, inj: tt.inject}
			r := make(Registry)
			r.Register(testPlugin,
				func(_ runtime.Object, fh v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
					return plugin, nil
				})
			pluginSet := &config.PluginSet{Enabled: []config.Plugin{{Name: testPlugin, Weight: 1}}}
			plugins := &config.Plugins{
				Score:     pluginSet,
				PreFilter: pluginSet,
				Filter:    pluginSet,
				PreScore:  pluginSet,
				Reserve:   pluginSet,
				Permit:    pluginSet,
				PreBind:   pluginSet,
				Bind:      pluginSet,
				PostBind:  pluginSet,
			}
			recorder := newMetricsRecorder(100, time.Nanosecond)
			f, err := newFrameworkWithQueueSortAndBind(r, plugins, emptyArgs, withMetricsRecorder(recorder), WithProfileName(testProfileName))
			if err != nil {
				t.Fatalf("Failed to create framework for testing: %v", err)
			}

			tt.action(f)

			// Stop the goroutine which records metrics and ensure it's stopped.
			close(recorder.stopCh)
			<-recorder.isStoppedCh
			// Try to clean up the metrics buffer again in case it's not empty.
			recorder.flushMetrics()

			collectAndCompareFrameworkMetrics(t, tt.wantExtensionPoint, tt.wantStatus)
			collectAndComparePluginMetrics(t, tt.wantExtensionPoint, testPlugin, tt.wantStatus)
		})
	}
}

func TestRunBindPlugins(t *testing.T) {
	tests := []struct {
		name       string
		injects    []v1alpha1.Code
		wantStatus v1alpha1.Code
	}{
		{
			name:       "simple success",
			injects:    []v1alpha1.Code{v1alpha1.Success},
			wantStatus: v1alpha1.Success,
		},
		{
			name:       "error on second",
			injects:    []v1alpha1.Code{v1alpha1.Skip, v1alpha1.Error, v1alpha1.Success},
			wantStatus: v1alpha1.Error,
		},
		{
			name:       "all skip",
			injects:    []v1alpha1.Code{v1alpha1.Skip, v1alpha1.Skip, v1alpha1.Skip},
			wantStatus: v1alpha1.Skip,
		},
		{
			name:       "error on third, but not reached",
			injects:    []v1alpha1.Code{v1alpha1.Skip, v1alpha1.Success, v1alpha1.Error},
			wantStatus: v1alpha1.Success,
		},
		{
			name:       "no bind plugin, returns default binder",
			injects:    []v1alpha1.Code{},
			wantStatus: v1alpha1.Success,
		},
		{
			name:       "invalid status",
			injects:    []v1alpha1.Code{v1alpha1.Unschedulable},
			wantStatus: v1alpha1.Error,
		},
		{
			name:       "simple error",
			injects:    []v1alpha1.Code{v1alpha1.Error},
			wantStatus: v1alpha1.Error,
		},
		{
			name:       "success on second, returns success",
			injects:    []v1alpha1.Code{v1alpha1.Skip, v1alpha1.Success},
			wantStatus: v1alpha1.Success,
		},
		{
			name:       "invalid status, returns error",
			injects:    []v1alpha1.Code{v1alpha1.Skip, v1alpha1.UnschedulableAndUnresolvable},
			wantStatus: v1alpha1.Error,
		},
		{
			name:       "error after success status, returns success",
			injects:    []v1alpha1.Code{v1alpha1.Success, v1alpha1.Error},
			wantStatus: v1alpha1.Success,
		},
		{
			name:       "success before invalid status, returns success",
			injects:    []v1alpha1.Code{v1alpha1.Success, v1alpha1.Error},
			wantStatus: v1alpha1.Success,
		},
		{
			name:       "success after error status, returns error",
			injects:    []v1alpha1.Code{v1alpha1.Error, v1alpha1.Success},
			wantStatus: v1alpha1.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics.Register()
			metrics.FrameworkExtensionPointDuration.Reset()
			metrics.PluginExecutionDuration.Reset()

			pluginSet := &config.PluginSet{}
			r := make(Registry)
			for i, inj := range tt.injects {
				name := fmt.Sprintf("bind-%d", i)
				plugin := &TestPlugin{name: name, inj: injectedResult{BindStatus: int(inj)}}
				r.Register(name,
					func(_ runtime.Object, fh v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
						return plugin, nil
					})
				pluginSet.Enabled = append(pluginSet.Enabled, config.Plugin{Name: name})
			}
			plugins := &config.Plugins{Bind: pluginSet}
			recorder := newMetricsRecorder(100, time.Nanosecond)
			fwk, err := newFrameworkWithQueueSortAndBind(r, plugins, emptyArgs, withMetricsRecorder(recorder), WithProfileName(testProfileName))
			if err != nil {
				t.Fatal(err)
			}

			st := fwk.RunBindPlugins(context.Background(), state, pod, "")
			if st.Code() != tt.wantStatus {
				t.Errorf("got status code %s, want %s", st.Code(), tt.wantStatus)
			}

			// Stop the goroutine which records metrics and ensure it's stopped.
			close(recorder.stopCh)
			<-recorder.isStoppedCh
			// Try to clean up the metrics buffer again in case it's not empty.
			recorder.flushMetrics()
			collectAndCompareFrameworkMetrics(t, "Bind", tt.wantStatus)
		})
	}
}

func TestPermitWaitDurationMetric(t *testing.T) {
	tests := []struct {
		name    string
		inject  injectedResult
		wantRes string
	}{
		{
			name: "WaitOnPermit - No Wait",
		},
		{
			name:    "WaitOnPermit - Wait Timeout",
			inject:  injectedResult{PermitStatus: int(v1alpha1.Wait)},
			wantRes: "Unschedulable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics.Register()
			metrics.PermitWaitDuration.Reset()

			plugin := &TestPlugin{name: testPlugin, inj: tt.inject}
			r := make(Registry)
			err := r.Register(testPlugin,
				func(_ runtime.Object, fh v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
					return plugin, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			plugins := &config.Plugins{
				Permit: &config.PluginSet{Enabled: []config.Plugin{{Name: testPlugin, Weight: 1}}},
			}
			f, err := newFrameworkWithQueueSortAndBind(r, plugins, emptyArgs)
			if err != nil {
				t.Fatalf("Failed to create framework for testing: %v", err)
			}

			f.RunPermitPlugins(context.TODO(), nil, pod, "")
			f.WaitOnPermit(context.TODO(), pod)

			collectAndComparePermitWaitDuration(t, tt.wantRes)
		})
	}
}

func TestWaitOnPermit(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pod",
			UID:  types.UID("pod"),
		},
	}

	tests := []struct {
		name        string
		action      func(f v1alpha1.Framework)
		wantStatus  v1alpha1.Code
		wantMessage string
	}{
		{
			name: "Reject Waiting Pod",
			action: func(f v1alpha1.Framework) {
				f.GetWaitingPod(pod.UID).Reject("reject message")
			},
			wantStatus:  v1alpha1.Unschedulable,
			wantMessage: "pod \"pod\" rejected while waiting on permit: reject message",
		},
		{
			name: "Allow Waiting Pod",
			action: func(f v1alpha1.Framework) {
				f.GetWaitingPod(pod.UID).Allow(permitPlugin)
			},
			wantStatus:  v1alpha1.Success,
			wantMessage: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testPermitPlugin := &TestPermitPlugin{}
			r := make(Registry)
			r.Register(permitPlugin,
				func(_ runtime.Object, fh v1alpha1.FrameworkHandle) (v1alpha1.Plugin, error) {
					return testPermitPlugin, nil
				})
			plugins := &config.Plugins{
				Permit: &config.PluginSet{Enabled: []config.Plugin{{Name: permitPlugin, Weight: 1}}},
			}

			f, err := newFrameworkWithQueueSortAndBind(r, plugins, emptyArgs)
			if err != nil {
				t.Fatalf("Failed to create framework for testing: %v", err)
			}

			runPermitPluginsStatus := f.RunPermitPlugins(context.Background(), nil, pod, "")
			if runPermitPluginsStatus.Code() != v1alpha1.Wait {
				t.Fatalf("Expected RunPermitPlugins to return status %v, but got %v",
					v1alpha1.Wait, runPermitPluginsStatus.Code())
			}

			go tt.action(f)

			waitOnPermitStatus := f.WaitOnPermit(context.Background(), pod)
			if waitOnPermitStatus.Code() != tt.wantStatus {
				t.Fatalf("Expected WaitOnPermit to return status %v, but got %v",
					tt.wantStatus, waitOnPermitStatus.Code())
			}
			if waitOnPermitStatus.Message() != tt.wantMessage {
				t.Fatalf("Expected WaitOnPermit to return status with message %q, but got %q",
					tt.wantMessage, waitOnPermitStatus.Message())
			}
		})
	}
}

func TestListPlugins(t *testing.T) {
	tests := []struct {
		name    string
		plugins *config.Plugins
		// pluginSetCount include queue sort plugin and bind plugin.
		pluginSetCount int
	}{
		{
			name:           "Add empty plugin",
			plugins:        &config.Plugins{},
			pluginSetCount: 2,
		},
		{
			name: "Add multiple plugins",
			plugins: &config.Plugins{
				Score: &config.PluginSet{Enabled: []config.Plugin{{Name: scorePlugin1}, {Name: scoreWithNormalizePlugin1}}},
			},
			pluginSetCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := newFrameworkWithQueueSortAndBind(registry, tt.plugins, emptyArgs)
			if err != nil {
				t.Fatalf("Failed to create framework for testing: %v", err)
			}
			plugins := f.ListPlugins()
			if len(plugins) != tt.pluginSetCount {
				t.Fatalf("Unexpected pluginSet count: %v", len(plugins))
			}
		})
	}
}

func buildScoreConfigDefaultWeights(ps ...string) *config.Plugins {
	return buildScoreConfigWithWeights(defaultWeights, ps...)
}

func buildScoreConfigWithWeights(weights map[string]int32, ps ...string) *config.Plugins {
	var plugins []config.Plugin
	for _, p := range ps {
		plugins = append(plugins, config.Plugin{Name: p, Weight: weights[p]})
	}
	return &config.Plugins{Score: &config.PluginSet{Enabled: plugins}}
}

type injectedResult struct {
	ScoreRes                 int64 `json:"scoreRes,omitempty"`
	NormalizeRes             int64 `json:"normalizeRes,omitempty"`
	ScoreStatus              int   `json:"scoreStatus,omitempty"`
	NormalizeStatus          int   `json:"normalizeStatus,omitempty"`
	PreFilterStatus          int   `json:"preFilterStatus,omitempty"`
	PreFilterAddPodStatus    int   `json:"preFilterAddPodStatus,omitempty"`
	PreFilterRemovePodStatus int   `json:"preFilterRemovePodStatus,omitempty"`
	FilterStatus             int   `json:"filterStatus,omitempty"`
	PostFilterStatus         int   `json:"postFilterStatus,omitempty"`
	PreScoreStatus           int   `json:"preScoreStatus,omitempty"`
	ReserveStatus            int   `json:"reserveStatus,omitempty"`
	PreBindStatus            int   `json:"preBindStatus,omitempty"`
	BindStatus               int   `json:"bindStatus,omitempty"`
	PermitStatus             int   `json:"permitStatus,omitempty"`
}

func setScoreRes(inj injectedResult) (int64, *v1alpha1.Status) {
	if v1alpha1.Code(inj.ScoreStatus) != v1alpha1.Success {
		return 0, v1alpha1.NewStatus(v1alpha1.Code(inj.ScoreStatus), "injecting failure.")
	}
	return inj.ScoreRes, nil
}

func injectNormalizeRes(inj injectedResult, scores v1alpha1.NodeScoreList) *v1alpha1.Status {
	if v1alpha1.Code(inj.NormalizeStatus) != v1alpha1.Success {
		return v1alpha1.NewStatus(v1alpha1.Code(inj.NormalizeStatus), "injecting failure.")
	}
	for i := range scores {
		scores[i].Score = inj.NormalizeRes
	}
	return nil
}

func collectAndComparePluginMetrics(t *testing.T, wantExtensionPoint, wantPlugin string, wantStatus v1alpha1.Code) {
	t.Helper()
	m := collectHistogramMetric(metrics.PluginExecutionDuration)
	if len(m.Label) != 3 {
		t.Fatalf("Unexpected number of label pairs, got: %v, want: 2", len(m.Label))
	}

	if *m.Label[0].Value != wantExtensionPoint {
		t.Errorf("Unexpected extension point label, got: %q, want %q", *m.Label[0].Value, wantExtensionPoint)
	}

	if *m.Label[1].Value != wantPlugin {
		t.Errorf("Unexpected plugin label, got: %q, want %q", *m.Label[1].Value, wantPlugin)
	}

	if *m.Label[2].Value != wantStatus.String() {
		t.Errorf("Unexpected status code label, got: %q, want %q", *m.Label[2].Value, wantStatus)
	}

	if *m.Histogram.SampleCount == 0 {
		t.Error("Expect at least 1 sample")
	}

	if *m.Histogram.SampleSum <= 0 {
		t.Errorf("Expect latency to be greater than 0, got: %v", *m.Histogram.SampleSum)
	}
}

func collectAndCompareFrameworkMetrics(t *testing.T, wantExtensionPoint string, wantStatus v1alpha1.Code) {
	t.Helper()
	m := collectHistogramMetric(metrics.FrameworkExtensionPointDuration)

	gotLabels := make(map[string]string, len(m.Label))
	for _, p := range m.Label {
		gotLabels[p.GetName()] = p.GetValue()
	}
	wantLabels := map[string]string{
		"extension_point": wantExtensionPoint,
		"status":          wantStatus.String(),
		"profile":         testProfileName,
	}
	if diff := cmp.Diff(wantLabels, gotLabels); diff != "" {
		t.Errorf("unexpected labels (-want,+got):\n%s", diff)
	}

	if *m.Histogram.SampleCount != 1 {
		t.Errorf("Expect 1 sample, got: %v", *m.Histogram.SampleCount)
	}

	if *m.Histogram.SampleSum <= 0 {
		t.Errorf("Expect latency to be greater than 0, got: %v", *m.Histogram.SampleSum)
	}
}

func collectAndComparePermitWaitDuration(t *testing.T, wantRes string) {
	m := collectHistogramMetric(metrics.PermitWaitDuration)
	if wantRes == "" {
		if m != nil {
			t.Errorf("PermitWaitDuration shouldn't be recorded but got %+v", m)
		}
		return
	}
	if wantRes != "" {
		if len(m.Label) != 1 {
			t.Fatalf("Unexpected number of label pairs, got: %v, want: 1", len(m.Label))
		}

		if *m.Label[0].Value != wantRes {
			t.Errorf("Unexpected result label, got: %q, want %q", *m.Label[0].Value, wantRes)
		}

		if *m.Histogram.SampleCount != 1 {
			t.Errorf("Expect 1 sample, got: %v", *m.Histogram.SampleCount)
		}

		if *m.Histogram.SampleSum <= 0 {
			t.Errorf("Expect latency to be greater than 0, got: %v", *m.Histogram.SampleSum)
		}
	}
}

func collectHistogramMetric(metric prometheus.Collector) *dto.Metric {
	ch := make(chan prometheus.Metric, 100)
	metric.Collect(ch)
	select {
	case got := <-ch:
		m := &dto.Metric{}
		got.Write(m)
		return m
	default:
		return nil
	}
}
