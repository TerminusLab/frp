package auth

import (
	"log"
	"time"
	"bytes"
	"errors"
	"io/ioutil"
	"net/http"
	"encoding/json"
)

func SendRequest(requestUrl string, requestData []byte) ([]byte, error) {
        bodyReader := bytes.NewReader(requestData)
        req, err := http.NewRequest(http.MethodPost, requestUrl, bodyReader)
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

        bds, err := ioutil.ReadAll(resp.Body)
        if err != nil {
                return nil, err
        }
        log.Println(string(bds))
        if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
                return bds, nil
        } else {
                return nil, errors.New(resp.Status)
        }
}

type VerifyRequest struct {
        Jws string `json:"jws"`
}

type Payload struct {
        Name   string `json:"name"`
        Did    string `json:"did"`
        Url    string `json:"url"`
        Domain string `json:"domain"`
        Time   string `json:"time"`
}

type VerifyResponse struct {
        Verify  bool    `json:"verify"`
	Payload Payload `json:"payload"`
	Did     string  `json:"did"`
	Name    string  `json:"name"`
}


func Verify(jwsVerifyUrl string, jws string, user string) (bool, error) {
	log.Println("jws: ", jwsVerifyUrl, jws, user)
        vr := VerifyRequest{
                Jws: jws,
        }
        reqBytes, err := json.Marshal(vr)
        if err != nil {
                log.Println(err)
                return false, err
        }
        requestUrl := jwsVerifyUrl
        log.Println(requestUrl)
        respBytes, err := SendRequest(requestUrl, reqBytes)
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

        if resp.Verify == false {
                errMsg := "verify false"
                return false, errors.New(errMsg)
        }

	if resp.Payload.Name != user {
                errMsg := "signer not match"
                return false, errors.New(errMsg)
        }

	return true, nil
}
