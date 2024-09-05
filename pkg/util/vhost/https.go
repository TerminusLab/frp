// Copyright 2016 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package vhost

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"errors"
	"time"
//	"io/ioutil"

	libnet "github.com/fatedier/golib/net"
)

type HTTPSMuxer struct {
	*Muxer
}

func NewHTTPSMuxer(listener net.Listener, timeout time.Duration) (*HTTPSMuxer, error) {
	mux, err := NewMuxer(listener, GetHTTPSHostname, timeout)
	mux.SetFailHookFunc(vhostFailed)
	mux.SetFailHookSNIFunc(vhostSNIFailed)
	if err != nil {
		return nil, err
	}
	return &HTTPSMuxer{mux}, err
}

func GetHTTPSHostname(c net.Conn) (_ net.Conn, _ map[string]string, err error) {
	reqInfoMap := make(map[string]string, 0)
	sc, rd := libnet.NewSharedConn(c)

	clientHello, err := readClientHello(rd)
	if err != nil {
		return nil, reqInfoMap, err
	}

	reqInfoMap["Host"] = clientHello.ServerName
	reqInfoMap["Scheme"] = "https"
	return sc, reqInfoMap, nil
}

func readClientHello(reader io.Reader) (*tls.ClientHelloInfo, error) {
	var hello *tls.ClientHelloInfo

	// Note that Handshake always fails because the readOnlyConn is not a real connection.
	// As long as the Client Hello is successfully read, the failure should only happen after GetConfigForClient is called,
	// so we only care about the error if hello was never set.
	err := tls.Server(readOnlyConn{reader: reader}, &tls.Config{
		GetConfigForClient: func(argHello *tls.ClientHelloInfo) (*tls.Config, error) {
			hello = &tls.ClientHelloInfo{}
			*hello = *argHello
			return nil, nil
		},
	}).Handshake()

	if hello == nil {
		return nil, err
	}
	return hello, nil
}

func vhostFailed(c net.Conn) {
	// Alert with alertUnrecognizedName
	_ = tls.Server(c, &tls.Config{}).Handshake()
	c.Close()
}

func GetErrorResponse(code int) (string, error) {
	var response, body string
	if code == 522 {
		response = "HTTP/1.1 522\r\n"
		body = "error code: 522"
	} else if code == 530 {
		response = "HTTP/1.1 530\r\n"
		body = "Error 1033"
	} else {
		return "", errors.New(fmt.Sprintf("not support code %v", code))
	}

	loc, err := time.LoadLocation("GMT")
	if err != nil {
		return "", err
	}

	response +=
		"Date: " + time.Now().In(loc).Format(time.RFC1123) + "\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n" +
		"X-Frame-Options: SAMEORIGIN\r\n" +
		"Referrer-Policy: same-origin\r\n" +
		"Cache-Control: private, max-age=0, no-store, no-cache, must-revalidate, post-check=0, pre-check=0\r\n" +
		"Expires: " + time.Unix(1, 0).In(loc).Format(time.RFC1123) + "\r\n" +
		"Server: frp\r\n" +
		"Connection: close\r\n" +
		"\r\n" + body

	return response, nil
}

func vhostSNIFailed(c net.Conn, sni string) {
	defer c.Close()
	fmt.Printf("sni ----------------------------> [%v]\n", sni)
	data, err := GetCert(sni)
	if err != nil {
		fmt.Println("Error Get certificates:", err)
		return
	}
	var certPEM = data.Cert
	var keyPEM = data.Key
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		fmt.Println("Error loading certificates:", err)
		//		_ = tls.Server(c, &tls.Config{}).Handshake()
		return
	}
	tlsConn := tls.Server(c, &tls.Config{
		Certificates: []tls.Certificate{cert},
	})
	defer tlsConn.Close()

	if err := tlsConn.Handshake(); err != nil {
		fmt.Println("Handshake failed:", err)
		return
	}
	/*
		buf, err := ioutil.ReadAll(tlsConn)
		if err != nil {
			fmt.Println("Error reading from connection:", err)
			return
		}

		fmt.Printf("Received data: %s\n", buf)
	*/
	httpCode := 530
	response, err := GetErrorResponse(httpCode)
	if err != nil {
		fmt.Println("GetErrorResponse", err)
	}

	fmt.Println(response)

	_, err = tlsConn.Write([]byte(response))
	if err != nil {
		fmt.Println("Error writing response:", err)
		return
	}

	fmt.Println(sni, "Sent %v response", httpCode)
}

type readOnlyConn struct {
	reader io.Reader
}

func (conn readOnlyConn) Read(p []byte) (int, error)         { return conn.reader.Read(p) }
func (conn readOnlyConn) Write(_ []byte) (int, error)        { return 0, io.ErrClosedPipe }
func (conn readOnlyConn) Close() error                       { return nil }
func (conn readOnlyConn) LocalAddr() net.Addr                { return nil }
func (conn readOnlyConn) RemoteAddr() net.Addr               { return nil }
func (conn readOnlyConn) SetDeadline(_ time.Time) error      { return nil }
func (conn readOnlyConn) SetReadDeadline(_ time.Time) error  { return nil }
func (conn readOnlyConn) SetWriteDeadline(_ time.Time) error { return nil }
