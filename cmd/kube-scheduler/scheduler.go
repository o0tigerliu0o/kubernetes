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

package main

import (
	"math/rand"
	"os"
	"time"

	"github.com/spf13/pflag"

	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/logs"
	_ "k8s.io/component-base/metrics/prometheus/clientgo"
	_ "k8s.io/component-base/metrics/prometheus/version" // for version metric registration
	"kubernetes/cmd/kube-scheduler/app"
)

func main() {
	// 组件全局随机数生成对象
	rand.Seed(time.Now().UnixNano())
	// 实例化命令行参数。通过fags对命令行参数进行解析并存储至Options对象中
	command := app.NewSchedulerCommand()

	// TODO: once we switch everything over to Cobra commands, we can go back to calling
	// utilflag.InitFlags() (by removing its pflag.Parse() call). For now, we have to set the
	// normalize func and add the go flag set by hand.
	pflag.CommandLine.SetNormalizeFunc(cliflag.WordSepNormalizeFunc)
	// utilflag.InitFlags()
	// 实例化日志对象，用于日志管理
	logs.InitLogs()
	defer logs.FlushLogs()
	// 组件进程运行的逻辑。运行前通过Complete函数填充默认参数，通过Validate函数验证所有参数，
	//最后通过Run函数持久运行。只有当进程收到退出信号时，进程才会退出。
	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}
