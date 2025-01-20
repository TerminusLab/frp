// Copyright 2019 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package memreport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatedier/frp/pkg/util/log"
	"github.com/fatedier/frp/pkg/util/metric"
	server "github.com/fatedier/frp/server/metrics"
)

var (
	sm = newServerMetrics()

	ServerMetrics  server.ServerMetrics
	StatsCollector Collector
)

func init() {
	ServerMetrics = sm
	StatsCollector = sm
	// sm.run()
}

type serverMetrics struct {
	info *ServerStatistics
	mu   sync.Mutex
}

func newServerMetrics() *serverMetrics {
	return &serverMetrics{
		info: &ServerStatistics{
			TotalTrafficIn:  metric.NewDateCounter(ReserveDays),
			TotalTrafficOut: metric.NewDateCounter(ReserveDays),
			CurConns:        metric.NewCounter(),

			ClientCounts:    metric.NewCounter(),
			ProxyTypeCounts: make(map[string]metric.Counter),

			ProxyStatistics: make(map[string]*ProxyStatistics),
			UserStatistics:  make(map[string]*UserStatistics),
		},
	}
}

func (m *serverMetrics) run() {
	go func() {
		for {
			log.Infof("make lint happy")
			/*
				time.Sleep(12 * time.Hour)
				start := time.Now()
				count, total := m.clearUselessInfo(time.Duration(7*24) * time.Hour)
				log.Debugf("clear useless proxy statistics data count %d/%d, cost %v", count, total, time.Since(start))
			*/
			//			PostUsersTraffic(helper.Cfg.Cloud.ReportUrl, helper.Cfg.Cloud.ReportIntervalSeconds)
		}
	}()
}

func PostUsersTraffic(url string, interval int) {
	doRequest := func(stats []UserTrafficInfo) error {
		data, _ := json.Marshal(stats)
		log.Debugf("post data: %v", data)
		response, err := http.Post(fmt.Sprintf("%s/users/traffic", url), "application/json", bytes.NewBuffer(data))
		if err != nil {
			return err
		}
		response.Body.Close()

		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("post users traffic data error:%s", response.Status)
		}
		return nil
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	for {
		<-ticker.C
		start := time.Now()
		stats := sm.GetAllUsersTraffic()
		if err := doRequest(stats); err != nil {
			log.Errorf("post users statistics data error: %v", err)
		} else {
			log.Debugf("post users statistics data, count:%d, %v, cost %v", len(stats), stats, time.Since(start))
		}
	}
}

func (m *serverMetrics) clearUselessInfo(continuousOfflineDuration time.Duration) (int, int) {
	count := 0
	total := 0
	// To check if there are any proxies that have been closed for more than continuousOfflineDuration and remove them.
	m.mu.Lock()
	defer m.mu.Unlock()
	total = len(m.info.ProxyStatistics)
	for name, data := range m.info.ProxyStatistics {
		if !data.LastCloseTime.IsZero() &&
			data.LastStartTime.Before(data.LastCloseTime) &&
			time.Since(data.LastCloseTime) > continuousOfflineDuration {
			delete(m.info.ProxyStatistics, name)
			count++
			log.Tracef("clear proxy [%s]'s statistics data, lastCloseTime: [%s]", name, data.LastCloseTime.String())
		}
	}
	return count, total
}

func (m *serverMetrics) ClearOfflineProxies() (int, int) {
	return m.clearUselessInfo(0)
}

func (m *serverMetrics) NewClient() {
	m.info.ClientCounts.Inc(1)
}

func (m *serverMetrics) CloseClient() {
	m.info.ClientCounts.Dec(1)
}

func (m *serverMetrics) NewProxy(user, name string, proxyType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counter, ok := m.info.ProxyTypeCounts[proxyType]
	if !ok {
		counter = metric.NewCounter()
	}
	counter.Inc(1)
	m.info.ProxyTypeCounts[proxyType] = counter

	proxyStats, ok := m.info.ProxyStatistics[name]
	if !(ok && proxyStats.ProxyType == proxyType) {
		proxyStats = &ProxyStatistics{
			Name:       name,
			ProxyType:  proxyType,
			CurConns:   metric.NewCounter(),
			TrafficIn:  metric.NewDateCounter(ReserveDays),
			TrafficOut: metric.NewDateCounter(ReserveDays),
		}
		m.info.ProxyStatistics[name] = proxyStats
	}
	proxyStats.LastStartTime = time.Now()

	userStats, ok := m.info.UserStatistics[user]
	if !ok {
		userStats = &UserStatistics{
			Name:       user,
			TrafficIn:  &atomic.Uint64{},
			TrafficOut: &atomic.Uint64{},
			CurProxies: metric.NewCounter(),
		}
		m.info.UserStatistics[user] = userStats
	}
	userStats.CurProxies.Inc(1)
	userStats.LastStartTime = time.Now()
}

func (m *serverMetrics) CloseProxy(user, name string, proxyType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if counter, ok := m.info.ProxyTypeCounts[proxyType]; ok {
		counter.Dec(1)
	}
	if proxyStats, ok := m.info.ProxyStatistics[name]; ok {
		proxyStats.LastCloseTime = time.Now()
	}

	if userStats, ok := m.info.UserStatistics[user]; ok {
		userStats.CurProxies.Dec(1)
		userStats.LastCloseTime = time.Now()
	}
}

func (m *serverMetrics) OpenConnection(name string, _ string) {
	m.info.CurConns.Inc(1)

	m.mu.Lock()
	defer m.mu.Unlock()
	proxyStats, ok := m.info.ProxyStatistics[name]
	if ok {
		proxyStats.CurConns.Inc(1)
		m.info.ProxyStatistics[name] = proxyStats
	}
}

