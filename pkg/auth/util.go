package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/fatedier/frp/pkg/util/xlog"
)

func SendRequest(requestURL string, requestData []byte) ([]byte, error) {
	xl := xlog.New()
	bodyReader := bytes.NewReader(requestData)
	req, err := http.NewRequest(http.MethodPost, requestURL, bodyReader)
	if err != nil {
		xl.Infof("client: could not create request: %s", err)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	xl.Infof("%+v", req)

	client := http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		xl.Infof("client: error making http request: %s", err)
		return nil, err
	}

	xl.Infof("%+v", resp)

	defer resp.Body.Close()

	bds, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	xl.Infof(string(bds))
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return bds, nil
	}

	return nil, errors.New(resp.Status)
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
	xl := xlog.New()
	xl.Infof(jwsVerifyURL, jws, user)
	vr := VerifyRequest{
		Jws: jws,
	}
	reqBytes, err := json.Marshal(vr)
	if err != nil {
		xl.Warnf("%v", err)
		return false, err
	}
	requestURL := jwsVerifyURL
	xl.Infof(requestURL)
	respBytes, err := SendRequest(requestURL, reqBytes)
	if err != nil {
		xl.Warnf("%v", err)
		return false, err
	}
	var resp VerifyResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		errMsg := "unmarshalling json"
		xl.Warnf(errMsg)
		return false, err
	}

	if !resp.Verify {
		errMsg := "verify false"
		return false, errors.New(errMsg)
	}

	if resp.Payload.Name != user {
		errMsg := "signer not match"
		return false, errors.New(errMsg)
	}

	return true, nil
}
