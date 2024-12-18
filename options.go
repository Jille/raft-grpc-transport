package transport

import "time"

type Option func(m *Manager)

// WithHeartbeatTimeout configures the transport to not wait for more than d
// for a heartbeat to be executed by a remote peer.
func WithHeartbeatTimeout(d time.Duration) Option {
	return func(m *Manager) {
		m.heartbeatTimeout = d
	}
}

// WithAppendEntriesChunkSize configures the chunk size to use when switching
// to chunked AppendEntries. The default value is 4MB (the gRPC default), but
// as there is no way to auto-discover that value, it's up to the developer
// to configure this, if the default value is not appropriate.
func WithAppendEntriesChunkSize(v int) Option {
	return func(m *Manager) {
		m.appendEntriesChunkSize = v
	}
}
