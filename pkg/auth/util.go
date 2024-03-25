package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"
)

func SendRequest(requestURL string, requestData []byte) ([]byte, error) {
	bodyReader := bytes.NewReader(requestData)
	req, err := http.NewRequest(http.MethodPost, requestURL, bodyReader)
	if err != nil {
		log.Printf("client: could not create request: %s\n", err)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	log.Printf("%+v", req)

	client := http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("client: error making http request: %s\n", err)
		return nil, err
	}

	log.Printf("%+v\n", resp)

	defer resp.Body.Close()

	bds, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	log.Println(string(bds))
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
	log.Println("jws: ", jwsVerifyURL, jws, user)
	vr := VerifyRequest{
		Jws: jws,
	}
	reqBytes, err := json.Marshal(vr)
	if err != nil {
		log.Println(err)
		return false, err
	}
	requestURL := jwsVerifyURL
	log.Println(requestURL)
	respBytes, err := SendRequest(requestURL, reqBytes)
	if err != nil {
		log.Println(err)
		return false, err
	}
	var resp VerifyResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		errMsg := "unmarshalling json"
		log.Println(errMsg)
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
