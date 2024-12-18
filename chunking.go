package transport

import (
	pb "github.com/Jille/raft-grpc-transport/proto"
	"google.golang.org/protobuf/proto"
)

type AppendEntriesChunkedRequestStreamSender interface {
	Send(*pb.AppendEntriesChunkedRequest) error
}

func sendAppendEntriesChunkedRequest(chunkSize int, stream AppendEntriesChunkedRequestStreamSender, appendEntriesRequest *pb.AppendEntriesRequest) error {
	reqBuf, err := proto.Marshal(appendEntriesRequest)
	if err != nil {
		return err
	}

	reqSize := len(reqBuf)
	numChunks := reqSize / chunkSize
	if reqSize%chunkSize != 0 {
		numChunks++
	}

	remainingBytes := reqSize
	for chunkIdx := 0; chunkIdx < numChunks; chunkIdx++ {
		lowerBound := chunkIdx * chunkSize
		upperBound := (chunkIdx + 1) * chunkSize
		if reqSize < upperBound {
			upperBound = reqSize
		}

		remainingBytes -= upperBound - lowerBound
		chunk := &pb.AppendEntriesChunkedRequest{
			RemainingBytes: int64(remainingBytes),
			Chunk:          reqBuf[lowerBound:upperBound],
		}
		if err := stream.Send(chunk); err != nil {
			return err
		}
	}

	return nil
}

type AppendEntriesChunkedRequestStreamReceiver interface {
	Recv() (*pb.AppendEntriesChunkedRequest, error)
}

func receiveAppendEntriesChunkedRequest(stream AppendEntriesChunkedRequestStreamReceiver) (*pb.AppendEntriesRequest, error) {
	var reqBuf []byte

	chunk, err := stream.Recv()
	if err != nil {
		return &pb.AppendEntriesRequest{}, err
	}

	if chunk.RemainingBytes == 0 {
		reqBuf = chunk.Chunk
	} else {
		reqBuf = make([]byte, len(chunk.Chunk)+int(chunk.RemainingBytes))
		lowerBound := copy(reqBuf, chunk.Chunk)

		for chunk.RemainingBytes > 0 {
			chunk, err = stream.Recv()
			if err != nil {
				return &pb.AppendEntriesRequest{}, err
			}

			lowerBound += copy(reqBuf[lowerBound:], chunk.Chunk)
		}
	}

	appendEntriesRequest := new(pb.AppendEntriesRequest)
	if err := proto.Unmarshal(reqBuf, appendEntriesRequest); err != nil {
		return &pb.AppendEntriesRequest{}, err
	}

	return appendEntriesRequest, nil
}
