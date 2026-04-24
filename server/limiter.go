package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/go-retryablehttp"
	"golang.org/x/time/rate"

	"github.com/fatedier/frp/pkg/config/types"
	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/frp/server/helper"
)

const (
	DefaultLimitBandwidth int64 = 1000 * 1000 * 1
)

func GetDefaultBandwidth() int64 {
	xl := xlog.New()
	configBandwidth := helper.Cfg.BandwidthLimiter.DefaultBandwidth
	xl.Infof("config default bandwidth: [%v]", configBandwidth.String())
	if configBandwidth.Bytes() < DefaultLimitBandwidth {
		return DefaultLimitBandwidth
	}
	return configBandwidth.Bytes()
}

func convertMbToMBAndKB(mbStr string) (string, error) {
	//        mbStr = strings.TrimSpace(strings.ToLower(mbStr))
	suffix := "Mb"

	if !strings.HasSuffix(mbStr, suffix) {
		return "", fmt.Errorf("invalid format: %s", mbStr)
	}

	numberStr := strings.TrimSuffix(mbStr, suffix)

	mbValue, err := strconv.ParseFloat(numberStr, 64)
	if err != nil {
		return "", fmt.Errorf("failed to parse number: %s", numberStr)
	}

	mbValueConverted := mbValue * 0.125
	kbValue := mbValueConverted * 1024

	return fmt.Sprintf("%vKB", int(kbValue)), nil
}

type LimiterManager struct {
	rateLimiter map[string]*rate.Limiter

	mu sync.RWMutex
}

func NewLimiterManager() *LimiterManager {
	return &LimiterManager{
		rateLimiter: make(map[string]*rate.Limiter),
	}
}

func (lm *LimiterManager) GetBandwidth(terminusName string) map[string]int64 {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	bandwidth := make(map[string]int64)
	if terminusName != "" {
		if l, ok := lm.rateLimiter[terminusName]; ok {
			bandwidth[terminusName] = int64(l.Limit())
		}

		return bandwidth
	}

	for key, value := range lm.rateLimiter {
		bandwidth[key] = int64(value.Limit())
	}

	return bandwidth
}

func (lm *LimiterManager) GetUserUsingDefaultBandwidth() (terminusNames []string) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	for key, value := range lm.rateLimiter {
		if int64(value.Limit()) == GetDefaultBandwidth() {
			terminusNames = append(terminusNames, key)
		}
	}

	return
}

func (lm *LimiterManager) GetRateLimiter(terminusName string, limitBytes int64, burstBytes int) *rate.Limiter {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if l, ok := lm.rateLimiter[terminusName]; ok {
		l.SetLimit(rate.Limit(float64(limitBytes)))
		l.SetBurst(burstBytes)

		return l
	}

	limiter := rate.NewLimiter(rate.Limit(float64(limitBytes)), burstBytes)
	lm.rateLimiter[terminusName] = limiter

	return limiter
}

func (lm *LimiterManager) UpdateLimiterByGroup(clusterUsers []string, limitBytes int64, burstBytes int) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	updatedCount := 0
	skippedCount := 0
	for i := range clusterUsers {
		if l, ok := lm.rateLimiter[clusterUsers[i]]; ok {
			// User is connected to this server, update their limiter
			l.SetLimit(rate.Limit(float64(limitBytes)))
			l.SetBurst(burstBytes)
			updatedCount++
		} else {
			// User is not connected to this server, skip (they will be updated by their own frp server)
			skippedCount++
		}
	}
	if skippedCount > 0 {
		xl := xlog.New()
		xl.Debugf("UpdateLimiterByGroup: updated %d local users, skipped %d users (connected to other frp servers)", updatedCount, skippedCount)
	}
}

func (lm *LimiterManager) GetAllTerminusNames() (terminusNames []string) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	for key := range lm.rateLimiter {
		terminusNames = append(terminusNames, key)
	}

	return
}

