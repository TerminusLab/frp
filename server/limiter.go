package server

import (
	"sync"
	"time"
	"bytes"
	"slices"
	"errors"
	"net/http"
	"io/ioutil"
	"encoding/json"


	"github.com/google/uuid"
	"golang.org/x/time/rate"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/frp/pkg/config/types"
)

type LimiterManager struct {
	clusterUser map[string]string
	rateLimiter map[string]*rate.Limiter

	mu sync.RWMutex
}

func NewLimiterManager() *LimiterManager {
	return &LimiterManager{
		clusterUser: make(map[string]string),
		rateLimiter: make(map[string]*rate.Limiter),
	}
}

func (lm *LimiterManager) GetRateLimiter(terminusName string, limitBytes int64, burstBytes int) * rate.Limiter {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if l, ok := lm.rateLimiter[terminusName]; !ok {
		limiter := rate.NewLimiter(rate.Limit(float64(limitBytes)), burstBytes)

		return limiter
	} else {
		l.SetLimit(rate.Limit(float64(limitBytes)))
		l.SetBurst(int(burstBytes))

		return l
	}
}

func (lm *LimiterManager) UpdateLimiterByGroup(terminusNames [] string, limitBytes int64, burstBytes int) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	for i := range terminusNames {
		if l, ok := lm.rateLimiter[terminusNames[i]]; ok {
			l.SetLimit(rate.Limit(float64(limitBytes)))
			l.SetBurst(int(burstBytes))
		}
	}
}


func (lm *LimiterManager) AddRateLimiter(terminusId, terminusName string, limitBytes int64, burstBytes int) *rate.Limiter {
	lm.mu.Lock()
	defer lm.mu.Unlock()
/*
	var limiter *rate.Limiter
	if terminusId != terminusName {
		if l, ok := lm.rateLimiters[terminusName][terminusName] {
			limiter = l
			delete(lm.rateLimiters[terminusName]
		}
	}
	*/
	/*

	if al, ok := lm.clusterUser[terminusId]; !ok {
		lm.rateLimiter[terminusId] = make(map[string]*rate.Limiter)
	} else {
		for _, l := range al {
			l.SetLimit(rate.Limit(float64(limitBytes)))
			l.SetBurst(int(burstBytes))
		}
	}

	if l, ok := lm.rateLimiters[terminusId][terminusName]; !ok {
		if limiter == nil {
			limiter = rate.NewLimiter(rate.Limit(float64(limitBytes)), burstBytes)
		}
		lm.rateLimiters[terminusId][terminusName] = limiter

		return limiter
	} else {
		return l
	}
	*/
	return nil
}

/*
func (lm *LimiterManager) Get(terminusId, terminusName string) (l *rate.Limiter, ok bool) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	l, ok = lm.rateLimiter[terminusId][terminusName]

	return
}
*/

func (lm *LimiterManager) UpdateLoop() {
	xl := xlog.New()
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			xl.Infof("ssssssssssssssssssssssssssssssssssssssss 555555555555555555555")
		}
	}
}

func (lm *LimiterManager) GetBandwidthByTerminusName(terminusName string) (int64, []string, error) {
	var limitBytes int64
	var terminusNames []string
	xl := xlog.New()
	respBody, err := lm.GetCommon("https://cloud-dev-api.bttcdn.com/v1/resource/clusterUsers", []byte("terminusName="+terminusName))
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
			xl.Infof(">>>><<<<<<<<<<<<<<<<: %v", bd.Bytes())
			limitBytes := bd.Bytes()
			count := len(terminusNames)
			if count > 0 {
				limitBytes /= int64(count)
			}
			xl.Infof(">>>><<<<<<<<<<<<<<<<: %v %v", bd.Bytes(), limitBytes)
			return  limitBytes, terminusNames, nil
		}
	} else {
		xl.Warnf("using default valueeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee %v", terminusName)
		return limitBytes, terminusNames, errors.New("invalid reponse")
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
	req.Header.Set("Authorization", "b9ec4f2904f891405df84be3a0dcc31a")
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

func (lm *LimiterManager) UpdateLimiterByTerminusNames(terminusNames []string) {
        xl := xlog.New()

        for i := range terminusNames {
                terminusName := terminusNames[i]
                limitBytes, terminusNames, err := lm.GetBandwidthByTerminusName(terminusName)
                if err == nil {
			lm.UpdateLimiterByGroup(terminusNames, limitBytes, int(1*limitBytes))
                        xl.Infof("update %vs bandwidth limit to %v", terminusName, limitBytes)
                } else {
                        xl.Warnf("update bandwidth for %v(%v)", err)
                }
        }
        time.Sleep(1 * time.Second)
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

