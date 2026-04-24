// Copyright 2017 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fatedier/frp/pkg/metrics/memreport"
	"github.com/fatedier/frp/pkg/util/log"
)

type limiterGeneralResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type getBandwidthResp struct {
	BandwidthLimit map[string]string `json:"bandwidthLimit"`
}

type getUserTrafficResp struct {
	UpTimeMs      int64                       `json:"upTimeMs"`
	TrafficByName []memreport.UserTrafficInfo `json:"trafficByName"`
}

// POST /api/bandwidth
func (svr *Service) apiUpdateLimiters(w http.ResponseWriter, r *http.Request) {
	res := limiterGeneralResponse{Code: 200}

	log.Infof("Http request: [%s]", r.URL.Path)
	defer func() {
		log.Infof("Http response [%s]: code [%d]", r.URL.Path, res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()

	var terminusNames []string
	err := json.NewDecoder(r.Body).Decode(&terminusNames)
	if err != nil {
		res.Code = 400
		res.Msg = "invalid request body"
		return
	}

	go svr.limiterManager.UpdateLimiterByTerminusNames(terminusNames)
}

// GET /api/bandwidth
func (svr *Service) apiBandwidth(w http.ResponseWriter, r *http.Request) {
	res := limiterGeneralResponse{Code: 200}

	log.Infof("Http request: [%s]", r.URL.Path)
	defer func() {
		log.Infof("Http response [%s]: code [%d]", r.URL.Path, res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()

	w.Header().Set("Content-Type", "application/json")

	terminusName := r.URL.Query().Get("name")

	bandwidth := svr.limiterManager.GetBandwidth(terminusName)
	outBandwidth := make(map[string]string)
	for key, value := range bandwidth {
		outBandwidth[key] = fmt.Sprintf("%vMb", float64(value)*8/1024/1024)
	}
	bandwidthResp := getBandwidthResp{
		BandwidthLimit: outBandwidth,
	}

	buf, _ := json.Marshal(&bandwidthResp)
	res.Msg = string(buf)
}

// POST /api/traffic (separate auth from dashboard routes)
func (svr *Service) apiTraffic(w http.ResponseWriter, r *http.Request) {
	res := limiterGeneralResponse{Code: 200}

	log.Infof("Http request: [%s]", r.URL.Path)
	defer func() {
		log.Infof("Http response [%s]: code [%d]", r.URL.Path, res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()

	var terminusNames []string
	err := json.NewDecoder(r.Body).Decode(&terminusNames)
	if err != nil {
		res.Code = 400
		res.Msg = "invalid request body"
		return
	}

	trafficResp := getUserTrafficResp{}
	trafficResp.UpTimeMs = svr.cfg.UpTime
	trafficResp.TrafficByName = memreport.StatsCollector.GetUserTraffic(terminusNames)

	buf, _ := json.Marshal(&trafficResp)
	res.Msg = string(buf)
}

// GET /api/traffic
func (svr *Service) getAPITraffic(w http.ResponseWriter, r *http.Request) {
	res := limiterGeneralResponse{Code: 200}

	log.Infof("Http request: [%s]", r.URL.Path)
	defer func() {
		log.Infof("Http response [%s]: code [%d]", r.URL.Path, res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()

	trafficResp := getUserTrafficResp{}
	trafficResp.UpTimeMs = svr.cfg.UpTime
	trafficResp.TrafficByName = memreport.StatsCollector.GetAllUsersTraffic()

	buf, _ := json.Marshal(&trafficResp)
	res.Msg = string(buf)
}