func (m *serverMetrics) CloseConnection(name string, _ string) {
	m.info.CurConns.Dec(1)

	m.mu.Lock()
	defer m.mu.Unlock()
	proxyStats, ok := m.info.ProxyStatistics[name]
	if ok {
		proxyStats.CurConns.Dec(1)
		m.info.ProxyStatistics[name] = proxyStats
	}
}

func (m *serverMetrics) AddTrafficIn(user, name string, _ string, trafficBytes int64) {
	m.info.TotalTrafficIn.Inc(trafficBytes)

	m.mu.Lock()
	defer m.mu.Unlock()

	proxyStats, ok := m.info.ProxyStatistics[name]
	if ok {
		proxyStats.TrafficIn.Inc(trafficBytes)
		m.info.ProxyStatistics[name] = proxyStats
	}

	if userStats, ok := m.info.UserStatistics[user]; ok {
		userStats.TrafficIn.Add(uint64(trafficBytes))
		m.info.UserStatistics[user] = userStats
	}
}

func (m *serverMetrics) AddTrafficOut(user, name string, _ string, trafficBytes int64) {
	m.info.TotalTrafficOut.Inc(trafficBytes)

	m.mu.Lock()
	defer m.mu.Unlock()

	proxyStats, ok := m.info.ProxyStatistics[name]
	if ok {
		proxyStats.TrafficOut.Inc(trafficBytes)
		m.info.ProxyStatistics[name] = proxyStats
	}

	if userStats, ok := m.info.UserStatistics[user]; ok {
		userStats.TrafficOut.Add(uint64(trafficBytes))
		m.info.UserStatistics[user] = userStats
	}
}

func (m *serverMetrics) GetAllUsersTraffic() (res []UserTrafficInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res = make([]UserTrafficInfo, 0)
	for name, userStat := range m.info.UserStatistics {
		res = append(res, UserTrafficInfo{
			Name:       name,
			TrafficIn:  userStat.TrafficIn.Load(),
			TrafficOut: userStat.TrafficOut.Load(),
		})
	}

	return res
}

// Get stats data api.

func (m *serverMetrics) GetServer() *ServerStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := &ServerStats{
		TotalTrafficIn:  m.info.TotalTrafficIn.TodayCount(),
		TotalTrafficOut: m.info.TotalTrafficOut.TodayCount(),
		CurConns:        int64(m.info.CurConns.Count()),
		ClientCounts:    int64(m.info.ClientCounts.Count()),
		ProxyTypeCounts: make(map[string]int64),
	}
	for k, v := range m.info.ProxyTypeCounts {
		s.ProxyTypeCounts[k] = int64(v.Count())
	}
	return s
}

func (m *serverMetrics) GetProxiesByType(proxyType string) []*ProxyStats {
	res := make([]*ProxyStats, 0)
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, proxyStats := range m.info.ProxyStatistics {
		if proxyStats.ProxyType != proxyType {
			continue
		}

		ps := &ProxyStats{
			Name:            name,
			Type:            proxyStats.ProxyType,
			TodayTrafficIn:  proxyStats.TrafficIn.TodayCount(),
			TodayTrafficOut: proxyStats.TrafficOut.TodayCount(),
			CurConns:        int64(proxyStats.CurConns.Count()),
		}
		if !proxyStats.LastStartTime.IsZero() {
			ps.LastStartTime = proxyStats.LastStartTime.Format("01-02 15:04:05")
		}
		if !proxyStats.LastCloseTime.IsZero() {
			ps.LastCloseTime = proxyStats.LastCloseTime.Format("01-02 15:04:05")
		}
		res = append(res, ps)
	}
	return res
}

func (m *serverMetrics) GetProxiesByTypeAndName(proxyType string, proxyName string) (res *ProxyStats) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, proxyStats := range m.info.ProxyStatistics {
		if proxyStats.ProxyType != proxyType {
			continue
		}

		if name != proxyName {
			continue
		}

		res = &ProxyStats{
			Name:            name,
			Type:            proxyStats.ProxyType,
			TodayTrafficIn:  proxyStats.TrafficIn.TodayCount(),
			TodayTrafficOut: proxyStats.TrafficOut.TodayCount(),
			CurConns:        int64(proxyStats.CurConns.Count()),
		}
		if !proxyStats.LastStartTime.IsZero() {
			res.LastStartTime = proxyStats.LastStartTime.Format("01-02 15:04:05")
		}
		if !proxyStats.LastCloseTime.IsZero() {
			res.LastCloseTime = proxyStats.LastCloseTime.Format("01-02 15:04:05")
		}
		break
	}
	return
}

func (m *serverMetrics) GetProxyTraffic(name string) (res *ProxyTrafficInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	proxyStats, ok := m.info.ProxyStatistics[name]
	if ok {
		res = &ProxyTrafficInfo{
			Name: name,
		}
		res.TrafficIn = proxyStats.TrafficIn.GetLastDaysCount(ReserveDays)
		res.TrafficOut = proxyStats.TrafficOut.GetLastDaysCount(ReserveDays)
	}
	return
}

func (m *serverMetrics) GetUserTraffic(names []string) (res []UserTrafficInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res = make([]UserTrafficInfo, 0)
	for _, name := range names {
		if userStat, ok := m.info.UserStatistics[name]; ok {
			res = append(res, UserTrafficInfo{
				Name:       name,
				TrafficIn:  userStat.TrafficIn.Load(),
				TrafficOut: userStat.TrafficOut.Load(),
			})
		}
	}

	return res
}
