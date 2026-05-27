/*
Copyright 2024 The K8sGPT Authors.
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

package kubernetes

import (
	"fmt"
	"sync"
)

const (
	// DefaultClusterID is the identifier used for the default cluster
	// (the one configured via --kubeconfig / --kubecontext flags).
	DefaultClusterID = "default"
)

// ClusterInfo holds the registration metadata and live client for a cluster.
type ClusterInfo struct {
	// ID is the unique identifier for this cluster within k8sgpt.
	ID string
	// Kubeconfig is the path to the kubeconfig file. May be empty for in-cluster.
	Kubeconfig string
	// Kubecontext is the kubeconfig context name to use. May be empty.
	Kubecontext string
	// Client is the live Kubernetes client for this cluster.
	Client *Client
}

// ClusterManager maintains a thread-safe registry of Kubernetes cluster clients.
type ClusterManager struct {
	mu       sync.RWMutex
	clusters map[string]*ClusterInfo
}

var (
	globalManager     *ClusterManager
	globalManagerOnce sync.Once
)

// DefaultManager returns the process-wide singleton ClusterManager.
func DefaultManager() *ClusterManager {
	globalManagerOnce.Do(func() {
		globalManager = &ClusterManager{
			clusters: make(map[string]*ClusterInfo),
		}
	})
	return globalManager
}

// RegisterCluster creates a Kubernetes client for the given kubeconfig/context
// and stores it under the provided id. If a cluster with the same id already
// exists it is replaced. Returns the populated ClusterInfo on success.
func (m *ClusterManager) RegisterCluster(id, kubeconfig, kubecontext string) (*ClusterInfo, error) {
	client, err := NewClient(kubecontext, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("registering cluster %q: %w", id, err)
	}

	info := &ClusterInfo{
		ID:          id,
		Kubeconfig:  kubeconfig,
		Kubecontext: kubecontext,
		Client:      client,
	}

	m.mu.Lock()
	m.clusters[id] = info
	m.mu.Unlock()

	return info, nil
}

// UnregisterCluster removes the cluster with the given id from the registry.
// It is a no-op if the cluster is not registered.
func (m *ClusterManager) UnregisterCluster(id string) {
	m.mu.Lock()
	delete(m.clusters, id)
	m.mu.Unlock()
}

// GetCluster returns the ClusterInfo for the given id.
// Returns an error if the cluster is not registered.
func (m *ClusterManager) GetCluster(id string) (*ClusterInfo, error) {
	m.mu.RLock()
	info, ok := m.clusters[id]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("cluster %q is not registered; use list-clusters to see available clusters", id)
	}
	return info, nil
}

// GetClientOrDefault returns the Kubernetes client for the given cluster id.
// If id is empty or equals DefaultClusterID, it returns the default cluster client.
// Returns an error if the requested cluster is not registered.
func (m *ClusterManager) GetClientOrDefault(id string) (*Client, error) {
	if id == "" {
		id = DefaultClusterID
	}
	info, err := m.GetCluster(id)
	if err != nil {
		return nil, err
	}
	return info.Client, nil
}

// ListClusters returns a snapshot of all registered clusters (read-only view).
func (m *ClusterManager) ListClusters() []*ClusterInfo {
	m.mu.RLock()
	result := make([]*ClusterInfo, 0, len(m.clusters))
	for _, info := range m.clusters {
		result = append(result, info)
	}
	m.mu.RUnlock()
	return result
}

// HasCluster reports whether a cluster with the given id is registered.
func (m *ClusterManager) HasCluster(id string) bool {
	m.mu.RLock()
	_, ok := m.clusters[id]
	m.mu.RUnlock()
	return ok
}
