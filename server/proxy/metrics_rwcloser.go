// Copyright 2017 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package proxy

import (
	"io"
	"sync"

	"github.com/fatedier/frp/server/metrics"
)

const metricsTrafficFlushBytes = 1024 * 1024

// metricsReadWriteCloser counts bytes on Write and periodically reports to metrics.Server
// (same semantics as batching post-Join AddTraffic*, but during the connection).
type metricsReadWriteCloser struct {
	io.ReadWriteCloser
	name      string
	proxyType string
	out       bool // true -> AddTrafficOut, false -> AddTrafficIn
	mu        sync.Mutex
	pending   int64
}

func newMetricsTrafficRW(name, proxyType string, rwc io.ReadWriteCloser, out bool) *metricsReadWriteCloser {
	return &metricsReadWriteCloser{
		ReadWriteCloser: rwc,
		name:            name,
		proxyType:       proxyType,
		out:             out,
	}
}

func (m *metricsReadWriteCloser) flushLocked() {
	if m.pending <= 0 {
		return
	}
	n := m.pending
	m.pending = 0
	if m.out {
		metrics.Server.AddTrafficOut(m.name, m.proxyType, n)
	} else {
		metrics.Server.AddTrafficIn(m.name, m.proxyType, n)
	}
}

func (m *metricsReadWriteCloser) Write(p []byte) (n int, err error) {
	n, err = m.ReadWriteCloser.Write(p)
	m.mu.Lock()
	m.pending += int64(n)
	if err != nil || m.pending >= metricsTrafficFlushBytes {
		m.flushLocked()
	}
	m.mu.Unlock()
	return n, err
}

func (m *metricsReadWriteCloser) Close() error {
	m.mu.Lock()
	m.flushLocked()
	m.mu.Unlock()
	return m.ReadWriteCloser.Close()
}
