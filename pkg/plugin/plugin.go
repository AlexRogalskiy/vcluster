package plugin

import (
	context "context"
	"encoding/json"
	"fmt"
	"go.uber.org/atomic"
	"k8s.io/klog"
	"net"

	remote "github.com/loft-sh/vcluster/pkg/plugin/remote"
	grpc "google.golang.org/grpc"
	rest "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	synccontext "github.com/loft-sh/vcluster/pkg/controllers/syncer/context"
	"github.com/pkg/errors"
)

var DefaultManager Manager = &manager{}

type Manager interface {
	Start(context *synccontext.RegisterContext, syncerConfig *clientcmdapi.Config) error
	SetLeader(isLeader bool)
}

type manager struct {
	remote.UnimplementedPluginInitializerServer

	physicalKubeConfig string
	virtualKubeConfig  string
	syncerKubeConfig   string

	targetNamespace  string
	currentNamespace string

	options string

	isLeader atomic.Bool
}

func (m *manager) SetLeader(isLeader bool) {
	m.isLeader.Store(isLeader)
}

func (m *manager) Start(ctx *synccontext.RegisterContext, syncerConfig *clientcmdapi.Config) error {
	// base options
	m.currentNamespace = ctx.CurrentNamespace
	m.targetNamespace = ctx.TargetNamespace

	// Context options
	out, err := json.Marshal(ctx.Options)
	if err != nil {
		return errors.Wrap(err, "marshal options")
	}
	m.options = string(out)

	// Virtual client config
	rawVirtualConfig, err := ConvertRestConfigToClientConfig(ctx.VirtualManager.GetConfig()).RawConfig()
	if err != nil {
		return errors.Wrap(err, "convert virtual client config")
	}
	virtualConfigBytes, err := clientcmd.Write(rawVirtualConfig)
	if err != nil {
		return errors.Wrap(err, "marshal virtual client config")
	}
	m.virtualKubeConfig = string(virtualConfigBytes)

	// Physical client config
	rawPhysicalConfig, err := ConvertRestConfigToClientConfig(ctx.PhysicalManager.GetConfig()).RawConfig()
	if err != nil {
		return errors.Wrap(err, "convert physical client config")
	}
	phyisicalConfigBytes, err := clientcmd.Write(rawPhysicalConfig)
	if err != nil {
		return errors.Wrap(err, "marshal physical client config")
	}
	m.physicalKubeConfig = string(phyisicalConfigBytes)

	// Syncer client config
	syncerConfigBytes, err := clientcmd.Write(*syncerConfig)
	if err != nil {
		return errors.Wrap(err, "marshal syncer client config")
	}
	m.syncerKubeConfig = string(syncerConfigBytes)

	// start the grpc server
	lis, err := net.Listen("tcp", ctx.Options.PluginListenAddress)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	var opts []grpc.ServerOption
	grpcServer := grpc.NewServer(opts...)
	remote.RegisterPluginInitializerServer(grpcServer, m)
	return grpcServer.Serve(lis)
}

func (m *manager) IsLeader(ctx context.Context, empty *remote.Empty) (*remote.LeaderInfo, error) {
	return &remote.LeaderInfo{
		Leader: m.isLeader.Load(),
	}, nil
}

func (m *manager) Register(ctx context.Context, info *remote.PluginInfo) (*remote.Context, error) {
	if info != nil && info.Name != "" {
		klog.Infof("Registering plugin %s", info.Name)
	}
	return &remote.Context{
		VirtualClusterConfig:  m.virtualKubeConfig,
		PhysicalClusterConfig: m.physicalKubeConfig,
		SyncerConfig:          m.syncerKubeConfig,
		TargetNamespace:       m.targetNamespace,
		CurrentNamespace:      m.currentNamespace,
		Options:               m.options,
	}, nil
}

func ConvertRestConfigToClientConfig(config *rest.Config) clientcmd.ClientConfig {
	contextName := "local"
	kubeConfig := clientcmdapi.NewConfig()
	kubeConfig.Contexts = map[string]*clientcmdapi.Context{
		contextName: {
			Cluster:  contextName,
			AuthInfo: contextName,
		},
	}
	kubeConfig.Clusters = map[string]*clientcmdapi.Cluster{
		contextName: {
			Server:                   config.Host,
			InsecureSkipTLSVerify:    config.Insecure,
			CertificateAuthorityData: config.CAData,
			CertificateAuthority:     config.CAFile,
		},
	}
	kubeConfig.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		contextName: {
			Token:                 config.BearerToken,
			TokenFile:             config.BearerTokenFile,
			Impersonate:           config.Impersonate.UserName,
			ImpersonateGroups:     config.Impersonate.Groups,
			ImpersonateUserExtra:  config.Impersonate.Extra,
			ClientCertificate:     config.CertFile,
			ClientCertificateData: config.CertData,
			ClientKey:             config.KeyFile,
			ClientKeyData:         config.KeyData,
			Username:              config.Username,
			Password:              config.Password,
			AuthProvider:          config.AuthProvider,
			Exec:                  config.ExecProvider,
		},
	}
	kubeConfig.CurrentContext = contextName
	return clientcmd.NewDefaultClientConfig(*kubeConfig, &clientcmd.ConfigOverrides{})
}
