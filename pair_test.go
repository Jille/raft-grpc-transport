package transport_test

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"reflect"
	"testing"

	transport "github.com/Jille/raft-grpc-transport"
	"github.com/hashicorp/raft"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

func makeTestPair(ctx context.Context, t *testing.T) (raft.Transport, raft.Transport) {
	t.Helper()
	t1Listen := bufconn.Listen(1024)
	t2Listen := bufconn.Listen(1024)

	t1 := transport.New(raft.ServerAddress("t1"), []grpc.DialOption{grpc.WithInsecure(), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return t2Listen.Dial()
	})})
	t2 := transport.New(raft.ServerAddress("t2"), []grpc.DialOption{grpc.WithInsecure(), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return t1Listen.Dial()
	})})

	s1 := grpc.NewServer()
	t1.Register(s1)
	go func() {
		if err := s1.Serve(t1Listen); err != nil {
			log.Fatalf("t1 exited with error: %v", err)
		}
	}()

	s2 := grpc.NewServer()
	t2.Register(s2)
	go func() {
		if err := s2.Serve(t2Listen); err != nil {
			log.Fatalf("t2 exited with error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		// TODO(quis): Add a Close() method to the Manager.
		// t1.Close()
		// t2.Close()
	}()

	return t1.Transport(), t2.Transport()
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