func (lm *LimiterManager) UpdateLoop(getOnlineUsers func() []string) {
	xl := xlog.New()
	tick := time.NewTicker(1 * time.Hour)
	defer tick.Stop()
	for range tick.C {
		xl.Infof("Update All terminus name in local frp")
		allTerminusNames := lm.GetAllTerminusNames()
		xl.Infof("local terminus name list %v", allTerminusNames)

		onlineUsers := getOnlineUsers()
		xl.Infof("online users: %v", onlineUsers)
		onlineUsersSet := make(map[string]bool, len(onlineUsers))
		for _, user := range onlineUsers {
			onlineUsersSet[user] = true
		}

		onlineTerminusNames := make([]string, 0, len(allTerminusNames))
		for _, name := range allTerminusNames {
			if onlineUsersSet[name] {
				onlineTerminusNames = append(onlineTerminusNames, name)
			}
		}

		if len(onlineTerminusNames) > 0 {
			xl.Infof("updating bandwidth for %d online users", len(onlineTerminusNames))
			lm.UpdateLimiterByTerminusNames(onlineTerminusNames)
		} else {
			xl.Infof("no online users to update")
		}
	}
}

func (lm *LimiterManager) GetBandwidthByTerminusName(terminusName string) (int64, []string, error) {
	xl := xlog.New()
	respBody, err := lm.GetCommon(helper.Cfg.Cloud.URL+"/v1/resource/clusterUsers", []byte("terminusName="+terminusName))
	if err != nil {
		xl.Warnf("Get cluster users: %v", err)
		return 0, nil, err
	}
	xl.Infof(respBody)
	var response Response
	err = json.Unmarshal([]byte(respBody), &response)
	if err != nil {
		xl.Warnf("Error unmarshaling response: %v", err)
		return 0, nil, err
	}
	xl.Debugf("response: %v", response)
	if response.Code != 200 || response.Data.TerminusID == "" {
		xl.Warnf("invalid response for %v: code=%v, terminusID=%v", terminusName, response.Code, response.Data.TerminusID)
		return 0, nil, errors.New("invalid response")
	}

	parsedUUID, err := uuid.Parse(response.Data.TerminusID)
	if err != nil {
		xl.Warnf("Invalid uuid %v", response.Data.TerminusID)
		return 0, nil, err
	}
	xl.Debugf("parsed UUID: %v", parsedUUID)

	clusterUsers := make([]string, 0, len(response.Data.Users))
	for _, v := range response.Data.Users {
		clusterUsers = append(clusterUsers, v.TerminusName)
	}
	xl.Infof("cluster users: %v", clusterUsers)

	if !slices.Contains(clusterUsers, terminusName) {
		xl.Warnf("terminusName %v not found in cluster users %v", terminusName, clusterUsers)
		return 0, nil, errors.New("terminusName not found in cluster users")
	}

	downBandwidth, err := convertMbToMBAndKB(response.Data.DownBandwidth)
	if err != nil {
		xl.Warnf("Error converting bandwidth %v: %v", response.Data.DownBandwidth, err)
		return 0, nil, err
	}
	bd, err := types.NewBandwidthQuantity(downBandwidth)
	if err != nil {
		xl.Warnf("Error creating bandwidth quantity %v: %v", downBandwidth, err)
		return 0, nil, err
	}

	limitBytes := bd.Bytes()
	count := len(clusterUsers)
	if count > 0 {
		limitBytes /= int64(count)
	}
	xl.Infof("total bandwidth: %v bytes/s, per user: %v bytes/s (cluster size: %d)", bd.Bytes(), limitBytes, count)
	return limitBytes, clusterUsers, nil
}

