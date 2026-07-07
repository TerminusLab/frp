// Copyright 2017 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package proxy

import (
	"io"
	"sync/atomic"

	"github.com/fatedier/frp/server/metrics"
)

const metricsTrafficFlushBytes = 1024 * 1024

// metricsReadWriteCloser counts bytes on Write and periodically reports to metrics.Server.
// Used for long-lived connections (e.g. HTTP/2 over TCP/HTTPS proxy) so traffic is
// recorded during the connection, not only after Join returns.
type metricsReadWriteCloser struct {
	io.ReadWriteCloser
	user      string
	name      string
	proxyType string
	out       bool // true -> AddTrafficOut, false -> AddTrafficIn
	pending   atomic.Int64
}

func newMetricsTrafficRW(user, name, proxyType string, rwc io.ReadWriteCloser, out bool) *metricsReadWriteCloser {
	return &metricsReadWriteCloser{
		ReadWriteCloser: rwc,
		user:            user,
		name:            name,
		proxyType:       proxyType,
		out:             out,
	}
}

func (m *metricsReadWriteCloser) flush() {
	n := m.pending.Swap(0)
	if n <= 0 {
		return
	}
	if m.out {
		metrics.Server.AddTrafficOut(m.user, m.name, m.proxyType, n)
	} else {
		metrics.Server.AddTrafficIn(m.user, m.name, m.proxyType, n)
	}
}

func (m *metricsReadWriteCloser) Write(p []byte) (n int, err error) {
	n, err = m.ReadWriteCloser.Write(p)
	if m.pending.Add(int64(n)) >= metricsTrafficFlushBytes || err != nil {
		m.flush()
	}
	return n, err
}

func (m *metricsReadWriteCloser) Close() error {
	m.flush()
	return m.ReadWriteCloser.Close()
}
