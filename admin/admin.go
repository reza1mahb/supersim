package admin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/ethereum-optimism/supersim/config"
	"github.com/ethereum-optimism/supersim/interop"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/gin-gonic/gin"
)

type AdminServer struct {
	log log.Logger

	srv    *http.Server
	cancel context.CancelFunc
	wg     sync.WaitGroup

	networkConfig    *config.NetworkConfig
	l2ToL2MsgIndexer *interop.L2ToL2MessageIndexer

	port uint64
}

type RPCMethods struct {
	log              log.Logger
	networkConfig    *config.NetworkConfig
	l2ToL2MsgIndexer *interop.L2ToL2MessageIndexer
}

func NewAdminServer(log log.Logger, port uint64, networkConfig *config.NetworkConfig, indexer *interop.L2ToL2MessageIndexer) *AdminServer {

	adminServer := &AdminServer{log: log, port: port, networkConfig: networkConfig}

	if networkConfig.InteropEnabled && indexer != nil {
		adminServer.l2ToL2MsgIndexer = indexer
	}

	return adminServer
}

func (s *AdminServer) Start(ctx context.Context) error {
	router := s.setupRouter()

	s.srv = &http.Server{Handler: router}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		panic(err)
	}

	addr := listener.Addr().(*net.TCPAddr)

	s.port = uint64(addr.Port)
	s.log.Debug("admin server listening", "port", s.port)

	ctx, s.cancel = context.WithCancel(ctx)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.log.Error("failed to serve", "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		if err := s.srv.Shutdown(context.Background()); err != nil {
			s.log.Error("failed to shutdown", "error", err)
		}
	}()

	return nil
}

func (s *AdminServer) Stop(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}

	s.wg.Wait()
	return nil
}

func (s *AdminServer) Endpoint() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.port)
}

func (s *AdminServer) ConfigAsString() string {
	return fmt.Sprintf("Admin Server: %s\n\n", s.Endpoint())
}

func filterByChainID(chains []config.ChainConfig, chainId uint64) *config.ChainConfig {
	for _, chain := range chains {
		if chain.ChainID == chainId {
			return &chain
		}
	}
	return nil
}

func (s *AdminServer) setupRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	rpcServer := rpc.NewServer()
	rpcMethods := &RPCMethods{
		log:           s.log,
		networkConfig: s.networkConfig,
	}

	if s.networkConfig.InteropEnabled {
		rpcMethods.l2ToL2MsgIndexer = s.l2ToL2MsgIndexer
	}

	if err := rpcServer.RegisterName("admin", rpcMethods); err != nil {
		s.log.Error("failed to register RPC methods:", "error", err)
		return nil
	}

	router.GET("/ready", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	router.POST("/", gin.WrapH(rpcServer))

	return router
}

func (m *RPCMethods) GetConfig(args *struct{}) *config.NetworkConfig {

	m.log.Debug("admin_getConfig")
	return m.networkConfig
}

func (m *RPCMethods) GetL1Addresses(args *uint64) *map[string]string {
	chain := filterByChainID(m.networkConfig.L2Configs, *args)
	if chain == nil {
		m.log.Error("chain not found", "chainID", *args)
		return nil
	}

	reply := map[string]string{
		"AddressManager":                    chain.L2Config.L1Addresses.AddressManager.String(),
		"L1CrossDomainMessengerProxy":       chain.L2Config.L1Addresses.L1CrossDomainMessengerProxy.String(),
		"L1ERC721BridgeProxy":               chain.L2Config.L1Addresses.L1ERC721BridgeProxy.String(),
		"L1StandardBridgeProxy":             chain.L2Config.L1Addresses.L1StandardBridgeProxy.String(),
		"L2OutputOracleProxy":               chain.L2Config.L1Addresses.L2OutputOracleProxy.String(),
		"OptimismMintableERC20FactoryProxy": chain.L2Config.L1Addresses.OptimismMintableERC20FactoryProxy.String(),
		"OptimismPortalProxy":               chain.L2Config.L1Addresses.OptimismPortalProxy.String(),
		"SystemConfigProxy":                 chain.L2Config.L1Addresses.SystemConfigProxy.String(),
		"ProxyAdmin":                        chain.L2Config.L1Addresses.ProxyAdmin.String(),
		"SuperchainConfig":                  chain.L2Config.L1Addresses.SuperchainConfig.String(),
	}
	m.log.Debug("admin_getL2Addresses")
	return &reply
}

func (m *RPCMethods) GetL2ToL2MessageByMsgHash(args *common.Hash) *interop.L2ToL2Message {

	if args == nil {
		m.log.Error("valid msg hash not provided", "args", *args)
		return nil
	}

	if m.networkConfig.InteropEnabled && m.l2ToL2MsgIndexer != nil {

		storeEntry, err := m.l2ToL2MsgIndexer.Get(*args)

		if err != nil {
			m.log.Error("failed to get msg", "error", err)
			return nil
		}

		reply := storeEntry.Message()

		return reply

	} else {
		m.log.Error("auto relay not enabled", "args", *args)
		return nil
	}
}
