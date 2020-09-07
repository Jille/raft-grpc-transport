package transport

import (
	"sync"

	pb "github.com/Jille/raft-grpc-transport/proto"
	"github.com/hashicorp/raft"
	"google.golang.org/grpc"
)

type Manager struct {
	localAddress raft.ServerAddress
	dialOptions  []grpc.DialOption

	rpcChan chan raft.RPC

	connectionsMtx sync.Mutex
	connections    map[raft.ServerID]*conn
}

func New(localAddress raft.ServerAddress, dialOptions []grpc.DialOption) *Manager {
	return &Manager{
		localAddress: localAddress,
		dialOptions:  dialOptions,

		rpcChan:     make(chan raft.RPC),
		connections: map[raft.ServerID]*conn{},
	}
}

func (m *Manager) Register(s *grpc.Server) {
	pb.RegisterRaftTransportServer(s, gRPCAPI{m})
}

func (m *Manager) Transport() raft.Transport {
	return raftAPI{m}
}
