package vhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"golang.org/x/sync/singleflight"

	httppkg "github.com/fatedier/frp/pkg/util/http"
	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/frp/server/helper"
)

type Response struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    Cert   `json:"data"`
}

type Cert struct {
	Zone    string `json:"zone"`
	Cert    string `json:"cert"`
	Key     string `json:"key"`
	EndDate string `json:"enddate"`
}

var (
	certs         = make(map[string]Cert)
	mu            sync.RWMutex
	httpClient    *retryablehttp.Client
	httpClientMu  sync.Once
	downloadGroup singleflight.Group
)

func getHTTPClient() *retryablehttp.Client {
	httpClientMu.Do(func() {
		httpClient = retryablehttp.NewClient()
		httpClient.HTTPClient.Timeout = 6 * time.Second
		httpClient.RetryMax = 1
		httpClient.RetryWaitMin = 1 * time.Second
		httpClient.RetryWaitMax = 2 * time.Second
		httpClient.RequestLogHook = func(l retryablehttp.Logger, r *http.Request, attemptNum int) {
			if attemptNum != 0 {
				xl := xlog.New()
				xl.Infof("RequestLogHook: %s %s (attempt %d)", r.Method, r.URL, attemptNum)
			}
		}
		httpClient.ResponseLogHook = func(l retryablehttp.Logger, resp *http.Response) {
			if resp.StatusCode != http.StatusOK {
				xl := xlog.New()
				xl.Warnf("ResponseLogHook: status=%d, url=%s", resp.StatusCode, resp.Request.URL)
			}
		}
	})
	return httpClient
}

func AddCertToCache(key string, cert Cert) {
	mu.Lock()
	defer mu.Unlock()
	certs[key] = cert
}

func GetCertFromCache(key string) (Cert, bool) {
	mu.RLock()
	defer mu.RUnlock()
	cert, exists := certs[key]
	return cert, exists
}

func GetCertRequest(name, user, password, theurl string) (string, error) {
	xl := xlog.New()
	var ret string

	bodyReader := bytes.NewReader([]byte{})
	requestURL := theurl + "/download?name="
	requestURL += url.QueryEscape(name)
	xl.Infof("Downloading certificate for terminusName=%v, url=%s %s", name, http.MethodGet, requestURL)

	req, err := retryablehttp.NewRequest(http.MethodGet, requestURL, bodyReader)
	if err != nil {
		xl.Warnf("Failed to create certificate download request for terminusName=%v, url=%s: %v", name, requestURL, err)
		return ret, err
	}
	req.Header.Set("Authorization", httppkg.BasicAuth(user, password))

	client := getHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		xl.Warnf("Certificate download request failed for terminusName=%v, url=%s: %v", name, requestURL, err)
		return ret, err
	}
	defer resp.Body.Close()

	xl.Debugf("Certificate download response for terminusName=%v: status=%d, url=%s", name, resp.StatusCode, requestURL)
	if resp.StatusCode != http.StatusOK {
		xl.Warnf("Certificate download failed for terminusName=%v: status=%d, url=%s", name, resp.StatusCode, requestURL)
		return ret, errors.New(resp.Status)
	}

	bds, err := io.ReadAll(resp.Body)
	if err != nil {
		xl.Warnf("Failed to read certificate response body for terminusName=%v: %v", name, err)
		return ret, err
	}
	xl.Infof("Certificate download response body for terminusName=%v: %s", name, string(bds))

	return string(bds), nil
}

func GetTerminusNameFromSNI(input string) (string, error) {
	var user, domain string
	if net.ParseIP(input) != nil {
		return "", errors.New("is ip")
	}

	parts := strings.Split(input, ".")
	sniSplitLen := len(parts)
	switch {
	case sniSplitLen < 3:
		return "", errors.New("too short")
	case sniSplitLen == 3:
		user = parts[0]
		domain = strings.Join(parts[1:], ".")
	default:
		user = parts[1]
		domain = strings.Join(parts[2:], ".")
	}
	/*
		if sniSplitLen < 3 {
			return "", errors.New("too short")
		} else if sniSplitLen == 3 {
			user = parts[0]
			domain = strings.Join(parts[1:], ".")
		} else {
			user = parts[1]
			domain = strings.Join(parts[2:], ".")
		}
	*/

	terminusName := fmt.Sprintf("%s@%s", user, domain)

	return terminusName, nil
}

