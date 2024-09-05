package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/fatedier/frp/pkg/config/types"
	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/frp/server/helper"
	"github.com/google/uuid"
	"github.com/hashicorp/go-retryablehttp"
	"golang.org/x/time/rate"
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
	} else {
		return configBandwidth.Bytes()
	}
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
	lm.mu.Lock()
	defer lm.mu.Unlock()

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

	if l, ok := lm.rateLimiter[terminusName]; !ok {
		limiter := rate.NewLimiter(rate.Limit(float64(limitBytes)), burstBytes)
		lm.rateLimiter[terminusName] = limiter

		return limiter
	} else {
		l.SetLimit(rate.Limit(float64(limitBytes)))
		l.SetBurst(int(burstBytes))

		return l
	}
}

func (lm *LimiterManager) UpdateLimiterByGroup(terminusNames []string, limitBytes int64, burstBytes int) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	for i := range terminusNames {
		if l, ok := lm.rateLimiter[terminusNames[i]]; ok {
			l.SetLimit(rate.Limit(float64(limitBytes)))
			l.SetBurst(int(burstBytes))
		}
	}
}

func (lm *LimiterManager) GetAllTerminusNames() (terminusNames []string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	for key, _ := range lm.rateLimiter {
		terminusNames = append(terminusNames, key)
	}

	return
}

func (lm *LimiterManager) UpdateLoop() {
	xl := xlog.New()
	tick := time.NewTicker(1 * time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			xl.Infof("Update All terminus name in local frp")
			terminusNames := lm.GetAllTerminusNames()
			xl.Infof("local terminus name list %v", terminusNames)
			lm.UpdateLimiterByTerminusNames(terminusNames)
		}
	}
}

func (lm *LimiterManager) GetBandwidthByTerminusName(terminusName string) (int64, []string, error) {
	var limitBytes int64
	var terminusNames []string
	xl := xlog.New()
	respBody, err := lm.GetCommon(helper.Cfg.Cloud.Url+"/v1/resource/clusterUsers", []byte("terminusName="+terminusName))
	if err != nil {
		xl.Warnf("Get clsuter users: %v", err)
		return limitBytes, terminusNames, err
	}
	xl.Infof(respBody)
	var response Reponse
	err = json.Unmarshal([]byte(respBody), &response)
	if err != nil {
		xl.Warnf("Error: %v", err)
		return limitBytes, terminusNames, err
	}
	xl.Debugf("response: %v", response)
	if response.Code == 200 && response.Data.TerminusId != "" {
		parsedUUID, err := uuid.Parse(response.Data.TerminusId)
		if err != nil {
			xl.Warnf("Invalid uuid %v", response.Data.TerminusId)
			return limitBytes, terminusNames, err
		}
		xl.Warnf("%v %v", response.Data.TerminusId, parsedUUID)
		//		terminusId := parsedUUID.String()
		for _, v := range response.Data.Users {
			terminusNames = append(terminusNames, v.TerminusName)
		}
		xl.Infof("%v", terminusNames)
		if !slices.Contains(terminusNames, terminusName) {
			return limitBytes, terminusNames, errors.New("invalid reponse")
		}
		bd, err := types.NewBandwidthQuantity(response.Data.DownBandwidth)
		if err != nil {
			xl.Warnf("VVVVVVVVVVVVVVVVVVVVVVV %v %v", err, response.Data.DownBandwidth)
			return limitBytes, terminusNames, err
		} else {
			limitBytes := bd.Bytes()
			count := len(terminusNames)
			if count > 0 {
				limitBytes /= int64(count)
			}
			xl.Infof("all: %v, div: %v", bd.Bytes(), limitBytes)
			return limitBytes, terminusNames, nil
		}
	} else {
		xl.Warnf("invalid  response for %v", terminusName)
		//		SendFeishu
		return limitBytes, terminusNames, errors.New("invalid response")
	}
}

