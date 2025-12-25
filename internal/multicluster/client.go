// Package multicluster provides multi-cluster Kubernetes client management
package multicluster

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClientManager manages Kubernetes clients for multiple clusters
type ClientManager struct {
	// scheme is the runtime scheme for creating typed clients
	scheme *runtime.Scheme

	// localClient is the client for the local/management cluster
	localClient client.Client

	// clientCache caches remote cluster clients
	clientCache map[string]*ClusterClient
	cacheMu     sync.RWMutex
}

// ClusterClient contains clients for a single cluster
type ClusterClient struct {
	// Client is the controller-runtime client
	Client client.Client

	// Clientset is the typed Kubernetes clientset
	Clientset kubernetes.Interface

	// RestConfig is the REST config for this cluster
	RestConfig *rest.Config
}

// NewClientManager creates a new multi-cluster client manager
func NewClientManager(scheme *runtime.Scheme, localClient client.Client) *ClientManager {
	return &ClientManager{
		scheme:      scheme,
		localClient: localClient,
		clientCache: make(map[string]*ClusterClient),
	}
}

// GetLocalClient returns the local/management cluster client
func (m *ClientManager) GetLocalClient() client.Client {
	return m.localClient
}

// GetClientFromSecret retrieves or creates a client for a cluster using kubeconfig from a Secret
func (m *ClientManager) GetClientFromSecret(ctx context.Context, secretNamespace, secretName, secretKey string) (*ClusterClient, error) {
	cacheKey := fmt.Sprintf("%s/%s/%s", secretNamespace, secretName, secretKey)

	// Check cache first
	m.cacheMu.RLock()
	if cc, ok := m.clientCache[cacheKey]; ok {
		m.cacheMu.RUnlock()
		return cc, nil
	}
	m.cacheMu.RUnlock()

	// Fetch the secret containing the kubeconfig
	secret := &corev1.Secret{}
	if err := m.localClient.Get(ctx, client.ObjectKey{
		Namespace: secretNamespace,
		Name:      secretName,
	}, secret); err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret %s/%s: %w", secretNamespace, secretName, err)
	}

	// Get the kubeconfig data from the secret
	kubeconfigData, ok := secret.Data[secretKey]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s does not contain key %q", secretNamespace, secretName, secretKey)
	}

	// Create client from kubeconfig
	cc, err := m.createClientFromKubeconfig(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to create client from kubeconfig: %w", err)
	}

	// Cache the client
	m.cacheMu.Lock()
	m.clientCache[cacheKey] = cc
	m.cacheMu.Unlock()

	return cc, nil
}

// GetClientFromKubeconfig creates a client directly from kubeconfig bytes
func (m *ClientManager) GetClientFromKubeconfig(kubeconfig []byte) (*ClusterClient, error) {
	return m.createClientFromKubeconfig(kubeconfig)
}

// createClientFromKubeconfig creates a ClusterClient from kubeconfig bytes
func (m *ClientManager) createClientFromKubeconfig(kubeconfig []byte) (*ClusterClient, error) {
	// Parse the kubeconfig
	clientConfig, err := clientcmd.NewClientConfigFromBytes(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	// Get the REST config
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config: %w", err)
	}

	// Create the controller-runtime client
	c, err := client.New(restConfig, client.Options{
		Scheme: m.scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Create the typed clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return &ClusterClient{
		Client:     c,
		Clientset:  clientset,
		RestConfig: restConfig,
	}, nil
}

// GetClientFromRestConfig creates a client from a REST config
func (m *ClientManager) GetClientFromRestConfig(restConfig *rest.Config) (*ClusterClient, error) {
	// Create the controller-runtime client
	c, err := client.New(restConfig, client.Options{
		Scheme: m.scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Create the typed clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return &ClusterClient{
		Client:     c,
		Clientset:  clientset,
		RestConfig: restConfig,
	}, nil
}

// InvalidateCache removes a cached client
func (m *ClientManager) InvalidateCache(secretNamespace, secretName, secretKey string) {
	cacheKey := fmt.Sprintf("%s/%s/%s", secretNamespace, secretName, secretKey)
	m.cacheMu.Lock()
	delete(m.clientCache, cacheKey)
	m.cacheMu.Unlock()
}

// ClearCache removes all cached clients
func (m *ClientManager) ClearCache() {
	m.cacheMu.Lock()
	m.clientCache = make(map[string]*ClusterClient)
	m.cacheMu.Unlock()
}

// TestConnection tests connectivity to a cluster
func (m *ClientManager) TestConnection(ctx context.Context, cc *ClusterClient) error {
	// Try to get server version as a connectivity test
	_, err := cc.Clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}
	return nil
}

// ContextRef represents a reference to a cluster context
type ContextRef struct {
	// SecretNamespace is the namespace of the kubeconfig secret
	SecretNamespace string

	// SecretName is the name of the kubeconfig secret
	SecretName string

	// SecretKey is the key in the secret containing the kubeconfig (default: "kubeconfig")
	SecretKey string
}

// GetClient is a convenience method to get a client from a ContextRef
func (m *ClientManager) GetClient(ctx context.Context, ref ContextRef) (*ClusterClient, error) {
	secretKey := ref.SecretKey
	if secretKey == "" {
		secretKey = "kubeconfig"
	}
	return m.GetClientFromSecret(ctx, ref.SecretNamespace, ref.SecretName, secretKey)
}

// BuildScheme builds a runtime scheme with all necessary types
func BuildScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	// Add core types
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	return scheme, nil
}