func IsExpired(endDate string) (bool, error) {
	xl := xlog.New()
	parsedTime, err := time.Parse(time.RFC3339, endDate)
	if err != nil {
		xl.Errorf("Failed to parse certificate endDate=%v: %v", endDate, err)
		return false, err
	}

	currentTime := time.Now().UTC()
	advanced := currentTime.AddDate(0, 0, 7)

	if parsedTime.Before(advanced) {
		xl.Debugf("Certificate expired: endDate=%v, currentTime=%v, threshold=%v (currentTime + 7 days)", endDate, currentTime, advanced)
		return true, nil
	}
	xl.Debugf("Certificate valid: endDate=%v, currentTime=%v, threshold=%v", endDate, currentTime, advanced)
	return false, nil
}

/*
func checkDid(did string) bool {
	didLen := len(did)
	if didLen < 1 || didLen > 63 {
		return false
	}
	for i, c := range did {
		if (i == 0 || (i == len(did)-1)) && c == '-' {
			return false
		}
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	if strings.Contains(did, "--") {
		return false
	}

	return true
}

func checkDomain(domain string) bool {
	string_slice := strings.Split(domain, ".")
	if len(string_slice) < 2 {
		return false
	}
	for _, label := range string_slice {
		if !checkDid(label) {
			return false
		}
	}

	return true
}
*/

func GetCert(name string) (Cert, error) {
	xl := xlog.New()
	var cert Cert
	name, err := GetTerminusNameFromSNI(name)
	if err != nil {
		return cert, err
	}

	// Check cache first
	if c, ok := GetCertFromCache(name); ok {
		isExpired, err := IsExpired(c.EndDate)
		if err == nil && !isExpired {
			xl.Debugf("Using cached certificate for terminusName=%v, endDate=%v", name, c.EndDate)
			return c, nil
		}
		if isExpired {
			xl.Infof("Cached certificate expired for terminusName=%v, endDate=%v, will download new one", name, c.EndDate)
		} else {
			xl.Warnf("Error checking certificate expiration for terminusName=%v, endDate=%v: %v", name, c.EndDate, err)
		}
	}

	// Use singleflight to prevent concurrent downloads of the same certificate
	result, err, _ := downloadGroup.Do(name, func() (interface{}, error) {
		// Double-check cache after acquiring lock (another goroutine might have downloaded it)
		if c, ok := GetCertFromCache(name); ok {
			isExpired, err := IsExpired(c.EndDate)
			if err == nil && !isExpired {
				return c, nil
			}
		}

		respBody, err := GetCertRequest(name, helper.Cfg.CertDownload.User, helper.Cfg.CertDownload.Password, helper.Cfg.CertDownload.URL)
		if err != nil {
			xl.Warnf("Failed to download certificate for terminusName=%v: %v", name, err)
			return nil, err
		}

		var response Response
		err = json.Unmarshal([]byte(respBody), &response)
		if err != nil {
			xl.Warnf("Failed to unmarshal certificate response for terminusName=%v, responseBody length=%d: %v", name, len(respBody), err)
			return nil, err
		}

		if response.Success {
			AddCertToCache(name, response.Data)
			xl.Infof("Successfully downloaded and cached certificate for terminusName=%v, zone=%v, endDate=%v", name, response.Data.Zone, response.Data.EndDate)
			return response.Data, nil
		}

		xl.Warnf("Certificate download returned error for terminusName=%v: %v", name, response.Message)
		return nil, errors.New(response.Message)
	})

	if err != nil {
		return cert, err
	}

	return result.(Cert), nil
}
