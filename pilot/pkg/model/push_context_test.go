// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package model

import (
	"fmt"
	"reflect"
	"regexp"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	meshconfig "istio.io/api/mesh/v1alpha1"
	networking "istio.io/api/networking/v1alpha3"
	securityBeta "istio.io/api/security/v1beta1"
	selectorpb "istio.io/api/type/v1beta1"

	"istio.io/istio/pkg/config/host"
	"istio.io/istio/pkg/config/labels"
	"istio.io/istio/pkg/config/mesh"
	"istio.io/istio/pkg/config/schema/collections"
	"istio.io/istio/pkg/config/schema/resource"
	"istio.io/istio/pkg/config/visibility"
)

func TestMergeUpdateRequest(t *testing.T) {
	push0 := &PushContext{}
	// trivially different push contexts just for testing
	push1 := &PushContext{ProxyStatus: make(map[string]map[string]ProxyPushStatus)}

	var t0 time.Time
	t1 := t0.Add(time.Minute)

	cases := []struct {
		name   string
		left   *PushRequest
		right  *PushRequest
		merged PushRequest
	}{
		{
			"left nil",
			nil,
			&PushRequest{Full: true},
			PushRequest{Full: true},
		},
		{
			"right nil",
			&PushRequest{Full: true},
			nil,
			PushRequest{Full: true},
		},
		{
			"simple merge",
			&PushRequest{
				Full:  true,
				Push:  push0,
				Start: t0,
				ConfigsUpdated: map[ConfigKey]struct{}{
					{Kind: resource.GroupVersionKind{Kind: "cfg1"}, Namespace: "ns1"}: {}},
				Reason: []TriggerReason{ServiceUpdate, ServiceUpdate},
			},
			&PushRequest{
				Full:  false,
				Push:  push1,
				Start: t1,
				ConfigsUpdated: map[ConfigKey]struct{}{
					{Kind: resource.GroupVersionKind{Kind: "cfg2"}, Namespace: "ns2"}: {}},
				Reason: []TriggerReason{EndpointUpdate},
			},
			PushRequest{
				Full:  true,
				Push:  push1,
				Start: t0,
				ConfigsUpdated: map[ConfigKey]struct{}{
					{Kind: resource.GroupVersionKind{Kind: "cfg1"}, Namespace: "ns1"}: {},
					{Kind: resource.GroupVersionKind{Kind: "cfg2"}, Namespace: "ns2"}: {}},
				Reason: []TriggerReason{ServiceUpdate, ServiceUpdate, EndpointUpdate},
			},
		},
		{
			"skip config type merge: one empty",
			&PushRequest{Full: true, ConfigsUpdated: nil},
			&PushRequest{Full: true, ConfigsUpdated: map[ConfigKey]struct{}{{
				Kind: resource.GroupVersionKind{Kind: "cfg2"}}: {}}},
			PushRequest{Full: true, ConfigsUpdated: nil},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.left.Merge(tt.right)
			if !reflect.DeepEqual(&tt.merged, got) {
				t.Fatalf("expected %v, got %v", tt.merged, got)
			}
		})
	}
}

