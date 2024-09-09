package vhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	//	"encoding/base64"

	httppkg "github.com/fatedier/frp/pkg/util/http"
	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/frp/server/helper"
	"github.com/hashicorp/go-retryablehttp"
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

var certs map[string]Cert = make(map[string]Cert)

func GetCertRequest(name, user, password, theurl string) (string, error) {
	var ret string

	bodyReader := bytes.NewReader([]byte{})
	requestUrl := theurl + "/download?name="
	requestUrl += url.QueryEscape(name)
	log.Println(http.MethodGet, requestUrl)

	req, err := retryablehttp.NewRequest(http.MethodGet, requestUrl, bodyReader)
	if err != nil {
		log.Printf("client: could not create request: %s\n", err)
		return ret, err
	}
	/*
		auth := fmt.Sprintf("%s:%s", user, password)
		encodedAuth := base64.StdEncoding.EncodeToString([]byte(auth))
		req.Header.Set("Authorization", "Basic " + encodedAuth)
	*/
	req.Header.Set("Authorization", httppkg.BasicAuth(user, password))

	client := retryablehttp.NewClient()
	client.HTTPClient.Timeout = 15 * time.Second
	client.RetryMax = 1
	client.RetryWaitMin = 1 * time.Second
	client.RetryWaitMax = 10 * time.Second
	client.RequestLogHook = func(l retryablehttp.Logger, r *http.Request, attemptNum int) {
		if attemptNum != 0 {
			// l.Printf("Request: %s %s (attempt %d)", r.Method, r.URL, attemptNum)
			log.Printf("RequestLogHook: %s %s (attempt %d)", r.Method, r.URL, attemptNum)
			//			SendFeishu(fmt.Sprintf("retry -> %s", r.URL))
		}
	}

	client.ResponseLogHook = func(l retryablehttp.Logger, resp *http.Response) {
		if resp.StatusCode != http.StatusOK {
			// l.Printf("Response: %d", resp.StatusCode)
			log.Printf("ResponseLogHook: %+v", resp)
			//			SendFeishu(fmt.Sprintf("status: %s -> %s", resp.Status, resp.Request.URL))
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("client: error making http request: %s\n", err)
		return ret, err
	}
	log.Printf("%+v", resp)
	if resp.StatusCode != http.StatusOK {
		//		SendFeishu(resp.Status + " -> " + requestUrl)
		return ret, errors.New(resp.Status)
	}
	defer resp.Body.Close()

	bds, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return ret, err
	}
	log.Println(string(bds))

	return string(bds), nil
}

func GetTerminusNameFromSNI(input string) (string, error) {
	parts := strings.Split(input, ".")
	sniSplitLen := len(parts)
	if sniSplitLen < 3 {
		return "", errors.New("too short!")
	}

	return parts[sniSplitLen-3] + "@" + parts[sniSplitLen-2] + "." + parts[sniSplitLen-1], nil
}

func IsExpired(endDate string) (bool, error) {
	xl := xlog.New()
	parsedTime, err := time.Parse(time.RFC3339, endDate)
	if err != nil {
		xl.Errorf("Error parsing date:", err)
		return false, err
	}

	currentTime := time.Now().UTC()
	advanced := currentTime.AddDate(0, 0, 7)

	if parsedTime.Before(advanced) {
		xl.Infof("The end date is before the current time + 7.")
		return true, nil
	} else {
		xl.Infof("The end date is not before the current time + 7.")
		return false, nil
	}
}

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

func GetCert(name string) (Cert, error) {
	xl := xlog.New()
	var cert Cert
	name, err := GetTerminusNameFromSNI(name)
	if err != nil {
		return cert, err
	}

	if c, ok := certs[name]; ok {
		isExpired, err := IsExpired(c.EndDate)
		if err == nil && !isExpired {
			return c, nil
		}
		xl.Warnf("is expired: %v err: %v", isExpired, err)
	}

	respBody, err := GetCertRequest(name, helper.Cfg.CertDownload.User, helper.Cfg.CertDownload.Password, helper.Cfg.CertDownload.Url)
	if err != nil {
		return cert, err
	}

	xl.Infof("%v %v", name, respBody)

	var response Response
	err = json.Unmarshal([]byte(respBody), &response)
	if err != nil {
		xl.Warnf("Error: %v", err)
		return cert, err
	}

	if response.Success {
		certs[name] = response.Data
		return response.Data, nil
	}

	return cert, errors.New(response.Message)

}
