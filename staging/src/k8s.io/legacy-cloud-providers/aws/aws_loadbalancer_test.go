// +build !providerless

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

package aws

import (
	"k8s.io/apimachinery/pkg/types"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/elb"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/stretchr/testify/assert"

	"k8s.io/api/core/v1"
)

func TestElbProtocolsAreEqual(t *testing.T) {
	grid := []struct {
		L        *string
		R        *string
		Expected bool
	}{
		{
			L:        aws.String("http"),
			R:        aws.String("http"),
			Expected: true,
		},
		{
			L:        aws.String("HTTP"),
			R:        aws.String("http"),
			Expected: true,
		},
		{
			L:        aws.String("HTTP"),
			R:        aws.String("TCP"),
			Expected: false,
		},
		{
			L:        aws.String(""),
			R:        aws.String("TCP"),
			Expected: false,
		},
		{
			L:        aws.String(""),
			R:        aws.String(""),
			Expected: true,
		},
		{
			L:        nil,
			R:        aws.String(""),
			Expected: false,
		},
		{
			L:        aws.String(""),
			R:        nil,
			Expected: false,
		},
		{
			L:        nil,
			R:        nil,
			Expected: true,
		},
	}
	for _, g := range grid {
		actual := elbProtocolsAreEqual(g.L, g.R)
		if actual != g.Expected {
			t.Errorf("unexpected result from protocolsEquals(%v, %v)", g.L, g.R)
		}
	}
}

func TestAWSARNEquals(t *testing.T) {
	grid := []struct {
		L        *string
		R        *string
		Expected bool
	}{
		{
			L:        aws.String("arn:aws:acm:us-east-1:123456789012:certificate/12345678-1234-1234-1234-123456789012"),
			R:        aws.String("arn:aws:acm:us-east-1:123456789012:certificate/12345678-1234-1234-1234-123456789012"),
			Expected: true,
		},
		{
			L:        aws.String("ARN:AWS:ACM:US-EAST-1:123456789012:CERTIFICATE/12345678-1234-1234-1234-123456789012"),
			R:        aws.String("arn:aws:acm:us-east-1:123456789012:certificate/12345678-1234-1234-1234-123456789012"),
			Expected: true,
		},
		{
			L:        aws.String("arn:aws:acm:us-east-1:123456789012:certificate/12345678-1234-1234-1234-123456789012"),
			R:        aws.String(""),
			Expected: false,
		},
		{
			L:        aws.String(""),
			R:        aws.String(""),
			Expected: true,
		},
		{
			L:        nil,
			R:        aws.String(""),
			Expected: false,
		},
		{
			L:        aws.String(""),
			R:        nil,
			Expected: false,
		},
		{
			L:        nil,
			R:        nil,
			Expected: true,
		},
	}
	for _, g := range grid {
		actual := awsArnEquals(g.L, g.R)
		if actual != g.Expected {
			t.Errorf("unexpected result from awsArnEquals(%v, %v)", g.L, g.R)
		}
	}
}

func TestIsNLB(t *testing.T) {
	tests := []struct {
		name string

		annotations map[string]string
		want        bool
	}{
		{
			"NLB annotation provided",
			map[string]string{"service.beta.kubernetes.io/aws-load-balancer-type": "nlb"},
			true,
		},
		{
			"NLB annotation has invalid value",
			map[string]string{"service.beta.kubernetes.io/aws-load-balancer-type": "elb"},
			false,
		},
		{
			"NLB annotation absent",
			map[string]string{},
			false,
		},
	}

	for _, test := range tests {
		t.Logf("Running test case %s", test.name)
		got := isNLB(test.annotations)

		if got != test.want {
			t.Errorf("Incorrect value for isNLB() case %s. Got %t, expected %t.", test.name, got, test.want)
		}
	}
}

