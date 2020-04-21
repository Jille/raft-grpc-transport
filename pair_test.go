package raftgrpc

import (
	"bytes"
	context "context"
	"io"
	"log"
	"net"
	"reflect"
	"testing"

	"github.com/hashicorp/raft"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

// TestRaftGRPCTransportImplementsRaftTransport checks if the transport implements the raft interface correctly
func TestRaftGRPCTransportImplementsRaftTransport(t *testing.T) {
	var typeAssertion raft.Transport = &RaftGRPCTransport{}
	_ = typeAssertion
}

// TestRaftGRPCTransportImplementsService checks if the transport implements the server interface correctly
func TestRaftGRPCTransportImplementsService(t *testing.T) {
	var typeAssertion RaftServiceServer = (&RaftGRPCTransport{}).GetServerService()
	_ = typeAssertion
}

func makeTestPair(ctx context.Context, t *testing.T) (*RaftGRPCTransport, *RaftGRPCTransport) {
	t.Helper()
	t1 := NewTransport(ctx, "t1")
	t2 := NewTransport(ctx, "t2")

	t1Listen := bufconn.Listen(1024)
	s1 := grpc.NewServer()
	RegisterRaftServiceServer(s1, t1.GetServerService())
	go func() {
		if err := s1.Serve(t1Listen); err != nil {
			log.Fatalf("t1 exited with error: %v", err)
		}
	}()

	t2Listen := bufconn.Listen(1024)
	s2 := grpc.NewServer()
	RegisterRaftServiceServer(s2, t2.GetServerService())
	go func() {
		if err := s2.Serve(t2Listen); err != nil {
			log.Fatalf("t2 exited with error: %v", err)
		}
	}()

	conn1, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return t1Listen.Dial()
	}), grpc.WithInsecure())
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}
	t2.AddPeer("t1", NewRaftServiceClient(conn1))

	conn2, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return t2Listen.Dial()
	}), grpc.WithInsecure())
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}
	t1.AddPeer("t2", NewRaftServiceClient(conn2))

	go func() {
		<-ctx.Done()
		conn1.Close()
		conn2.Close()
	}()

	return t1, t2
}

func TestAppendEntries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t1, t2 := makeTestPair(ctx, t)

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case rpc := <-t2.Consumer():
				if got, want := rpc.Command.(*raft.AppendEntriesRequest).Leader, []byte{3, 2, 1}; !bytes.Equal(got, want) {
					t.Errorf("request.Leader = %v, want %v", got, want)
				}
				if got, want := rpc.Command.(*raft.AppendEntriesRequest).Entries, []*raft.Log{
					{Type: raft.LogNoop, Extensions: []byte{1}, Data: []byte{55}},
				}; !reflect.DeepEqual(got, want) {
					t.Errorf("request.Entries = %v, want %v", got, want)
				}
				rpc.Respond(&raft.AppendEntriesResponse{
					Success: true,
					LastLog: 12396,
				}, nil)
			}
		}
	}()

	var resp raft.AppendEntriesResponse
	if err := t1.AppendEntries("t2", "t2", &raft.AppendEntriesRequest{
		Leader: []byte{3, 2, 1},
		Entries: []*raft.Log{
			{Type: raft.LogNoop, Extensions: []byte{1}, Data: []byte{55}},
		},
	}, &resp); err != nil {
		t.Errorf("AppendEntries() failed: %v", err)
	}
	if got, want := resp.LastLog, uint64(12396); got != want {
		t.Errorf("resp.LastLog = %v, want %v", got, want)
	}

	close(stop)
}

func TestSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t1, t2 := makeTestPair(ctx, t)

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case rpc := <-t2.Consumer():
				if got, want := rpc.Command.(*raft.InstallSnapshotRequest), (&raft.InstallSnapshotRequest{
					Term:               123,
					Leader:             []byte{2},
					Configuration:      []byte{4, 2, 3},
					ConfigurationIndex: 3,
					Size:               654321,
					Peers:              []byte{8},
				}); !reflect.DeepEqual(got, want) {
					t.Errorf("request = %+v, want %+v", got, want)
				}

				var i int
				for {
					var buf [431]byte
					n, err := rpc.Reader.Read(buf[:])
					if err != nil {
						if err == io.EOF {
							break
						}
						t.Errorf("Read() returned: %v", err)
					}
					i += n
					if !bytes.Equal(buf[:n], bytes.Repeat([]byte{89}, n)) {
						t.Errorf("Bad data: got %v, want %v", buf[:n], bytes.Repeat([]byte{89}, n))
					}
				}
				if got, want := int64(i), rpc.Command.(*raft.InstallSnapshotRequest).Size; got != want {
					t.Errorf("read %d bytes, want %d", got, want)
				}

				rpc.Respond(&raft.InstallSnapshotResponse{
					Success: true,
				}, nil)
			}
		}
	}()

	var resp raft.InstallSnapshotResponse
	b := bytes.Repeat([]byte{89}, 654321)
	if err := t1.InstallSnapshot("t2", "t2", &raft.InstallSnapshotRequest{
		Term:               123,
		Leader:             []byte{2},
		Configuration:      []byte{4, 2, 3},
		ConfigurationIndex: 3,
		Size:               int64(len(b)),
		Peers:              []byte{8},
	}, &resp, bytes.NewReader(b)); err != nil {
		t.Errorf("InstallSnapshot() failed: %v", err)
	}
	if got, want := resp.Success, true; got != want {
		t.Errorf("resp.Success = %v, want %v", got, want)
	}

	close(stop)
}
