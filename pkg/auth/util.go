package auth

import (
	"bytes"
	//	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"

	"github.com/fatedier/frp/pkg/util/feishu"
	"github.com/fatedier/frp/pkg/util/xlog"
)

func SendRequest(requestURL string, requestData []byte) ([]byte, error) {
	xl := xlog.New()

	retryClient := retryablehttp.NewClient()

	retryClient.RetryMax = 2
	retryClient.RetryWaitMin = 500 * time.Millisecond
	retryClient.RetryWaitMax = 3 * time.Second
	retryClient.Backoff = retryablehttp.DefaultBackoff
	retryClient.CheckRetry = retryablehttp.DefaultRetryPolicy
	retryClient.Logger = &retryableLogger{xl: xl}
	retryClient.HTTPClient.Timeout = 5 * time.Second

	/*
		retryClient.ResponseLogHook = func(logger retryablehttp.Logger, resp *http.Response) {
			if resp == nil || resp.Body == nil {
				return
			}

			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
				xl.Infof("client: intermediate response status: %s (success, body not logged)", resp.Status)
				return
			}

			bodyBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				xl.Warnf("client: failed to read intermediate error response body: %v", err)
				return
			}

			resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			xl.Infof("client: intermediate ERROR response status: %s, body: %s", resp.Status, string(bodyBytes))
		}
	*/

	retryClient.ErrorHandler = func(resp *http.Response, err error, retries int) (*http.Response, error) {
		if resp != nil && resp.Body != nil {
			bodyBytes, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				// Restore body so that the caller can read it again
				resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
				xl.Infof("client: final ERROR response status: %s, body: %s", resp.Status, string(bodyBytes))
			} else {
				xl.Warnf("client: failed to read final error response body: %v", readErr)
			}
		}
		// Return the original resp and err – this keeps the request as failed
		return resp, err
	}

	req, err := retryablehttp.NewRequest(http.MethodPost, requestURL, bytes.NewReader(requestData))
	if err != nil {
		xl.Warnf("client: could not create retryable request: %s", err)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	xl.Infof("client: sending request to %s, method: %s", req.URL.String(), req.Method)
	xl.Infof("client: request body: %s", string(requestData))

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

	return bds, errors.New(resp.Status)
}

type retryableLogger struct {
	xl *xlog.Logger
}

func (l *retryableLogger) Error(msg string, keysAndValues ...interface{}) {
	l.xl.Errorf("%s %v", msg, keysAndValues)
}

func (l *retryableLogger) Info(msg string, keysAndValues ...interface{}) {
	l.xl.Infof("%s %v", msg, keysAndValues)
}

func (l *retryableLogger) Debug(msg string, keysAndValues ...interface{}) {
	l.xl.Debugf("%s %v", msg, keysAndValues)
}

func (l *retryableLogger) Warn(msg string, keysAndValues ...interface{}) {
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
		_ = feishu.SendError(title, user+" **Marshal Error**")
		xl.Warnf("marshal error: %v", err)
		return false, err
	}

	respBytes, err := SendRequest(jwsVerifyURL, reqBytes)
	if err != nil {
		content := fmt.Sprintf("%s **JWS Verification Failed**", user)
		if respBytes != nil {
			content += fmt.Sprintf("\n**%s**", string(respBytes))
		}
		_ = feishu.SendError(title, content)
		xl.Warnf("send request error: %v", err)
		return false, err
	}

	var resp VerifyResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		_ = feishu.SendError(title, user+" **Unmarshal Error**")
		xl.Warnf("unmarshal error: %v", err)
		return false, err
	}

	if !resp.Verify {
		_ = feishu.SendError(title, user+" **Verify False**")
		return false, errors.New("verify false")
	}
	if resp.Payload.Name != user {
		_ = feishu.SendError(title, user+" **Does Not Match With JWS Signer ** "+resp.Payload.Name)
		return false, errors.New("signer not match")
	}

	return true, nil
}