func TestIsLBExternal(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		want        bool
	}{
		{
			name:        "No annotation",
			annotations: map[string]string{},
			want:        false,
		},
		{
			name:        "Type NLB",
			annotations: map[string]string{"service.beta.kubernetes.io/aws-load-balancer-type": "nlb"},
			want:        false,
		},
		{
			name:        "Type NLB-IP",
			annotations: map[string]string{"service.beta.kubernetes.io/aws-load-balancer-type": "nlb-ip"},
			want:        true,
		},
		{
			name:        "Type External",
			annotations: map[string]string{"service.beta.kubernetes.io/aws-load-balancer-type": "external"},
			want:        true,
		},
	}
	for _, test := range tests {
		t.Logf("Running test case %s", test.name)
		got := isLBExternal(test.annotations)

		if got != test.want {
			t.Errorf("Incorrect value for isLBExternal() case %s. Got %t, expected %t.", test.name, got, test.want)
		}
	}
}

func TestSyncElbListeners(t *testing.T) {
	tests := []struct {
		name                 string
		loadBalancerName     string
		listeners            []*elb.Listener
		listenerDescriptions []*elb.ListenerDescription
		toCreate             []*elb.Listener
		toDelete             []*int64
	}{
		{
			name:             "no edge cases",
			loadBalancerName: "lb_one",
			listeners: []*elb.Listener{
				{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("HTTP"), LoadBalancerPort: aws.Int64(443), Protocol: aws.String("HTTP"), SSLCertificateId: aws.String("abc-123")},
				{InstancePort: aws.Int64(80), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP"), SSLCertificateId: aws.String("def-456")},
				{InstancePort: aws.Int64(8443), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(8443), Protocol: aws.String("TCP"), SSLCertificateId: aws.String("def-456")},
			},
			listenerDescriptions: []*elb.ListenerDescription{
				{Listener: &elb.Listener{InstancePort: aws.Int64(80), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP")}},
				{Listener: &elb.Listener{InstancePort: aws.Int64(8443), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(8443), Protocol: aws.String("TCP"), SSLCertificateId: aws.String("def-456")}},
			},
			toDelete: []*int64{
				aws.Int64(80),
			},
			toCreate: []*elb.Listener{
				{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("HTTP"), LoadBalancerPort: aws.Int64(443), Protocol: aws.String("HTTP"), SSLCertificateId: aws.String("abc-123")},
				{InstancePort: aws.Int64(80), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP"), SSLCertificateId: aws.String("def-456")},
			},
		},
		{
			name:             "no listeners to delete",
			loadBalancerName: "lb_two",
			listeners: []*elb.Listener{
				{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("HTTP"), LoadBalancerPort: aws.Int64(443), Protocol: aws.String("HTTP"), SSLCertificateId: aws.String("abc-123")},
				{InstancePort: aws.Int64(80), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP"), SSLCertificateId: aws.String("def-456")},
			},
			listenerDescriptions: []*elb.ListenerDescription{
				{Listener: &elb.Listener{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("HTTP"), LoadBalancerPort: aws.Int64(443), Protocol: aws.String("HTTP"), SSLCertificateId: aws.String("abc-123")}},
			},
			toCreate: []*elb.Listener{
				{InstancePort: aws.Int64(80), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP"), SSLCertificateId: aws.String("def-456")},
			},
			toDelete: []*int64{},
		},
		{
			name:             "no listeners to create",
			loadBalancerName: "lb_three",
			listeners: []*elb.Listener{
				{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("HTTP"), LoadBalancerPort: aws.Int64(443), Protocol: aws.String("HTTP"), SSLCertificateId: aws.String("abc-123")},
			},
			listenerDescriptions: []*elb.ListenerDescription{
				{Listener: &elb.Listener{InstancePort: aws.Int64(80), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP")}},
				{Listener: &elb.Listener{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("HTTP"), LoadBalancerPort: aws.Int64(443), Protocol: aws.String("HTTP"), SSLCertificateId: aws.String("abc-123")}},
			},
			toDelete: []*int64{
				aws.Int64(80),
			},
			toCreate: []*elb.Listener{},
		},
		{
			name:             "nil actual listener",
			loadBalancerName: "lb_four",
			listeners: []*elb.Listener{
				{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("HTTP"), LoadBalancerPort: aws.Int64(443), Protocol: aws.String("HTTP")},
			},
			listenerDescriptions: []*elb.ListenerDescription{
				{Listener: &elb.Listener{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("HTTP"), LoadBalancerPort: aws.Int64(443), Protocol: aws.String("HTTP"), SSLCertificateId: aws.String("abc-123")}},
				{Listener: nil},
			},
			toDelete: []*int64{
				aws.Int64(443),
			},
			toCreate: []*elb.Listener{
				{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("HTTP"), LoadBalancerPort: aws.Int64(443), Protocol: aws.String("HTTP")},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			additions, removals := syncElbListeners(test.loadBalancerName, test.listeners, test.listenerDescriptions)
			assert.Equal(t, additions, test.toCreate)
			assert.Equal(t, removals, test.toDelete)
		})
	}
}

func TestElbListenersAreEqual(t *testing.T) {
	tests := []struct {
		name             string
		expected, actual *elb.Listener
		equal            bool
	}{
		{
			name:     "should be equal",
			expected: &elb.Listener{InstancePort: aws.Int64(80), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP")},
			actual:   &elb.Listener{InstancePort: aws.Int64(80), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP")},
			equal:    true,
		},
		{
			name:     "instance port should be different",
			expected: &elb.Listener{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP")},
			actual:   &elb.Listener{InstancePort: aws.Int64(80), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP")},
			equal:    false,
		},
		{
			name:     "instance protocol should be different",
			expected: &elb.Listener{InstancePort: aws.Int64(80), InstanceProtocol: aws.String("HTTP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP")},
			actual:   &elb.Listener{InstancePort: aws.Int64(80), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP")},
			equal:    false,
		},
		{
			name:     "load balancer port should be different",
			expected: &elb.Listener{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(443), Protocol: aws.String("TCP")},
			actual:   &elb.Listener{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP")},
			equal:    false,
		},
		{
			name:     "protocol should be different",
			expected: &elb.Listener{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("TCP")},
			actual:   &elb.Listener{InstancePort: aws.Int64(443), InstanceProtocol: aws.String("TCP"), LoadBalancerPort: aws.Int64(80), Protocol: aws.String("HTTP")},
			equal:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.equal, elbListenersAreEqual(test.expected, test.actual))
		})
	}
}

func TestBuildTargetGroupName(t *testing.T) {
	type args struct {
		serviceName    types.NamespacedName
		servicePort    int64
		nodePort       int64
		targetProtocol string
		targetType     string
	}
	tests := []struct {
		name      string
		clusterID string
		args      args
		want      string
	}{
		{
			name:      "base case",
			clusterID: "cluster-a",
			args: args{
				serviceName:    types.NamespacedName{Namespace: "default", Name: "service-a"},
				servicePort:    80,
				nodePort:       8080,
				targetProtocol: "TCP",
				targetType:     "instance",
			},
			want: "k8s-default-servicea-0aeb5b75af",
		},
		{
			name:      "base case & clusterID changed",
			clusterID: "cluster-b",
			args: args{
				serviceName:    types.NamespacedName{Namespace: "default", Name: "service-a"},
				servicePort:    80,
				nodePort:       8080,
				targetProtocol: "TCP",
				targetType:     "instance",
			},
			want: "k8s-default-servicea-5d3a0a69a8",
		},
		{
			name:      "base case & serviceNamespace changed",
			clusterID: "cluster-a",
			args: args{
				serviceName:    types.NamespacedName{Namespace: "another", Name: "service-a"},
				servicePort:    80,
				nodePort:       8080,
				targetProtocol: "TCP",
				targetType:     "instance",
			},
			want: "k8s-another-servicea-f3a3263315",
		},
		{
			name:      "base case & serviceName changed",
			clusterID: "cluster-a",
			args: args{
				serviceName:    types.NamespacedName{Namespace: "default", Name: "service-b"},
				servicePort:    80,
				nodePort:       8080,
				targetProtocol: "TCP",
				targetType:     "instance",
			},
			want: "k8s-default-serviceb-9a3c03b25e",
		},
		{
			name:      "base case & servicePort changed",
			clusterID: "cluster-a",
			args: args{
				serviceName:    types.NamespacedName{Namespace: "default", Name: "service-a"},
				servicePort:    9090,
				nodePort:       8080,
				targetProtocol: "TCP",
				targetType:     "instance",
			},
			want: "k8s-default-servicea-6e07474ff4",
		},
		{
			name:      "base case & nodePort changed",
			clusterID: "cluster-a",
			args: args{
				serviceName:    types.NamespacedName{Namespace: "default", Name: "service-a"},
				servicePort:    80,
				nodePort:       9090,
				targetProtocol: "TCP",
				targetType:     "instance",
			},
			want: "k8s-default-servicea-6cb2d0201c",
		},
		{
			name:      "base case & targetProtocol changed",
			clusterID: "cluster-a",
			args: args{
				serviceName:    types.NamespacedName{Namespace: "default", Name: "service-a"},
				servicePort:    80,
				nodePort:       8080,
				targetProtocol: "UDP",
				targetType:     "instance",
			},
			want: "k8s-default-servicea-70495e628e",
		},
		{
			name:      "base case & targetType changed",
			clusterID: "cluster-a",
			args: args{
				serviceName:    types.NamespacedName{Namespace: "default", Name: "service-a"},
				servicePort:    80,
				nodePort:       8080,
				targetProtocol: "TCP",
				targetType:     "ip",
			},
			want: "k8s-default-servicea-fff6dd8028",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cloud{
				tagging: awsTagging{ClusterID: tt.clusterID},
			}
			if got := c.buildTargetGroupName(tt.args.serviceName, tt.args.servicePort, tt.args.nodePort, tt.args.targetProtocol, tt.args.targetType); got != tt.want {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestFilterTargetNodes(t *testing.T) {
	tests := []struct {
		name                    string
		nodeLabels, annotations map[string]string
		nodeTargeted            bool
	}{
		{
			name:         "when no filter is provided, node should be targeted",
			nodeLabels:   map[string]string{"k1": "v1"},
			nodeTargeted: true,
		},
		{
			name:         "when all key-value filters match, node should be targeted",
			nodeLabels:   map[string]string{"k1": "v1", "k2": "v2"},
			annotations:  map[string]string{ServiceAnnotationLoadBalancerTargetNodeLabels: "k1=v1,k2=v2"},
			nodeTargeted: true,
		},
		{
			name:         "when all just-key filter match, node should be targeted",
			nodeLabels:   map[string]string{"k1": "v1", "k2": "v2"},
			annotations:  map[string]string{ServiceAnnotationLoadBalancerTargetNodeLabels: "k1,k2"},
			nodeTargeted: true,
		},
		{
			name:         "when some filters do not match, node should not be targeted",
			nodeLabels:   map[string]string{"k1": "v1"},
			annotations:  map[string]string{ServiceAnnotationLoadBalancerTargetNodeLabels: "k1=v1,k2"},
			nodeTargeted: false,
		},
		{
			name:         "when no filter matches, node should not be targeted",
			nodeLabels:   map[string]string{"k1": "v1", "k2": "v2"},
			annotations:  map[string]string{ServiceAnnotationLoadBalancerTargetNodeLabels: "k3=v3"},
			nodeTargeted: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := &v1.Node{}
			node.Labels = test.nodeLabels

			nodes := []*v1.Node{node}
			targetNodes := filterTargetNodes(nodes, test.annotations)

			if test.nodeTargeted {
				assert.Equal(t, nodes, targetNodes)
			} else {
				assert.Empty(t, targetNodes)
			}
		})
	}
}

func TestCloud_chunkTargetDescriptions(t *testing.T) {
	type args struct {
		targets   []*elbv2.TargetDescription
		chunkSize int
	}
	tests := []struct {
		name string
		args args
		want [][]*elbv2.TargetDescription
	}{
		{
			name: "can be evenly chunked",
			args: args{
				targets: []*elbv2.TargetDescription{
					{
						Id:   aws.String("i-abcdefg1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg2"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg3"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg4"),
						Port: aws.Int64(8080),
					},
				},
				chunkSize: 2,
			},
			want: [][]*elbv2.TargetDescription{
				{
					{
						Id:   aws.String("i-abcdefg1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg2"),
						Port: aws.Int64(8080),
					},
				},
				{
					{
						Id:   aws.String("i-abcdefg3"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg4"),
						Port: aws.Int64(8080),
					},
				},
			},
		},
		{
			name: "cannot be evenly chunked",
			args: args{
				targets: []*elbv2.TargetDescription{
					{
						Id:   aws.String("i-abcdefg1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg2"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg3"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg4"),
						Port: aws.Int64(8080),
					},
				},
				chunkSize: 3,
			},
			want: [][]*elbv2.TargetDescription{
				{
					{
						Id:   aws.String("i-abcdefg1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg2"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg3"),
						Port: aws.Int64(8080),
					},
				},
				{

					{
						Id:   aws.String("i-abcdefg4"),
						Port: aws.Int64(8080),
					},
				},
			},
		},
		{
			name: "chunkSize equal to total count",
			args: args{
				targets: []*elbv2.TargetDescription{
					{
						Id:   aws.String("i-abcdefg1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg2"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg3"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg4"),
						Port: aws.Int64(8080),
					},
				},
				chunkSize: 4,
			},
			want: [][]*elbv2.TargetDescription{
				{
					{
						Id:   aws.String("i-abcdefg1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg2"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg3"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg4"),
						Port: aws.Int64(8080),
					},
				},
			},
		},
		{
			name: "chunkSize greater than total count",
			args: args{
				targets: []*elbv2.TargetDescription{
					{
						Id:   aws.String("i-abcdefg1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg2"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg3"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg4"),
						Port: aws.Int64(8080),
					},
				},
				chunkSize: 10,
			},
			want: [][]*elbv2.TargetDescription{
				{
					{
						Id:   aws.String("i-abcdefg1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg2"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg3"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdefg4"),
						Port: aws.Int64(8080),
					},
				},
			},
		},
		{
			name: "chunk nil slice",
			args: args{
				targets:   nil,
				chunkSize: 2,
			},
			want: nil,
		},
		{
			name: "chunk empty slice",
			args: args{
				targets:   []*elbv2.TargetDescription{},
				chunkSize: 2,
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cloud{}
			got := c.chunkTargetDescriptions(tt.args.targets, tt.args.chunkSize)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCloud_diffTargetGroupTargets(t *testing.T) {
	type args struct {
		expectedTargets []*elbv2.TargetDescription
		actualTargets   []*elbv2.TargetDescription
	}
	tests := []struct {
		name                    string
		args                    args
		wantTargetsToRegister   []*elbv2.TargetDescription
		wantTargetsToDeregister []*elbv2.TargetDescription
	}{
		{
			name: "all targets to register",
			args: args{
				expectedTargets: []*elbv2.TargetDescription{
					{
						Id:   aws.String("i-abcdef1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdef2"),
						Port: aws.Int64(8080),
					},
				},
				actualTargets: nil,
			},
			wantTargetsToRegister: []*elbv2.TargetDescription{
				{
					Id:   aws.String("i-abcdef1"),
					Port: aws.Int64(8080),
				},
				{
					Id:   aws.String("i-abcdef2"),
					Port: aws.Int64(8080),
				},
			},
			wantTargetsToDeregister: nil,
		},
		{
			name: "all targets to deregister",
			args: args{
				expectedTargets: nil,
				actualTargets: []*elbv2.TargetDescription{
					{
						Id:   aws.String("i-abcdef1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdef2"),
						Port: aws.Int64(8080),
					},
				},
			},
			wantTargetsToRegister: nil,
			wantTargetsToDeregister: []*elbv2.TargetDescription{
				{
					Id:   aws.String("i-abcdef1"),
					Port: aws.Int64(8080),
				},
				{
					Id:   aws.String("i-abcdef2"),
					Port: aws.Int64(8080),
				},
			},
		},
		{
			name: "some targets to register and deregister",
			args: args{
				expectedTargets: []*elbv2.TargetDescription{
					{
						Id:   aws.String("i-abcdef1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdef4"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdef5"),
						Port: aws.Int64(8080),
					},
				},
				actualTargets: []*elbv2.TargetDescription{
					{
						Id:   aws.String("i-abcdef1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdef2"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdef3"),
						Port: aws.Int64(8080),
					},
				},
			},
			wantTargetsToRegister: []*elbv2.TargetDescription{
				{
					Id:   aws.String("i-abcdef4"),
					Port: aws.Int64(8080),
				},
				{
					Id:   aws.String("i-abcdef5"),
					Port: aws.Int64(8080),
				},
			},
			wantTargetsToDeregister: []*elbv2.TargetDescription{
				{
					Id:   aws.String("i-abcdef2"),
					Port: aws.Int64(8080),
				},
				{
					Id:   aws.String("i-abcdef3"),
					Port: aws.Int64(8080),
				},
			},
		},
		{
			name: "both expected and actual targets are empty",
			args: args{
				expectedTargets: nil,
				actualTargets:   nil,
			},
			wantTargetsToRegister:   nil,
			wantTargetsToDeregister: nil,
		},
		{
			name: "expected and actual targets equals",
			args: args{
				expectedTargets: []*elbv2.TargetDescription{
					{
						Id:   aws.String("i-abcdef1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdef2"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdef3"),
						Port: aws.Int64(8080),
					},
				},
				actualTargets: []*elbv2.TargetDescription{
					{
						Id:   aws.String("i-abcdef1"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdef2"),
						Port: aws.Int64(8080),
					},
					{
						Id:   aws.String("i-abcdef3"),
						Port: aws.Int64(8080),
					},
				},
			},
			wantTargetsToRegister:   nil,
			wantTargetsToDeregister: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cloud{}
			gotTargetsToRegister, gotTargetsToDeregister := c.diffTargetGroupTargets(tt.args.expectedTargets, tt.args.actualTargets)
			assert.Equal(t, tt.wantTargetsToRegister, gotTargetsToRegister)
			assert.Equal(t, tt.wantTargetsToDeregister, gotTargetsToDeregister)
		})
	}
}

func TestCloud_computeTargetGroupExpectedTargets(t *testing.T) {
	type args struct {
		instanceIDs []string
		port        int64
	}
	tests := []struct {
		name string
		args args
		want []*elbv2.TargetDescription
	}{
		{
			name: "no instance",
			args: args{
				instanceIDs: nil,
				port:        8080,
			},
			want: []*elbv2.TargetDescription{},
		},
		{
			name: "one instance",
			args: args{
				instanceIDs: []string{"i-abcdef1"},
				port:        8080,
			},
			want: []*elbv2.TargetDescription{
				{
					Id:   aws.String("i-abcdef1"),
					Port: aws.Int64(8080),
				},
			},
		},
		{
			name: "multiple instances",
			args: args{
				instanceIDs: []string{"i-abcdef1", "i-abcdef2", "i-abcdef3"},
				port:        8080,
			},
			want: []*elbv2.TargetDescription{
				{
					Id:   aws.String("i-abcdef1"),
					Port: aws.Int64(8080),
				},
				{
					Id:   aws.String("i-abcdef2"),
					Port: aws.Int64(8080),
				},
				{
					Id:   aws.String("i-abcdef3"),
					Port: aws.Int64(8080),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cloud{}
			got := c.computeTargetGroupExpectedTargets(tt.args.instanceIDs, tt.args.port)
			assert.Equal(t, tt.want, got)
		})
	}
}