func (lm *LimiterManager) GetCommon(requestURL string, requestData []byte) (string, error) {
	xl := xlog.New()

	bodyReader := bytes.NewReader(requestData)
	req, err := retryablehttp.NewRequest(http.MethodPost, requestURL, bodyReader)
	if err != nil {
		xl.Warnf("client: could not create request: %s\n", err)
		return "", err
	}
	req.Header.Set("Authorization", helper.Cfg.Cloud.Token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := retryablehttp.NewClient()
	client.HTTPClient.Timeout = 8 * time.Second
	client.RetryMax = 0
	client.RetryWaitMin = 1 * time.Second
	client.RetryWaitMax = 10 * time.Second
	client.RequestLogHook = func(l retryablehttp.Logger, r *http.Request, attemptNum int) {
		if attemptNum != 0 {
			xl.Warnf("RequestLogHook: %s %s (attempt %d)", r.Method, r.URL, attemptNum)
			// SendFeishu(fmt.Sprintf("retry -> %s", r.URL))
		}
	}

	client.ResponseLogHook = func(l retryablehttp.Logger, resp *http.Response) {
		if resp.StatusCode != http.StatusOK {
			xl.Warnf("ResponseLogHook: %+v", resp)
			// SendFeishu(fmt.Sprintf("status: %s -> %s", resp.Status, resp.Request.URL))
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		xl.Warnf("client: error making http request: %s\n", err)
		return "", err
	}
	xl.Infof("%+v", resp)
	defer resp.Body.Close()

	bds, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	body := string(bds)
	xl.Infof(body)
	if resp.StatusCode != http.StatusOK {
		// SendFeishu(resp.Status + " -> " + requestURL)
		return body, errors.New(resp.Status)
	}

	return body, nil
}

func (lm *LimiterManager) UpdateLimiterAfter(terminusName string) {
	xl := xlog.New()
	xl.AppendPrefix(terminusName)
	timer := time.After(10 * time.Minute)

	go func() {
		<-timer
		xl.Infof("update limiter for %v", terminusName)
		limitBytes, clusterUsers, err := lm.GetBandwidthByTerminusName(terminusName)
		if err == nil {
			lm.UpdateLimiterByGroup(clusterUsers, limitBytes, int(1*limitBytes))
		}
	}()
}

func (lm *LimiterManager) Exist(terminusName string) bool {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	_, ok := lm.rateLimiter[terminusName]
	return ok
}

func (lm *LimiterManager) RemoveLimiter(terminusName string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	delete(lm.rateLimiter, terminusName)
}

func (lm *LimiterManager) UpdateLimiterByTerminusNames(terminusNames []string) {
	xl := xlog.New()

	for i := range terminusNames {
		if !lm.Exist(terminusNames[i]) {
			xl.Infof("not found terminus name (%v) in local", terminusNames[i])
			continue
		}

		terminusName := terminusNames[i]
		limitBytes, clusterUsers, err := lm.GetBandwidthByTerminusName(terminusName)
		if err == nil {
			lm.UpdateLimiterByGroup(clusterUsers, limitBytes, int(1*limitBytes))
			xl.Infof("update %vs bandwidth limit to %v", terminusName, limitBytes)
		} else {
			xl.Warnf("update bandwidth for %v(err: %v)", terminusName, err)
		}
		time.Sleep(1 * time.Second)
	}
}

func (lm *LimiterManager) UpdateLimiterByTerminusName(terminusName string) {
	xl := xlog.New()
	bandwidth, clusterUsers, err := lm.GetBandwidthByTerminusName(terminusName)
	if err == nil {
		lm.UpdateLimiterByGroup(clusterUsers, bandwidth, int(1*bandwidth))
		xl.Infof("updated bandwidth for cluster users %v to %v bytes/s", clusterUsers, bandwidth)
	} else {
		xl.Warnf("failed to update bandwidth for %v: %v", terminusName, err)
	}
}

func (lm *LimiterManager) GetLimiterByTerminusName(terminusName string) *rate.Limiter {
	xl := xlog.New()
	limitBytes := GetDefaultBandwidth()

	limiter := lm.GetRateLimiter(terminusName, limitBytes, int(1*limitBytes))
	xl.Infof("get limiter for %v: limit=%v bytes/s (default, will update async), limiter=%p", terminusName, limitBytes, limiter)

	// Update bandwidth asynchronously to avoid blocking login
	go func() {
		bandwidth, clusterUsers, err := lm.GetBandwidthByTerminusName(terminusName)
		if err == nil {
			lm.UpdateLimiterByGroup(clusterUsers, bandwidth, int(1*bandwidth))
			xl.Infof("updated limiter for %v: limit=%v bytes/s", terminusName, bandwidth)
		} else {
			xl.Infof("failed to get bandwidth for %v, using default: %v", terminusName, limitBytes)
			lm.UpdateLimiterAfter(terminusName)
		}
	}()

	return limiter
}

type Users struct {
	TerminusName string `json:"terminusName"`
	Role         string `json:"role"`
	ReverseProxy string `json:"reverseProxy"`
}

type ClusterUsers struct {
	TerminusID    string  `json:"terminusId"`
	Users         []Users `json:"users"`
	UpBandwidth   string  `json:"upBandwidth"`
	DownBandwidth string  `json:"downBandwidth"`
}
type Response struct {
	Code    int          `json:"code"`
	Message string       `json:"message"`
	Data    ClusterUsers `json:"data"`
}
