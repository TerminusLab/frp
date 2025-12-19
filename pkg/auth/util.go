package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/frp/pkg/util/feishu"
	"github.com/hashicorp/go-retryablehttp"
)

func SendRequest(requestURL string, requestData []byte) ([]byte, error) {
	xl := xlog.New()

	retryClient := retryablehttp.NewClient()

	retryClient.RetryMax = 2
	retryClient.RetryWaitMin = 500 * time.Millisecond
	retryClient.RetryWaitMax = 3 * time.Second
	retryClient.Backoff = retryablehttp.DefaultBackoff

	retryClient.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if resp != nil && resp.StatusCode == http.StatusRequestTimeout {
			return true, nil
		}
		return retryablehttp.DefaultRetryPolicy(ctx, resp, err)
	}

	retryClient.Logger = &retryableLogger{xl: xlog.New()}

	req, err := retryablehttp.NewRequest(http.MethodPost, requestURL, bytes.NewReader(requestData))
	if err != nil {
		xl.Warnf("client: could not create retryable request: %s", err)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	xl.Infof("client: sending request to %s, method: %s", req.URL.String(), req.Method)

	resp, err := retryClient.Do(req)
	if err != nil {
		xl.Warnf("client: request failed after all retries: %s", err)
		return nil, err
	}
	defer resp.Body.Close()

	bds, err := io.ReadAll(resp.Body)
	if err != nil {
		xl.Warnf("client: failed to read response body: %s", err)
		return nil, err
	}

	xl.Infof("client: response status: %s, body: %s", resp.Status, string(bds))

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return bds, nil
	}

	return nil, errors.New(resp.Status)
}

type retryableLogger struct {
	xl *xlog.Logger
}

func (l *retryableLogger) Error(msg string, keysAndValues ...interface{}) {
	//l.xl.Errorf(msg, keysAndValues...)
	l.xl.Errorf("%s %v", msg, keysAndValues)
}

func (l *retryableLogger) Info(msg string, keysAndValues ...interface{}) {
	//l.xl.Infof(msg, keysAndValues...)
	l.xl.Infof("%s %v", msg, keysAndValues)
}

func (l *retryableLogger) Debug(msg string, keysAndValues ...interface{}) {
	//l.xl.Debugf(msg, keysAndValues...)
	l.xl.Debugf("%s %v", msg, keysAndValues)
}

func (l *retryableLogger) Warn(msg string, keysAndValues ...interface{}) {
	//l.xl.Warnf(msg, keysAndValues...)
	l.xl.Warnf("%s %v", msg, keysAndValues)
}

type VerifyRequest struct {
	Jws string `json:"jws"`
}

type Payload struct {
	Name   string `json:"name"`
	Did    string `json:"did"`
	URL    string `json:"url"`
	Domain string `json:"domain"`
	Time   string `json:"time"`
}

type VerifyResponse struct {
	Verify  bool    `json:"verify"`
	Payload Payload `json:"payload"`
	Did     string  `json:"did"`
	Name    string  `json:"name"`
}

func Verify(jwsVerifyURL string, jws string, user string) (bool, error) {
	title := "JWS Verification"
	xl := xlog.New()
	xl.Infof("Verify request: url=%s, user=%s", jwsVerifyURL, user)

	vr := VerifyRequest{
		Jws: jws,
	}
	reqBytes, err := json.Marshal(vr)
	if err != nil {
		feishu.SendError(title, user + " **Marshal Error**")
		xl.Warnf("marshal error: %v", err)
		return false, err
	}

	respBytes, err := SendRequest(jwsVerifyURL, reqBytes)
	if err != nil {
		feishu.SendError(title, user + " **Failed to Send JWS Verification Request**")
		xl.Warnf("send request error: %v", err)
		return false, err
	}

	var resp VerifyResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		feishu.SendError(title, user + " **Unmarshal Error**")
		xl.Warnf("unmarshal error: %v", err)
		return false, err
	}

	if !resp.Verify {
		feishu.SendError(title, user + " **Verify False**")
		return false, errors.New("verify false")
	}
	if resp.Payload.Name != user {
		feishu.SendError(title, user + " **Does Not Match With JWS Signer ** " + resp.Payload.Name)
		return false, errors.New("signer not match")
	}

	return true, nil
}
