// this might need to live somewhere else. This code was written by Miguel Duarte Barraso.

package networking

import (
	"context"

	k8snetworkplumbingwgv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type netAttachDefClient struct {
	k8sClient client.Client
}

func NewNetAttachDefClient(kubeconfigPath string) (*netAttachDefClient, error) {
	scheme := runtime.NewScheme()
	_ = k8snetworkplumbingwgv1.AddToScheme(scheme)

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}

	mapper, err := apiutil.NewDiscoveryRESTMapper(config)
	if err != nil {
		return nil, err
	}
	c, err := client.New(config, client.Options{Scheme: scheme, Mapper: mapper})
	if err != nil {
		return nil, err
	}

	return &netAttachDefClient{k8sClient: c}, nil
}

func (nadc netAttachDefClient) create(nad *k8snetworkplumbingwgv1.NetworkAttachmentDefinition) error {
	return nadc.k8sClient.Create(context.TODO(), nad)
}