func (lm *LimiterManager) GetCommon(requestUrl string, requestData []byte) (string, error) {
	xl := xlog.New()

	bodyReader := bytes.NewReader(requestData)
	req, err := retryablehttp.NewRequest(http.MethodPost, requestUrl, bodyReader)
	if err != nil {
		xl.Warnf("client: could not create request: %s\n", err)
		return "", err
	}
	req.Header.Set("Authorization", helper.Cfg.Cloud.Token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := retryablehttp.NewClient()
	client.HTTPClient.Timeout = 15 * time.Second
	client.RetryMax = 1
	client.RetryWaitMin = 1 * time.Second
	client.RetryWaitMax = 10 * time.Second
	client.RequestLogHook = func(l retryablehttp.Logger, r *http.Request, attemptNum int) {
		if attemptNum != 0 {
			xl.Warnf("RequestLogHook: %s %s (attempt %d)", r.Method, r.URL, attemptNum)
			//SendFeishu(fmt.Sprintf("retry -> %s", r.URL))
		}
	}

	client.ResponseLogHook = func(l retryablehttp.Logger, resp *http.Response) {
		if resp.StatusCode != http.StatusOK {
			xl.Warnf("ResponseLogHook: %+v", resp)
			//SendFeishu(fmt.Sprintf("status: %s -> %s", resp.Status, resp.Request.URL))
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		xl.Warnf("client: error making http request: %s\n", err)
		return "", err
	}
	xl.Infof("%+v", resp)
	defer resp.Body.Close()

	bds, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	body := string(bds)
	xl.Infof(body)
	if resp.StatusCode != http.StatusOK {
		//SendFeishu(resp.Status + " -> " + requestUrl)
		return body, errors.New(resp.Status)
	}

	return body, nil
}

func (lm *LimiterManager) UpdateLimiterAfter(terminusName string) {
	xl := xlog.New()
	xl.AppendPrefix(terminusName)
	timer := time.After(10 * time.Minute)

	go func() {
		select {
		case <-timer:
			xl.Infof("update limiteer for %v", terminusName)
			limitBytes, terminusNames, err := lm.GetBandwidthByTerminusName(terminusName)
			if err == nil {
				lm.UpdateLimiterByGroup(terminusNames, limitBytes, int(1*limitBytes))
			}

		}
	}()
}

func (lm *LimiterManager) Exist(terminusName string) bool {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	_, ok := lm.rateLimiter[terminusName]
	return ok
}

func (lm *LimiterManager) UpdateLimiterByTerminusNames(terminusNames []string) {
	xl := xlog.New()

	for i := range terminusNames {
		if !lm.Exist(terminusNames[i]) {
			xl.Infof("not found terminus name (%v) in local", terminusNames[i])
			continue
		}

		terminusName := terminusNames[i]
		limitBytes, terminusNames, err := lm.GetBandwidthByTerminusName(terminusName)
		if err == nil {
			lm.UpdateLimiterByGroup(terminusNames, limitBytes, int(1*limitBytes))
			xl.Infof("update %vs bandwidth limit to %v", terminusName, limitBytes)
		} else {
			xl.Warnf("update bandwidth for %v(err: %v)", terminusName, err)
		}
		time.Sleep(1 * time.Second)
	}
}

func (lm *LimiterManager) UpdateLimiterByTerminusName(terminusName string) {
	xl := xlog.New()
	bandwidth, terminusNames, err := lm.GetBandwidthByTerminusName(terminusName)
	if err == nil {
		lm.UpdateLimiterByGroup(terminusNames, bandwidth, int(1*bandwidth))
		xl.Infof("--------------> %v %v", terminusNames, bandwidth)
	}
}

func (lm *LimiterManager) GetLimiterByTerminusName(terminusName string) *rate.Limiter {
	xl := xlog.New()
	var limitBytes = GetDefaultBandwidth()
	bandwidth, terminusNames, err := lm.GetBandwidthByTerminusName(terminusName)
	if err == nil {
		limitBytes = bandwidth
		lm.UpdateLimiterByGroup(terminusNames, limitBytes, int(1*limitBytes))
	} else {
		xl.Infof("USING DEFAULT BANDWIDTH: %v", limitBytes)
		go func() {
			lm.UpdateLimiterAfter(terminusName)
		}()
	}

	limiter := lm.GetRateLimiter(terminusName, limitBytes, int(1*limitBytes))
	xl.Infof("look %v %v %p", terminusName, limitBytes, limiter)

	return limiter
}

type Users struct {
	TerminusName string `json:"terminusName"`
	Role         string `json:"role"`
	ReverseProxy string `json:"reverseProxy"`
}

type ClusterUsers struct {
	TerminusId    string  `json:"terminusId"`
	Users         []Users `json:"users"`
	UpBandwidth   string  `json:"upBandwidth"`
	DownBandwidth string  `json:"downBandwidth"`
}
type Reponse struct {
	Code    int          `json:"code"`
	Message string       `json:"message"`
	Data    ClusterUsers `json:"data"`
}