func TestEnvoyFilters(t *testing.T) {
	proxyVersionRegex := regexp.MustCompile(`1\.4.*`)
	envoyFilters := []*EnvoyFilterWrapper{
		{
			workloadSelector: map[string]string{"app": "v1"},
			Patches: map[networking.EnvoyFilter_ApplyTo][]*EnvoyFilterConfigPatchWrapper{
				networking.EnvoyFilter_LISTENER: {
					{
						Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
							Proxy: &networking.EnvoyFilter_ProxyMatch{
								ProxyVersion: "1\\.4.*",
							},
						},
						ProxyVersionRegex: proxyVersionRegex,
					},
				},
			},
		},
		{
			workloadSelector: map[string]string{"app": "v1"},
			Patches: map[networking.EnvoyFilter_ApplyTo][]*EnvoyFilterConfigPatchWrapper{
				networking.EnvoyFilter_CLUSTER: {
					{
						Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
							Proxy: &networking.EnvoyFilter_ProxyMatch{
								ProxyVersion: `1\\.4.*`,
							},
						},
						ProxyVersionRegex: proxyVersionRegex,
					},
				},
			},
		},
	}

	push := &PushContext{
		Mesh: &meshconfig.MeshConfig{
			RootNamespace: "istio-system",
		},
		envoyFiltersByNamespace: map[string][]*EnvoyFilterWrapper{
			"istio-system": envoyFilters,
			"test-ns":      envoyFilters,
		},
	}

	cases := []struct {
		name                    string
		proxy                   *Proxy
		expectedListenerPatches int
		expectedClusterPatches  int
	}{
		{
			name: "proxy matches two envoyfilters",
			proxy: &Proxy{
				Metadata:        &NodeMetadata{IstioVersion: "1.4.0", Labels: map[string]string{"app": "v1"}},
				ConfigNamespace: "test-ns",
			},
			expectedListenerPatches: 2,
			expectedClusterPatches:  2,
		},
		{
			name: "proxy in root namespace matches an envoyfilter",
			proxy: &Proxy{
				Metadata:        &NodeMetadata{IstioVersion: "1.4.0", Labels: map[string]string{"app": "v1"}},
				ConfigNamespace: "istio-system",
			},
			expectedListenerPatches: 1,
			expectedClusterPatches:  1,
		},

		{
			name: "proxy matches no envoyfilter",
			proxy: &Proxy{
				Metadata:        &NodeMetadata{IstioVersion: "1.4.0", Labels: map[string]string{"app": "v2"}},
				ConfigNamespace: "test-ns",
			},
			expectedListenerPatches: 0,
			expectedClusterPatches:  0,
		},

		{
			name: "proxy matches envoyfilter in root ns",
			proxy: &Proxy{
				Metadata:        &NodeMetadata{IstioVersion: "1.4.0", Labels: map[string]string{"app": "v1"}},
				ConfigNamespace: "test-n2",
			},
			expectedListenerPatches: 1,
			expectedClusterPatches:  1,
		},
		{
			name: "proxy version matches no envoyfilters",
			proxy: &Proxy{
				Metadata:        &NodeMetadata{IstioVersion: "1.3.0", Labels: map[string]string{"app": "v1"}},
				ConfigNamespace: "test-ns",
			},
			expectedListenerPatches: 0,
			expectedClusterPatches:  0,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			filter := push.EnvoyFilters(tt.proxy)
			if filter == nil {
				if tt.expectedClusterPatches != 0 || tt.expectedListenerPatches != 0 {
					t.Errorf("Got no envoy filter")
				}
				return
			}
			if len(filter.Patches[networking.EnvoyFilter_CLUSTER]) != tt.expectedClusterPatches {
				t.Errorf("Expect %d envoy filter cluster patches, but got %d", tt.expectedClusterPatches, len(filter.Patches[networking.EnvoyFilter_CLUSTER]))
			}
			if len(filter.Patches[networking.EnvoyFilter_LISTENER]) != tt.expectedListenerPatches {
				t.Errorf("Expect %d envoy filter listener patches, but got %d", tt.expectedListenerPatches, len(filter.Patches[networking.EnvoyFilter_LISTENER]))
			}
		})
	}
}

func TestSidecarScope(t *testing.T) {
	ps := NewPushContext()
	env := &Environment{Watcher: mesh.NewFixedWatcher(&meshconfig.MeshConfig{RootNamespace: "istio-system"})}
	ps.Mesh = env.Mesh()
	ps.ServiceDiscovery = env
	ps.ServiceByHostnameAndNamespace[host.Name("svc1.default.cluster.local")] = map[string]*Service{"default": nil}
	ps.ServiceByHostnameAndNamespace[host.Name("svc2.nosidecar.cluster.local")] = map[string]*Service{"nosidecar": nil}

	configStore := NewFakeStore()
	sidecarWithWorkloadSelector := &networking.Sidecar{
		WorkloadSelector: &networking.WorkloadSelector{
			Labels: map[string]string{"app": "foo"},
		},
		Egress: []*networking.IstioEgressListener{
			{
				Hosts: []string{"default/*"},
			},
		},
		OutboundTrafficPolicy: &networking.OutboundTrafficPolicy{},
	}
	sidecarWithoutWorkloadSelector := &networking.Sidecar{
		Egress: []*networking.IstioEgressListener{
			{
				Hosts: []string{"default/*"},
			},
		},
		OutboundTrafficPolicy: &networking.OutboundTrafficPolicy{},
	}
	configWithWorkloadSelector := Config{
		ConfigMeta: ConfigMeta{
			Type:      collections.IstioNetworkingV1Alpha3Sidecars.Resource().Kind(),
			Group:     collections.IstioNetworkingV1Alpha3Sidecars.Resource().Group(),
			Version:   collections.IstioNetworkingV1Alpha3Sidecars.Resource().Version(),
			Name:      "foo",
			Namespace: "default",
		},
		Spec: sidecarWithWorkloadSelector,
	}
	rootConfig := Config{
		ConfigMeta: ConfigMeta{
			Type:      collections.IstioNetworkingV1Alpha3Sidecars.Resource().Kind(),
			Group:     collections.IstioNetworkingV1Alpha3Sidecars.Resource().Group(),
			Version:   collections.IstioNetworkingV1Alpha3Sidecars.Resource().Version(),
			Name:      "global",
			Namespace: "istio-system",
		},
		Spec: sidecarWithoutWorkloadSelector,
	}
	_, _ = configStore.Create(configWithWorkloadSelector)
	_, _ = configStore.Create(rootConfig)

	store := istioConfigStore{ConfigStore: configStore}

	env.IstioConfigStore = &store
	if err := ps.initSidecarScopes(env); err != nil {
		t.Fatalf("init sidecar scope failed: %v", err)
	}
	cases := []struct {
		proxy      *Proxy
		collection labels.Collection
		sidecar    string
		describe   string
	}{
		{
			proxy:      &Proxy{ConfigNamespace: "default"},
			collection: labels.Collection{map[string]string{"app": "foo"}},
			sidecar:    "default/foo",
			describe:   "match local sidecar",
		},
		{
			proxy:      &Proxy{ConfigNamespace: "default"},
			collection: labels.Collection{map[string]string{"app": "bar"}},
			sidecar:    "istio-system/global",
			describe:   "no match local sidecar",
		},
		{
			proxy:      &Proxy{ConfigNamespace: "nosidecar"},
			collection: labels.Collection{map[string]string{"app": "bar"}},
			sidecar:    "istio-system/global",
			describe:   "no sidecar",
		},
	}
	for _, c := range cases {
		scope := ps.getSidecarScope(c.proxy, c.collection)
		if c.sidecar != scopeToSidecar(scope) {
			t.Errorf("case with %s should get sidecar %s but got %s", c.describe, c.sidecar, scopeToSidecar(scope))
		}
	}
}

