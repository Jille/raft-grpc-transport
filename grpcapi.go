package transport

import (
	"context"
	"io"

	"github.com/hashicorp/raft"
	pb "github.com/Jille/raft-grpc-transport/proto"
)

// These are requests incoming over gRPC that we need to relay to the Raft engine.

type gRPCAPI struct {
	manager *Manager
}

func (g gRPCAPI) handleRPC(req *pb.WrappedMessage, typedReq interface{}, data io.Reader) (*pb.WrappedMessage, error) {
	if err := decode(req.GetData(), typedReq); err != nil {
		return nil, err
	}
	ch := make(chan raft.RPCResponse, 1)
	rpc := raft.RPC{
		Command:  typedReq,
		RespChan: ch,
		Reader:   data,
	}
	g.manager.rpcChan <- rpc
	resp := <-ch
	if resp.Error != nil {
		return nil, resp.Error
	}
	b, err := encode(resp.Response)
	if err != nil {
		return nil, err
	}
	return &pb.WrappedMessage{Data: b}, nil
}

func (g gRPCAPI) AppendEntries(ctx context.Context, req *pb.WrappedMessage) (*pb.WrappedMessage, error) {
	return g.handleRPC(req, &raft.AppendEntriesRequest{}, nil)
}

func (g gRPCAPI) RequestVote(ctx context.Context, req *pb.WrappedMessage) (*pb.WrappedMessage, error) {
	return g.handleRPC(req, &raft.RequestVoteRequest{}, nil)
}

func (g gRPCAPI) TimeoutNow(ctx context.Context, req *pb.WrappedMessage) (*pb.WrappedMessage, error) {
	return g.handleRPC(req, &raft.TimeoutNowRequest{}, nil)
}

func (g gRPCAPI) InstallSnapshot(s pb.RaftTransport_InstallSnapshotServer) error {
	isr, err := s.Recv()
	if err != nil {
		return err
	}
	resp, err := g.handleRPC(isr.GetRequest(), &raft.InstallSnapshotRequest{}, &snapshotStream{s, isr.GetData()})
	if err != nil {
		return err
	}
	return s.SendAndClose(resp)
}

type snapshotStream struct {
	s pb.RaftTransport_InstallSnapshotServer

	buf []byte
}

func (s *snapshotStream) Read(b []byte) (int, error) {
	if len(s.buf) > 0 {
		n := copy(b, s.buf)
		s.buf = s.buf[n:]
		return n, nil
	}
	m, err := s.s.Recv()
	if err != nil {
		return 0, err
	}
	n := copy(b, m.GetData())
	if n < len(m.GetData()) {
		s.buf = m.GetData()[n:]
	}
	return n, nil
}

func (g gRPCAPI) AppendEntriesPipeline(s pb.RaftTransport_AppendEntriesPipelineServer) error {
	for {
		msg, err := s.Recv()
		if err != nil {
			return err
		}
		resp, err := g.handleRPC(msg, &raft.AppendEntriesRequest{}, nil)
		if err != nil {
			// TODO(quis): One failure doesn't have to break the entire stream?
			// Or does it all go wrong when it's out of order anyway?
			return err
		}
		if err := s.Send(resp); err != nil {
			return err
		}
	}
}
