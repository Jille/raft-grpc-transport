package transport_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"testing"

	transport "github.com/Jille/raft-grpc-transport"
	"github.com/hashicorp/raft"
	"go.uber.org/goleak"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

func makeTestPair(ctx context.Context, t *testing.T) (raft.Transport, raft.Transport, chan struct{}) {
	t.Helper()
	t1Listen := bufconn.Listen(1024)
	t2Listen := bufconn.Listen(1024)
	shutdownSig := make(chan struct{})

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
		if t1Err := t1.Close(); t1Err != nil {
			t.Fatalf("received error on t1 close: %s", t1Err)
		}
		if t2Err := t2.Close(); t2Err != nil {
			t.Fatalf("received error on t1 close: %s", t2Err)
		}

		s1.GracefulStop()
		s2.GracefulStop()

		close(shutdownSig)
	}()

	return t1.Transport(), t2.Transport(), shutdownSig
}

func testAppendEntries(t *testing.T, usePipeline bool) {
	randBytes := func(n int) []byte {
		buf := make([]byte, n)
		read, err := rand.Read(buf)
		if err != nil {
			t.Fatalf("error generating random byte slice: %s", err.Error())
		}
		if read != n {
			t.Fatalf("error generating random byte slice: read %d bytes, expected %d", read, n)
		}
		return buf
	}

	requests := []struct {
		name string
		raft.AppendEntriesRequest
		// response
		lastLog uint64
	}{
		{
			name: "small message: no chunking",
			AppendEntriesRequest: raft.AppendEntriesRequest{
				Leader: []byte{3, 2, 1},
				Entries: []*raft.Log{
					{Type: raft.LogNoop, Extensions: []byte{1}, Data: []byte{55}},
				},
			},
			lastLog: 12396,
		},
		{
			name: "large message (data only): chunking",
			AppendEntriesRequest: raft.AppendEntriesRequest{
				Leader: []byte{1, 2, 3},
				Entries: []*raft.Log{
					{Type: raft.LogNoop, Extensions: []byte{1}, Data: randBytes(8 * 1024 * 1024)},
				},
			},
			lastLog: 12397,
		},
		{
			name: "large message (extensions and data): chunking",
			AppendEntriesRequest: raft.AppendEntriesRequest{
				Leader: []byte{1, 3, 2},
				Entries: []*raft.Log{
					{Type: raft.LogNoop, Extensions: randBytes(8 * 1024 * 1024), Data: randBytes(8 * 1024 * 1024)},
				},
			},
			lastLog: 12398,
		},
		{
			name: "large message (extensions only): chunking",
			AppendEntriesRequest: raft.AppendEntriesRequest{
				Leader: []byte{3, 1, 2},
				Entries: []*raft.Log{
					{Type: raft.LogNoop, Extensions: randBytes(8 * 1024 * 1024), Data: []byte{55}},
				},
			},
			lastLog: 12399,
		},
		{
			name: "large message (by summing data and extensions): chunking",
			AppendEntriesRequest: raft.AppendEntriesRequest{
				Leader: []byte{2, 1, 3},
				Entries: []*raft.Log{
					{Type: raft.LogNoop, Extensions: randBytes(3 * 1024 * 1024), Data: randBytes(3 * 1024 * 1024)},
				},
			},
			lastLog: 12400,
		},
		{
			name: "many smaller log entries: chunking",
			AppendEntriesRequest: raft.AppendEntriesRequest{
				Leader: []byte{2, 1, 3},
				Entries: []*raft.Log{
					{Type: raft.LogNoop, Extensions: randBytes(256 * 1024), Data: randBytes(256 * 1024)},
					{Type: raft.LogNoop, Extensions: randBytes(256 * 1024), Data: randBytes(256 * 1024)},
					{Type: raft.LogNoop, Extensions: randBytes(256 * 1024), Data: randBytes(256 * 1024)},
					{Type: raft.LogNoop, Extensions: randBytes(256 * 1024), Data: randBytes(256 * 1024)},
					{Type: raft.LogNoop, Extensions: randBytes(256 * 1024), Data: randBytes(256 * 1024)},
					{Type: raft.LogNoop, Extensions: randBytes(256 * 1024), Data: randBytes(256 * 1024)},
					{Type: raft.LogNoop, Extensions: randBytes(256 * 1024), Data: randBytes(256 * 1024)},
					{Type: raft.LogNoop, Extensions: randBytes(256 * 1024), Data: randBytes(256 * 1024)},
				},
			},
			lastLog: 12400,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t1, t2, shutdownSig := makeTestPair(ctx, t)
	defer func() {
		cancel()
		<-shutdownSig
	}()

	stop := make(chan struct{})
	go func() {
		for idx := 0; ; idx++ {
			select {
			case <-stop:
				return
			case rpc := <-t2.Consumer():
				if got, want := rpc.Command.(*raft.AppendEntriesRequest).Leader, requests[idx].AppendEntriesRequest.Leader; !bytes.Equal(got, want) {
					t.Errorf("request.Leader = %v, want %v", got, want)
				}
				if got, want := rpc.Command.(*raft.AppendEntriesRequest).Entries, requests[idx].AppendEntriesRequest.Entries; !reflect.DeepEqual(got, want) {
					t.Errorf("request.Entries = %v, want %v", got, want)
					fmt.Println(len(got[0].Data), len(got[0].Extensions), len(want[0].Data), len(want[0].Extensions))
				}
				rpc.Respond(&raft.AppendEntriesResponse{
					Success: true,
					LastLog: requests[idx].lastLog,
				}, nil)
			}
		}
	}()

	var resp raft.AppendEntriesResponse

	// choose to test the simple or pipelined AppendEntries
	var appendEntries func(*raft.AppendEntriesRequest, *raft.AppendEntriesResponse) error
	if usePipeline {
		p, err := t1.AppendEntriesPipeline("t2", "t2")
		if err != nil {
			t.Fatalf("error opening AppendEntries pipeline: %s", err.Error())
		}
		defer p.Close()
		appendEntries = func(req *raft.AppendEntriesRequest, resp *raft.AppendEntriesResponse) error {
			future, err := p.AppendEntries(req, resp)
			if err != nil {
				return err
			}
			if err := future.Error(); err != nil {
				return err
			}
			*resp = *future.Response()
			return nil
		}
	} else {
		appendEntries = func(req *raft.AppendEntriesRequest, resp *raft.AppendEntriesResponse) error {
			return t1.AppendEntries("t2", "t2", req, resp)
		}
	}

	for idx := range requests {
		t.Run(requests[idx].name, func(t *testing.T) {
			if err := appendEntries(&requests[idx].AppendEntriesRequest, &resp); err != nil {
				t.Errorf("AppendEntries() failed: %v", err)
			} else if got, want := resp.LastLog, requests[idx].lastLog; got != want {
				t.Errorf("resp.LastLog = %v, want %v", got, want)
			}
		})
	}

	close(stop)
}

func TestAppendEntries(t *testing.T) {
	defer goleak.VerifyNone(t)

	testAppendEntries(t, false)
}

func TestAppendEntriesPipeline(t *testing.T) {
	defer goleak.VerifyNone(t)

	testAppendEntries(t, true)
}

func TestSnapshot(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx, cancel := context.WithCancel(context.Background())
	t1, t2, shutdownSig := makeTestPair(ctx, t)
	defer func() {
		cancel()
		<-shutdownSig
	}()

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