func TestBestEffortInferServiceMTLSMode(t *testing.T) {
	const partialNS string = "partial"
	const wholeNS string = "whole"
	ps := NewPushContext()
	env := &Environment{Watcher: mesh.NewFixedWatcher(&meshconfig.MeshConfig{RootNamespace: "istio-system"})}
	ps.Mesh = env.Mesh()
	ps.ServiceDiscovery = env

	configStore := NewFakeStore()

	// Add beta policies
	configStore.Create(*createTestPeerAuthenticationResource("default", wholeNS, time.Now(), nil, securityBeta.PeerAuthentication_MutualTLS_STRICT))
	// workload level beta policy.
	configStore.Create(*createTestPeerAuthenticationResource("workload-beta-policy", partialNS, time.Now(), &selectorpb.WorkloadSelector{
		MatchLabels: map[string]string{
			"app":     "httpbin",
			"version": "v1",
		},
	}, securityBeta.PeerAuthentication_MutualTLS_DISABLE))

	store := istioConfigStore{ConfigStore: configStore}
	env.IstioConfigStore = &store
	if err := ps.initAuthnPolicies(env); err != nil {
		t.Fatalf("init authn policies failed: %v", err)
	}

	cases := []struct {
		name             string
		serviceNamespace string
		servicePort      int
		wanted           MutualTLSMode
	}{
		{
			name:             "from namespace policy",
			serviceNamespace: wholeNS,
			servicePort:      80,
			wanted:           MTLSStrict,
		},
		{
			name:             "from mesh default",
			serviceNamespace: partialNS,
			servicePort:      80,
			wanted:           MTLSPermissive,
		},
	}
	serviceName := host.Name("some-service")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &Service{
				Hostname:   host.Name(fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, tc.serviceNamespace)),
				Attributes: ServiceAttributes{Namespace: tc.serviceNamespace},
			}
			// Intentionally use the externalService with the same name and namespace for test, though
			// these attributes don't matter.
			externalService := &Service{
				Hostname:     host.Name(fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, tc.serviceNamespace)),
				Attributes:   ServiceAttributes{Namespace: tc.serviceNamespace},
				MeshExternal: true,
			}

			port := &Port{
				Port: tc.servicePort,
			}
			if got := ps.BestEffortInferServiceMTLSMode(service, port); got != tc.wanted {
				t.Fatalf("want %s, but got %s", tc.wanted, got)
			}
			if got := ps.BestEffortInferServiceMTLSMode(externalService, port); got != MTLSUnknown {
				t.Fatalf("MTLS mode for external service should always be %s, but got %s", MTLSUnknown, got)
			}
		})
	}
}

func scopeToSidecar(scope *SidecarScope) string {
	if scope == nil || scope.Config == nil {
		return ""
	}
	return scope.Config.Namespace + "/" + scope.Config.Name
}

func TestSetDestinationRule(t *testing.T) {
	ps := NewPushContext()
	ps.defaultDestinationRuleExportTo = map[visibility.Instance]bool{visibility.Public: true}
	testhost := "httpbin.org"
	destinationRuleNamespace1 := Config{
		ConfigMeta: ConfigMeta{
			Name:      "rule1",
			Namespace: "test",
		},
		Spec: &networking.DestinationRule{
			Host: testhost,
			Subsets: []*networking.Subset{
				{
					Name: "subset1",
				},
				{
					Name: "subset2",
				},
			},
		},
	}
	destinationRuleNamespace2 := Config{
		ConfigMeta: ConfigMeta{
			Name:      "rule2",
			Namespace: "test",
		},
		Spec: &networking.DestinationRule{
			Host: testhost,
			Subsets: []*networking.Subset{
				{
					Name: "subset3",
				},
				{
					Name: "subset4",
				},
			},
		},
	}
	ps.SetDestinationRules([]Config{destinationRuleNamespace1, destinationRuleNamespace2})
	subsetsLocal := ps.namespaceLocalDestRules["test"].destRule[host.Name(testhost)].Spec.(*networking.DestinationRule).Subsets
	subsetsExport := ps.namespaceExportedDestRules["test"].destRule[host.Name(testhost)].Spec.(*networking.DestinationRule).Subsets
	if len(subsetsLocal) != 4 {
		t.Errorf("want %d, but got %d", 4, len(subsetsLocal))
	}

	if len(subsetsExport) != 4 {
		t.Errorf("want %d, but got %d", 4, len(subsetsExport))
	}
}

func TestIsClusterLocal(t *testing.T) {
	cases := []struct {
		name     string
		m        meshconfig.MeshConfig
		host     string
		expected bool
	}{
		{
			name:     "local by default",
			m:        mesh.DefaultMeshConfig(),
			host:     "s.kube-system.svc.cluster.local",
			expected: true,
		},
		{
			name:     "discovery server is local",
			m:        mesh.DefaultMeshConfig(),
			host:     "istiod.istio-system.svc.cluster.local",
			expected: true,
		},
		{
			name:     "not local by default",
			m:        mesh.DefaultMeshConfig(),
			host:     "not.cluster.local",
			expected: false,
		},
		{
			name: "override default",
			m: meshconfig.MeshConfig{
				// Remove the cluster-local setting for kube-system.
				ServiceSettings: []*meshconfig.MeshConfig_ServiceSettings{
					{
						Settings: &meshconfig.MeshConfig_ServiceSettings_Settings{
							ClusterLocal: false,
						},
						Hosts: []string{"*.kube-system.svc.cluster.local"},
					},
				},
			},
			host:     "s.kube-system.svc.cluster.local",
			expected: false,
		},
		{
			name: "local 1",
			m: meshconfig.MeshConfig{
				ServiceSettings: []*meshconfig.MeshConfig_ServiceSettings{
					{
						Settings: &meshconfig.MeshConfig_ServiceSettings_Settings{
							ClusterLocal: true,
						},
						Hosts: []string{
							"*.ns1.svc.cluster.local",
							"*.ns2.svc.cluster.local",
						},
					},
				},
			},
			host:     "s.ns1.svc.cluster.local",
			expected: true,
		},
		{
			name: "local 2",
			m: meshconfig.MeshConfig{
				ServiceSettings: []*meshconfig.MeshConfig_ServiceSettings{
					{
						Settings: &meshconfig.MeshConfig_ServiceSettings_Settings{
							ClusterLocal: true,
						},
						Hosts: []string{
							"*.ns1.svc.cluster.local",
							"*.ns2.svc.cluster.local",
						},
					},
				},
			},
			host:     "s.ns2.svc.cluster.local",
			expected: true,
		},
		{
			name: "not local",
			m: meshconfig.MeshConfig{
				ServiceSettings: []*meshconfig.MeshConfig_ServiceSettings{
					{
						Settings: &meshconfig.MeshConfig_ServiceSettings_Settings{
							ClusterLocal: true,
						},
						Hosts: []string{
							"*.ns1.svc.cluster.local",
							"*.ns2.svc.cluster.local",
						},
					},
				},
			},
			host:     "s.ns3.svc.cluster.local",
			expected: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			env := &Environment{Watcher: mesh.NewFixedWatcher(&c.m)}
			push := &PushContext{
				Mesh: env.Mesh(),
			}
			push.initClusterLocalHosts(env)

			svc := &Service{
				Hostname: host.Name(c.host),
			}
			clusterLocal := push.IsClusterLocal(svc)
			g.Expect(clusterLocal).To(Equal(c.expected))
		})
	}
}
