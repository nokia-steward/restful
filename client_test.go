// Copyright 2021-2024 Nokia
// Licensed under the BSD 3-Clause License.
// SPDX-License-Identifier: BSD-3-Clause

package restful

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
)

type strType struct {
	Str string `json:"str,omitempty"`
}

type innerStruct struct {
	String    string            `json:"string,omitempty"`
	Ints      []int             `json:"ints,omitzero"`
	IntsOrNo  []int             `json:"intsOrNo,omitempty"`
	IntsOrNil []int             `json:"intsOrNil"` // no omitempty
	Bytes     []byte            `json:"bytes,omitempty"`
	Map       map[string]string `json:"map,omitempty"`
	Number    int               `json:"number,omitzero"`
}

type structType struct {
	Str    string       `json:"str,omitempty"`
	Struct *innerStruct `json:"struct,omitempty"`
}

func TestMethods(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a strType
		err := GetRequestData(r, 0, &a)
		assert.Nil(err)

		// Answer
		w.Header().Set(ContentTypeHeader, ContentTypeApplicationJSON)
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"str":"b"}`))
		case http.MethodHead:
			w.Header().Set("LastModified", "1970-01-01T00:00:00")
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			assert.Equal("b", a.Str)
			w.Header().Set("Location", "/users/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"str":"b"}`))
		default: // PUT / PATCH
			assert.Equal("b", a.Str)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"str":"b"}`))
		}
	}))
	defer srv.Close()

	reqData := strType{Str: "b"}
	respData := strType{}
	ctx := context.Background()
	client := NewClient().Root(srv.URL).HTTPS(&HTTPSConfig{AllowLocalhostHTTP: true})
	location, err := client.Post(ctx, "/users", &reqData, &respData)
	assert.Nil(err)
	locationStr := location.String()
	assert.Equal(srv.URL+"/users/1", locationStr)
	assert.EqualValues(reqData, respData)

	_, err = Post(ctx, locationStr, &reqData, &respData)
	assert.Nil(err)
	assert.EqualValues(reqData, respData)

	_, err = client.Put(ctx, locationStr, &reqData, &respData)
	assert.Nil(err)
	assert.EqualValues(reqData, respData)
	_, err = Put(ctx, locationStr, &reqData, &respData)
	assert.Nil(err)
	assert.EqualValues(reqData, respData)

	err = client.Patch(ctx, locationStr, &reqData, &respData)
	assert.Nil(err)
	assert.EqualValues(reqData, respData)
	err = Patch(ctx, locationStr, &reqData, &respData)
	assert.Nil(err)
	assert.EqualValues(reqData, respData)

	err = client.Get(ctx, locationStr, &respData)
	assert.Nil(err)
	assert.EqualValues(reqData, respData)
	err = Get(ctx, locationStr, &respData)
	assert.Nil(err)
	assert.EqualValues(reqData, respData)

	headers, err := client.Head(ctx, locationStr)
	assert.Nil(err)
	assert.Equal("1970-01-01T00:00:00", headers["Lastmodified"][0])

	v := url.Values{}
	v.Set("Str", "b")
	_, err = client.PostForm(ctx, "/users", v, &respData)
	assert.NoError(err)
	assert.EqualValues(reqData, respData)

	v = url.Values{}
	v.Set("Str", "b")
	resp, err := client.PostFormWithFullResponse(ctx, "/users", v, nil, nil)
	assert.NoError(err)
	err = GetResponseData(resp, client.maxBytesToParse, &respData)
	assert.NoError(err)
	assert.NotNil(resp)
	assert.EqualValues(reqData, respData)

	err = client.Delete(ctx, locationStr)
	assert.Nil(err)
	err = Delete(ctx, locationStr)
	assert.Nil(err)
}

func TestSendRequest(t *testing.T) {
	// Find similar test cases in server_response_test.go
	testCases := []struct {
		in  structType
		out string
	}{
		{in: structType{}, out: `{}`},
		{in: structType{Str: "hello", Struct: nil}, out: `{"str":"hello"}`},
		{in: structType{Struct: &innerStruct{}}, out: `{"struct":{"intsOrNil":null}}`},
		{in: structType{Struct: &innerStruct{Ints: []int{}, IntsOrNo: []int{}, IntsOrNil: []int{}}}, out: `{"struct":{"ints":[],"intsOrNil":[]}}`},
		{in: structType{Struct: &innerStruct{Ints: []int{1}, IntsOrNo: []int{1}, IntsOrNil: []int{1}, Bytes: []byte{1}}}, out: `{"struct":{"ints":[1],"intsOrNo":[1],"intsOrNil":[1],"bytes":"AQ=="}}`},
	}

	for i, testCase := range testCases {
		t.Run(fmt.Sprintf("testCase %d", i), func(t *testing.T) {
			assert := assert.New(t)

			// Server
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(ContentTypeApplicationJSON, r.Header.Get(ContentTypeHeader))
				payload, err := io.ReadAll(r.Body)
				assert.NoError(err)
				assert.Equal(testCase.out, string(payload))
				w.WriteHeader(http.StatusCreated)
			}))
			defer srv.Close()

			// Client
			resp, err := NewClient().SendRequest(context.Background(), http.MethodPost, srv.URL, nil, &testCase.in)
			assert.NoError(err)
			assert.Equal(http.StatusCreated, resp.StatusCode)
			resp.Body.Close()
		})
	}
}

func TestHttpNotAllowed(t *testing.T) {
	assert := assert.New(t)
	assert.Equal(ErrNonHTTPSURL, NewClient().HTTPS(nil).Root("http://localhost").Get(context.Background(), "/", nil))
	assert.Equal(ErrNonHTTPSURL, NewClient().HTTPS(&HTTPSConfig{}).Root("http://localhost").Get(context.Background(), "/", nil))
	assert.Equal(ErrNonHTTPSURL, NewClient().HTTPS(&HTTPSConfig{AllowedHTTPHosts: []string{"remote"}}).Root("http://localhost").Get(context.Background(), "/", nil))
	assert.Equal(ErrNonHTTPSURL, NewClient().HTTPS(&HTTPSConfig{AllowLocalhostHTTP: true}).Root("http://remote").Get(context.Background(), "/", nil))
}

func urlParse(s string) *url.URL {
	url, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return url
}

func TestIsAllowedPrivateNetwork(t *testing.T) {
	assert := assert.New(t)
	{
		hc := &HTTPSConfig{AllowPrivateHTTP: true}
		assert.True(hc.isAllowed(urlParse("http://192.168.1.1")))
		assert.True(hc.isAllowed(urlParse("http://10.0.0.1")))
		assert.True(hc.isAllowed(urlParse("http://172.16.0.1")))
	}
	{
		hc := &HTTPSConfig{AllowPrivateHTTP: false}
		assert.False(hc.isAllowed(urlParse("http://192.168.1.1")))
		assert.False(hc.isAllowed(urlParse("http://10.0.0.1")))
		assert.False(hc.isAllowed(urlParse("http://172.16.0.1")))
	}
}

func TestGetTooLongAnswer(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Answer
		w.Header().Set(ContentTypeHeader, ContentTypeApplicationJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"str":"b"}`))
	}))
	defer srv.Close()

	client := NewClient().Root(srv.URL)
	client.SetMaxBytesToParse(5) // Small enough
	var empty struct{}
	err := client.Get(context.Background(), "/", &empty)
	assert.NotNil(err)
}

func TestRetry(t *testing.T) {
	assert := assert.New(t)

	// Server: Rejecting with 504 `retries` times.
	reqCount := 0
	retries := 4
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal("hello", r.Header.Get("User-Agent"))

		if r.Method == "POST" {
			body, err := io.ReadAll(r.Body)
			assert.NoError(err)
			assert.Equal(`{"str":"hello"}`, string(body))
		}

		if reqCount < retries { // r * fail
			w.WriteHeader(http.StatusGatewayTimeout) // Retry attempt may be made
			w.Write([]byte(`{"time" :"` + time.Now().String() + `"}`))
		} else {
			w.Header().Set(ContentTypeHeader, ContentTypeApplicationJSON)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"time" :"` + time.Now().String() + `"}`))
		}
		reqCount++
	}))
	defer srv.Close()

	{ // POST: Check if body is sent on all attempts.
		reqData := strType{Str: "hello"}
		respData := strType{}
		client := NewClient().Root(srv.URL).Retry(retries, 10*time.Millisecond, 0).UserAgent("hello")
		_, err := client.Post(context.Background(), "/", &reqData, &respData)
		assert.NoError(err)
		assert.Equal(http.StatusOK, GetErrStatusCode(err))
		assert.Equal(http.StatusOK, GetErrStatusCodeElse(err, 0))
		assert.Equal(retries+1, reqCount)
	}

	{ // GET: Check if non-existing body does not cause any issue.
		reqCount = 0 // reset server's counter
		respData := strType{}
		client := NewClient().Root(srv.URL).Retry(retries, 10*time.Millisecond, 0).UserAgent("hello")
		err := client.Get(context.Background(), "/", &respData)
		assert.NoError(err)
		assert.Equal(http.StatusOK, GetErrStatusCode(err))
		assert.Equal(http.StatusOK, GetErrStatusCodeElse(err, 0))
		assert.Equal(retries+1, reqCount)
	}
}

func TestRetryStatus(t *testing.T) {
	assert.False(t, retryStatus(200))
	assert.False(t, retryStatus(404))
	assert.False(t, retryStatus(429))
	assert.False(t, retryStatus(500))

	assert.True(t, retryStatus(502))
	assert.True(t, retryStatus(503))
	assert.True(t, retryStatus(504))
}

func TestRetryResp(t *testing.T) {
	assert.False(t, retryResp(&http.Response{StatusCode: 200}))

	assert.True(t, retryResp(nil))
	assert.True(t, retryResp(&http.Response{StatusCode: 502}))
}

func TestTimeout(t *testing.T) {
	assert := assert.New(t)

	delay := 10 * time.Millisecond

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
	}))
	defer srv.Close()

	{ // GET - timeout failure
		client := NewClient().Timeout(delay / 10)
		err := client.Get(context.Background(), srv.URL, nil)
		netErr, ok := err.(net.Error)
		assert.True(ok)
		assert.True(netErr.Timeout())
	}

	{ // GET - OK
		client := NewClient().Timeout(delay * 10)
		err := client.Get(context.Background(), srv.URL, nil)
		assert.NoError(err)
	}
}

func TestRetryWithServerForwarding(t *testing.T) {
	assert := assert.New(t)

	// Server the client forwards requests to
	srvCounter := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if srvCounter&1 == 0 {
			w.WriteHeader(502)
		} else {
			w.WriteHeader(200)
		}
		srvCounter++
	}))
	defer srv.Close()

	client := NewClient().Root(srv.URL).Retry(3, time.Millisecond, time.Second)
	{ // With body
		req, _ := http.NewRequest("POST", "/hello", strings.NewReader(`{"hello": "world"}`))
		req.GetBody = nil // Simulating server behavior
		resp, err := client.Do(req)
		assert.NoError(err)
		assert.Equal(200, resp.StatusCode)
	}

	{ // No body
		req, _ := http.NewRequest("DELETE", "/hello", nil)
		req.GetBody = nil // Simulating server behavior
		resp, err := client.Do(req)
		assert.NoError(err)
		assert.Equal(200, resp.StatusCode)
	}

	assert.Equal(4, srvCounter)
}

func TestMethodsError(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SendProblemResponse(w, r, http.StatusNotFound, "nope")
	}))
	defer srv.Close()

	reqData := strType{Str: "b"}
	respData := strType{}
	ctx := context.Background()
	client := NewClient().Root(srv.URL)
	client.SetMaxBytesToParse(100000)
	location, err := client.Post(ctx, "/users", &reqData, &respData)
	assert.NotNil(err)
	assert.Nil(location)
	locationStr := "/users/1"

	_, err = client.Put(ctx, locationStr, &reqData, &respData)
	assert.NotNil(err)

	err = client.Patch(ctx, locationStr, &reqData, &respData)
	assert.NotNil(err)

	err = client.Get(ctx, locationStr, &respData)
	assert.NotNil(err)

	_, err = client.Head(ctx, locationStr)
	assert.NotNil(err)

	err = client.Delete(ctx, locationStr)
	assert.NotNil(err)
}

func TestClientBasicAuth(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		assert.True(ok)
		assert.Equal("username", username)
		assert.Equal("password", password)
	}))
	defer srv.Close()

	ctx := context.Background()
	client := NewClient().Root(srv.URL).SetBasicAuth("username", "password")
	err := client.Get(ctx, "/", nil)
	assert.NoError(err)
}

func TestCookieJar(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie("token")
		assert.Equal("secret", cookie.Value)
		cookie.Value = "new secret"
		http.SetCookie(w, cookie)
	}))
	srvURL, _ := url.Parse(srv.URL)
	defer srv.Close()

	ctx := context.Background()
	jar, _ := cookiejar.New(nil)
	client := NewClient().Root(srv.URL).SetJar(jar)
	jar.SetCookies(srvURL, []*http.Cookie{{Name: "token", Value: "secret", MaxAge: 10}})
	err := client.Get(ctx, "/", nil)
	assert.NoError(err)
	assert.Equal("new secret", client.Jar().Cookies(srvURL)[0].Value)
}
func TestGetBadURL(t *testing.T) {
	assert := assert.New(t)
	respData := strType{}
	err := NewClient().Retry(3000, time.Second, 0).Get(context.Background(), ":::", &respData)
	assert.NotNil(err)
}

func TestGet500(t *testing.T) {
	assert := assert.New(t)
	const problem = `{"title":"Configuration error"}`

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acc := r.Header["Accept"]
		assert.Equal(2, len(acc)) // json and problem+json
		SendResp(w, r, NewError(nil, 500, problem), nil)
	}))
	defer srv.Close()

	c := NewClient().AcceptProblemJSON(true)
	err := c.Get(context.Background(), srv.URL, nil)
	assert.Error(err)
	assert.NotEmpty(err.Error())
	assert.Equal(problem, err.Error())
	assert.Equal(http.StatusInternalServerError, GetErrStatusCode(err))
	assert.Equal(http.StatusInternalServerError, GetErrStatusCodeElse(err, 0))
}

func TestGet500NoProblemJSON(t *testing.T) {
	assert := assert.New(t)
	const problem = `{"title":"Configuration error"}`

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acc := r.Header["Accept"]
		assert.Equal(1, len(acc)) // json only
		SendResp(w, r, NewError(nil, 500, problem), nil)
	}))
	defer srv.Close()

	c := NewClient().AcceptProblemJSON(false)
	err := c.Get(context.Background(), srv.URL, nil)
	assert.Error(err)
}

func TestGet500Details(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acc := r.Header["Accept"]
		assert.Equal(2, len(acc)) // json and problem+json
		err := NewDetailedError(nil, 500,
			ProblemDetails{
				Title:         "title",
				Detail:        "descr",
				InvalidParams: []InvalidParam{{"param1", "error text"}},
			})

		SendResp(w, r, err, nil)
	}))
	defer srv.Close()

	c := NewClient().AcceptProblemJSON(true)
	err := c.Get(context.Background(), srv.URL, nil)
	assert.Error(err)
	assert.NotEmpty(err.Error())
	assert.Equal(`{"title":"title","detail":"descr","invalidParams":[{"param":"param1","reason":"error text"}]}`, err.Error())
	assert.Equal(http.StatusInternalServerError, GetErrStatusCode(err))
	assert.Equal(http.StatusInternalServerError, GetErrStatusCodeElse(err, 0))
}

func TestSendRecv2xxBadCT(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-type", "nuku")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"str":"b"}`))
	}))
	defer srv.Close()

	var empty struct{}
	_, err := NewClient().SendRecv2xx(context.Background(), http.MethodGet, srv.URL, nil, nil, &empty) // Resp body gets parsed.
	assert.NotNil(err)
	assert.Equal(http.StatusInternalServerError, GetErrStatusCode(err)) // Fake error code
	assert.Equal(0, GetErrStatusCodeElse(err, 0))
}

func TestSendRecv2xxNoDataNoCT(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var empty struct{}
	_, err := NewClient().SendRecv2xx(context.Background(), http.MethodGet, srv.URL, nil, &empty, &empty) // Req and resp body gets parsed, as not nil.
	assert.Nil(err)
}

func TestH2CFailed(t *testing.T) {
	assert := assert.New(t)
	respData := strType{}
	client := NewH2CClient().Root("http://127.0.0.1:0")
	assert.Equal("h2c", client.Kind)
	err := client.Get(context.Background(), "/", &respData)
	assert.NotNil(err)
}

func TestCalcBackoff(t *testing.T) {
	assert := assert.New(t)
	c := NewClient().Retry(255, 1*time.Second, 0)
	assert.Equal((1<<0)*time.Second, c.calcBackoff(0))
	assert.Equal((1<<7)*time.Second, c.calcBackoff(7))
	assert.Equal((1<<7)*time.Second, c.calcBackoff(255))

	c = NewClient().Retry(255, 1*time.Second, 2*time.Second)
	assert.Equal((1<<0)*time.Second, c.calcBackoff(0))
	assert.Equal((1<<1)*time.Second, c.calcBackoff(7))
	assert.Equal((1<<1)*time.Second, c.calcBackoff(255))
}

func TestBroadcastRequest(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient().Root(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := client.BroadcastRequest(ctx, "GET", "/", nil, nil)
	assert.NoError(err)
}

func TestBroadcastRequestUnknown(t *testing.T) {
	assert := assert.New(t)
	err := NewClient().BroadcastRequest(context.Background(), "GET", "http://", nil, nil)
	assert.Error(err)
}

func TestBroadcastBadURL(t *testing.T) {
	assert := assert.New(t)
	err := NewClient().BroadcastRequest(context.Background(), "GET", ":::-1", nil, nil)
	assert.Error(err)
}

func TestNewHTTPClientSetsTimeoutAndRedirect(t *testing.T) {
	assert := assert.New(t)
	for _, c := range []*Client{NewClient(), NewH2Client(), NewH2CClient()} {
		assert.Equal(10*time.Second, c.Client.Timeout)
		assert.NotNil(c.Client.CheckRedirect)
	}
}

func TestCheckRedirect(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://TemporaryLocation")
		SendResp(w, r, NewError(nil, 307), nil)
	}))
	defer srv.Close()

	c := NewClient().CheckRedirect(func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	})

	var empty struct{}
	resp, err := c.SendRecv(context.Background(), http.MethodGet, srv.URL, nil, nil, &empty)
	assert.NoError(err)
	assert.Equal(http.StatusTemporaryRedirect, resp.StatusCode)

}

func TestHTTPSRedirectDoesNotDowngradeToHTTP(t *testing.T) {
	assert := assert.New(t)

	httpHits := 0
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer httpSrv.Close()

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, httpSrv.URL+"/secret", http.StatusFound)
	}))
	defer tlsSrv.Close()

	err := NewClient().Insecure().HTTPS(nil).Get(context.Background(), tlsSrv.URL, nil)
	assert.ErrorIs(err, ErrNonHTTPSURL)
	assert.Equal(0, httpHits)
}

func TestHTTPSRedirectToAllowedHTTPHost(t *testing.T) {
	assert := assert.New(t)

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer httpSrv.Close()

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, httpSrv.URL, http.StatusFound)
	}))
	defer tlsSrv.Close()

	httpURL, err := url.Parse(httpSrv.URL)
	assert.NoError(err)

	err = NewClient().Insecure().HTTPS(&HTTPSConfig{AllowedHTTPHosts: []string{httpURL.Hostname()}}).Get(context.Background(), tlsSrv.URL, nil)
	assert.NoError(err)

	err = NewClient().Insecure().HTTPS(&HTTPSConfig{AllowLocalhostHTTP: true}).Get(context.Background(), tlsSrv.URL, nil)
	assert.NoError(err)
}

func TestHTTPSRedirectPreservesCheckRedirect(t *testing.T) {
	assert := assert.New(t)

	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirect must not be followed when custom CheckRedirect stops it")
		w.WriteHeader(http.StatusOK)
	}))
	defer dest.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	called := 0
	check := func(req *http.Request, via []*http.Request) error {
		called++
		assert.Equal(dest.URL, strings.TrimRight(req.URL.String(), "/"))
		assert.Len(via, 1)
		return http.ErrUseLastResponse
	}

	clients := []*Client{
		NewClient().HTTPS(&HTTPSConfig{AllowLocalhostHTTP: true}).CheckRedirect(check),
		NewClient().CheckRedirect(check).HTTPS(&HTTPSConfig{AllowLocalhostHTTP: true}),
	}
	for _, c := range clients {
		called = 0
		resp, err := c.SendRecv(context.Background(), http.MethodGet, srv.URL, nil, nil, nil)
		assert.NoError(err)
		assert.Equal(1, called)
		assert.Equal(http.StatusTemporaryRedirect, resp.StatusCode)
		resp.Body.Close()
	}
}

func TestCtxCancelBefore(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	{ // GET - timeout failure
		client := NewClient()
		ctx := context.Background()
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		err := client.Get(ctx, srv.URL, nil)
		assert.True(errDeadlineOrCancel(err))
	}
}

func TestSetClientCredentialAuthDown(t *testing.T) {
	client := NewClient().HTTPS(nil).SetOauth2Conf(oauth2.Config{ClientID: "id", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: "https://0.0.0.0:1"}}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := client.Get(ctx, "https://127.0.0.1", nil)
	assert.Contains(t, err.Error(), "0.0.0.0:1")
}

func TestSetClientCredentialAuthDownAllowedTarget(t *testing.T) {
	client := NewClient().HTTPS(&HTTPSConfig{AllowedHTTPHosts: []string{"0.0.0.0"}}).SetOauth2Conf(oauth2.Config{ClientID: "id", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: "https://0.0.0.0:1"}}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := client.Get(ctx, "https://127.0.0.1", nil)
	assert.Contains(t, err.Error(), "0.0.0.0:1")
}

func TestSetClientCredentialNotAllowedTarget(t *testing.T) {
	client := NewClient().HTTPS(nil).SetOauth2Conf(oauth2.Config{ClientID: "id", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: "http://0.0.0.0:1"}}, nil)
	assert.Nil(t, client.oauth2.config)
	assert.NotNil(t, client)
}

func TestOauth2AccessTokenReqs(t *testing.T) {
	accessToken := "yourAccessToken"
	refreshToken := "yourRefreshToken"
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, r.ParseForm())
		switch strings.ToLower(r.Form.Get("grant_type")) {
		case "refreshToken":
			assert.NotEqual(t, r.Form.Get("refreshToken"), "")
			assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Basic"))
		case "password":
			assert.NotEqual(t, r.Form.Get("username"), "")
			assert.NotEqual(t, r.Form.Get("password"), "")
		default:
			assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Basic"))
		}
		w.Header().Set("Content-type", "application/json")
		w.Write([]byte(`{"access_token" : "` + accessToken + `", "expires_in": 60, "refresh_Token": "` + refreshToken + `"}`))
	}))
	defer authSrv.Close()
	ctx := context.Background()
	req, _ := http.NewRequest("GET", "http://127.0.0.1", nil)

	// Test client with invalid grant and no guessable grant from the other params
	client := NewClient().HTTPS(&HTTPSConfig{AllowedHTTPHosts: []string{"127.0.0.1"}}).SetOauth2Conf(oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: authSrv.URL}}, nil, 7)
	err := client.setOauth2Auth(ctx, req)
	assert.Error(t, err)

	// Test client with password credentials grant
	client = NewClient().HTTPS(&HTTPSConfig{AllowedHTTPHosts: []string{"127.0.0.1"}}).SetOauth2Conf(oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: authSrv.URL}}, nil, GrantPasswordCredentials)
	assert.Equal(t, len(client.oauth2.config.Scopes), 0)
	client.SetBasicAuth("user", "pass")
	err = client.setOauth2Auth(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, client.oauth2.token.AccessToken, accessToken)
	assert.Equal(t, client.oauth2.token.RefreshToken, refreshToken)
	req.Header.Del("Authorization")

	// Test client with refresh token grant
	client = NewClient().HTTPS(&HTTPSConfig{AllowedHTTPHosts: []string{"127.0.0.1"}}).SetOauth2Conf(oauth2.Config{ClientID: "id", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: authSrv.URL}, Scopes: []string{"openid", "profile"}}, nil, GrantPasswordCredentials)
	assert.Equal(t, len(client.oauth2.config.Scopes), 2)
	client.SetBasicAuth("user", "pass")
	assert.Equal(t, client.oauth2.token.RefreshToken, "")
	err = client.setOauth2Auth(ctx, req) // First try with password credentials grant without refresh_token
	assert.NoError(t, err)
	assert.Equal(t, client.oauth2.token.AccessToken, accessToken)
	assert.Equal(t, client.oauth2.token.RefreshToken, refreshToken)
	req.Header.Del("Authorization")
	client.oauth2.token.AccessToken = "" // Let's make the access token invalid before attempting a second request
	err = client.setOauth2Auth(ctx, req) // Second try with refresh_token grant with refresh_token included
	assert.NoError(t, err)
	assert.Equal(t, client.oauth2.token.AccessToken, accessToken)
	req.Header.Del("Authorization")

	// Test client with default client credentials grant
	client = NewClient().HTTPS(&HTTPSConfig{AllowedHTTPHosts: []string{"127.0.0.1"}}).SetOauth2Conf(oauth2.Config{ClientID: "id", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: authSrv.URL}, Scopes: []string{"openid", "profile"}}, nil)
	assert.Equal(t, len(client.oauth2.config.Scopes), 2)
	err = client.setOauth2Auth(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, client.oauth2.token.AccessToken, accessToken)

	// Test h2 OAuth2 client with HTTPS config
	client = NewClient().HTTPS(nil).SetOauth2Conf(oauth2.Config{ClientID: "id", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: authSrv.URL}}, nil).SetOauth2H2()
	err = client.setOauth2Auth(ctx, req)
	assert.Error(t, err) // clear text http URL not allowed.
}

func TestGetIPFromInterface(t *testing.T) {
	theUsedInterface := "eth1"
	netInterfaces = func() ([]net.Interface, error) {
		r := []net.Interface{{Name: "eth0"}, {Name: "eth1"}, {Name: "eth2"}}
		return r, nil
	}

	AddrOther := []net.Addr{&net.IPAddr{IP: net.IPv4(byte(1), byte(1), byte(1), byte(1))}}

	AddrIP := net.IPv4(byte(2), byte(2), byte(2), byte(2))
	AddrIf := []net.Addr{&net.IPNet{IP: AddrIP}}
	AddrTCP := net.TCPAddr{IP: AddrIP}

	netInterfaceAddrs = func(i *net.Interface) ([]net.Addr, error) {
		if i.Name == theUsedInterface {
			return AddrIf, nil
		}
		return AddrOther, nil
	}

	ip := GetIPFromInterface(theUsedInterface)
	theIP := ip
	assert.Equal(t, AddrTCP.String(), theIP.IPv4.String())
}

func TestGetIPFromInterfaceLocalhost(t *testing.T) {
	ip := GetIPFromInterface("lo")
	if ip.IPv4 != nil {
		assert.Equal(t, net.IP{127, 0, 0, 1}, ip.IPv4.IP)
	}
	if ip.IPv6 != nil {
		assert.Equal(t, net.IP{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, ip.IPv6.IP)
	}
}

func TestGetIPFromInterfaceNoName(t *testing.T) {
	theUsedInterface := ""
	netInterfaces = func() ([]net.Interface, error) {
		r := []net.Interface{{Name: "eth0"}, {Name: "eth1"}, {Name: "eth2"}}
		return r, nil
	}
	Addr := []net.Addr{&net.IPAddr{IP: net.IPv4(byte(1), byte(1), byte(1), byte(1))}}
	Addr2 := []net.Addr{&net.IPAddr{IP: net.IPv4(byte(2), byte(2), byte(2), byte(2))}}

	netInterfaceAddrs = func(i *net.Interface) ([]net.Addr, error) {
		if i.Name == theUsedInterface {
			return Addr2, nil
		}
		return Addr, nil
	}

	ip := GetIPFromInterface(theUsedInterface)
	assert.Nil(t, ip.IPv4)
	assert.Nil(t, ip.IPv6)
}

func TestGetIPFromInterfaceErrorAddr(t *testing.T) {
	theUsedInterface := "eth0"
	netInterfaces = func() ([]net.Interface, error) {
		r := []net.Interface{{Name: "eth0"}, {Name: "eth1"}, {Name: "eth2"}}
		return r, nil
	}

	netInterfaceAddrs = func(i *net.Interface) ([]net.Addr, error) {
		return nil, errors.New("new error")
	}

	ip := GetIPFromInterface(theUsedInterface)
	assert.Nil(t, ip.IPv4)
	assert.Nil(t, ip.IPv6)
}

func TestGetIPFromInterfaceError(t *testing.T) {
	theUsedInterface := "eth0"
	netInterfaces = func() ([]net.Interface, error) {
		return nil, errors.New("new error")
	}

	ip := GetIPFromInterface(theUsedInterface)
	assert.Nil(t, ip.IPv4)
	assert.Nil(t, ip.IPv6)
}

func TestGetIPFromInterfaceNoInt(t *testing.T) {
	theUsedInterface := "eth0"
	netInterfaces = func() ([]net.Interface, error) {
		r := []net.Interface{}
		return r, nil
	}

	ip := GetIPFromInterface(theUsedInterface)
	assert.Nil(t, ip.IPv4)
	assert.Nil(t, ip.IPv6)
}

func TestCientInterface(t *testing.T) {
	theUsedInterface := "eth2"
	netInterfaces = func() ([]net.Interface, error) {
		r := []net.Interface{{Name: "eth0"}, {Name: "eth1"}, {Name: "eth2"}}
		return r, nil
	}
	Addr := []net.Addr{&net.IPAddr{IP: net.IPv4(byte(1), byte(1), byte(1), byte(1))}}
	Addr2 := []net.Addr{&net.IPAddr{IP: net.IPv4(byte(2), byte(2), byte(2), byte(2))}}

	netInterfaceAddrs = func(i *net.Interface) ([]net.Addr, error) {
		if i.Name == theUsedInterface {
			return Addr2, nil
		}
		return Addr, nil
	}

	c := NewClientWInterface(theUsedInterface)
	assert.NotNil(t, c)
}

func startH2Server(mux *http.ServeMux, wg *sync.WaitGroup) *http.Server {
	protocols := http.Protocols{}
	protocols.SetHTTP2(true)
	server := &http.Server{
		Addr:    "localhost:8443",
		Handler: mux,
		TLSConfig: &tls.Config{
			NextProtos: []string{"h2"},
		},
		Protocols: &protocols,
	}

	go func() {
		wg.Done()
		if err := server.ListenAndServeTLS("test_certs/tls.crt", "test_certs/tls.key"); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Failed to start server: %v", err)
		}
	}()
	return server
}

func startH2CServer(mux *http.ServeMux, wg *sync.WaitGroup) *http.Server {
	protocols := http.Protocols{}
	protocols.SetUnencryptedHTTP2(true)
	server := &http.Server{
		Addr:      "localhost:8440",
		Handler:   mux,
		Protocols: &protocols,
	}

	go func() {
		wg.Done()
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Failed to start server: %v", err)
		}
	}()
	return server
}

func TestClients(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			http.Error(w, "expected HTTP/2 from the start, got "+r.Proto, http.StatusHTTPVersionNotSupported)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		response := map[string]string{"message": "Hello, world!"}
		json.NewEncoder(w).Encode(response)
	})
	var wg sync.WaitGroup
	wg.Add(2)
	h2Server := startH2Server(mux, &wg)
	h2cServer := startH2CServer(mux, &wg)
	defer func() {
		h2Server.Close()
		h2cServer.Close()
	}()

	h2Client := NewH2Client().Insecure()
	h2cClient := NewH2CClient()

	wg.Wait()
	time.Sleep(100 * time.Millisecond) // wg does not ensure that servers are ready, only that the listening may be started.

	tests := []struct {
		name      string
		client    *Client
		serverURL string
	}{
		{
			name:      "HTTP/2 Client (H2)",
			client:    h2Client,
			serverURL: "https://localhost:8443", // H2 server
		},
		{
			name:      "HTTP/2 Cleartext Client (H2C)",
			client:    h2cClient,
			serverURL: "http://localhost:8440", // H2C server
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			var resp any
			err := test.client.Get(context.Background(), test.serverURL, &resp)
			if err != nil {
				t.Fatalf("Failed to make request: %v", err)
			}

			b, _ := json.Marshal(resp)
			if string(b) != "{\"message\":\"Hello, world!\"}" {
				t.Fatalf("Unexpected response: %s", b)
			}
		})
	}
}

func TestH2TransportIsHTTP2Only(t *testing.T) {
	tests := []struct {
		name        string
		client      *Client
		transport   http.RoundTripper
		unencrypted bool
	}{
		{name: "H2Client", client: NewH2Client(), transport: NewH2Client().GetTransport(), unencrypted: false},
		{name: "H2CClient", client: NewH2CClient(), transport: NewH2CClient().GetTransport(), unencrypted: true},
		{name: "H2ClientWInterface", client: NewH2ClientWInterface("lo"), transport: NewH2ClientWInterface("lo").GetTransport(), unencrypted: false},
		{name: "H2CClientWInterface", client: NewH2CClientWInterface("lo"), transport: NewH2CClientWInterface("lo").GetTransport(), unencrypted: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.NotNil(t, test.transport)
			tr, ok := (test.transport).(*http.Transport)
			assert.True(t, ok)
			assert.NotNil(t, tr.Protocols)
			assert.False(t, tr.Protocols.HTTP1(), "HTTP/1 should not be supported")
			if test.unencrypted {
				assert.True(t, tr.Protocols.UnencryptedHTTP2(), "Unencrypted HTTP/2 should be supported")
			} else {
				assert.True(t, tr.Protocols.HTTP2(), "HTTP/2 should be supported")
				assert.False(t, tr.Protocols.UnencryptedHTTP2(), "Unencrypted HTTP/2 should not be supported")
			}
		})
	}
}

func TestH2ClientRejectsHTTP1(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Must not be here, TLS ALPN must have failed")
	}))
	defer srv.Close()

	// Each request must fail with TLS ALPN error, as the client wants h2 while the server does not support that.
	err := NewH2Client().Insecure().Get(context.Background(), srv.URL, nil)
	assert.Error(t, err)

	err = NewH2ClientWInterface("lo").Insecure().Get(context.Background(), srv.URL, nil)
	assert.Error(t, err)
}

func TestH2CClientRejectsHTTP1(t *testing.T) {
	seen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, 2, r.ProtoMajor)
		assert.NotEqual(t, "h2c", r.Header.Get("Upgrade"), "prior-knowledge h2c must not send HTTP/1.1 Upgrade")
		assert.Equal(t, "PRI", r.Method, "HTTP/1.1 servers may see the HTTP/2 connection preface, not an upgraded GET")
		seen = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Each request must fail with TLS ALPN error, as the client wants h2c while the server does not support that.
	err := NewH2CClient().Get(context.Background(), srv.URL, nil)
	assert.Error(t, err)
	assert.True(t, seen)

	seen = false
	err = NewH2CClientWInterface("lo").Get(context.Background(), srv.URL, nil)
	assert.Error(t, err)
	assert.True(t, seen)
}

func insecureOauth2H2TokenTransport(c *Client) {
	tr, ok := c.oauth2.client.Transport.(*http.Transport)
	if !ok {
		return
	}
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- httptest certificate
}

func TestOauth2H2AccessToken(t *testing.T) {
	authSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, 2, r.ProtoMajor)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"h2-token","expires_in":60,"token_type":"Bearer"}`))
	}))
	authSrv.EnableHTTP2 = true
	authSrv.StartTLS()
	defer authSrv.Close()

	client := NewClient().HTTPS(nil).SetOauth2Conf(oauth2.Config{
		ClientID:     "id",
		ClientSecret: "secret",
		Endpoint:     oauth2.Endpoint{TokenURL: authSrv.URL},
	}, nil).SetOauth2H2()
	insecureOauth2H2TokenTransport(client)

	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	err := client.setOauth2Auth(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "h2-token", client.oauth2.token.AccessToken)
}

func TestOauth2H2RejectsHTTP1TokenServer(t *testing.T) {
	authSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"h1-token","expires_in":60,"token_type":"Bearer"}`))
	}))
	defer authSrv.Close()

	client := NewClient().HTTPS(nil).SetOauth2Conf(oauth2.Config{
		ClientID:     "id",
		ClientSecret: "secret",
		Endpoint:     oauth2.Endpoint{TokenURL: authSrv.URL},
	}, nil).SetOauth2H2()
	insecureOauth2H2TokenTransport(client)

	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	err := client.setOauth2Auth(context.Background(), req)
	assert.Error(t, err)
	assert.Empty(t, client.oauth2.token.AccessToken)
}

func TestEnableLoadBalanceRandom(t *testing.T) {
	client := NewClient()
	assert.False(t, client.LoadBalanceRandom, "LoadBalanceRandom should be false by default")

	client.EnableLoadBalanceRandom(true)
	assert.True(t, client.LoadBalanceRandom, "LoadBalanceRandom should be true after calling EnableLoadBalanceRandom")
}
func TestSetLoadBalanceTarget_NoLoadBalance(t *testing.T) {
	client := NewClient()
	req, _ := http.NewRequest("GET", "http://example.com/resource", nil)
	target := "http://example.com/resource"
	out := client.setLoadBalanceTarget(req, target, req.URL.Hostname())
	assert.Equal(t, target, out)
}

func TestSetLoadBalanceTarget_IPAddressHost(t *testing.T) {
	client := NewClient().EnableLoadBalanceRandom(true)
	req, _ := http.NewRequest("GET", "http://127.0.0.1/resource", nil)
	target := "http://127.0.0.1/resource"
	out := client.setLoadBalanceTarget(req, target, req.URL.Hostname())
	assert.Equal(t, target, out)
}

func TestSetLoadBalanceTarget_ResolveError(t *testing.T) {
	client := NewClient().EnableLoadBalanceRandom(true)
	req, _ := http.NewRequest("GET", "http://nonexistent.invalid/resource", nil)
	target := "http://nonexistent.invalid/resource"

	// Patch net.LookupHost to simulate error
	origLookupHost := netLookupHost
	netLookupHost = func(ctx context.Context, host string) ([]string, error) {
		return nil, errors.New("lookup error")
	}
	defer func() { netLookupHost = origLookupHost }()

	out := client.setLoadBalanceTarget(req, target, req.URL.Hostname())
	assert.Equal(t, target, out)
}

func TestSetLoadBalanceTarget_SingleIP(t *testing.T) {
	client := NewClient().EnableLoadBalanceRandom(true)
	req, _ := http.NewRequest("GET", "http://example.com/resource", nil)
	target := "http://example.com/resource"

	// Patch net.LookupHost to return a single IP
	origLookupHost := netLookupHost
	netLookupHost = func(ctx context.Context, host string) ([]string, error) {
		return []string{"192.0.2.1"}, nil
	}
	defer func() { netLookupHost = origLookupHost }()

	out := client.setLoadBalanceTarget(req, target, req.URL.Hostname())
	assert.NotContains(t, out, "->")
	assert.Contains(t, req.URL.Host, "example.com")
	assert.Equal(t, "example.com", req.Host)
}

func TestSetLoadBalanceTarget_DoubleIP(t *testing.T) {
	URL := "http://example-headless.com/resource"
	client := NewClient().EnableLoadBalanceRandom(strings.Contains(URL, "headless"))
	req, _ := http.NewRequest("GET", URL, nil)
	target := "http://example.com/resource"

	// Patch net.LookupHost to return a single IP
	origLookupHost := netLookupHost
	netLookupHost = func(ctx context.Context, host string) ([]string, error) {
		return []string{"192.0.2.1", "192.0.2.2"}, nil
	}
	defer func() { netLookupHost = origLookupHost }()

	out := client.setLoadBalanceTarget(req, target, req.URL.Hostname())
	assert.Equal(t, "example-headless.com", req.URL.Host)
	assert.Equal(t, "example-headless.com", req.Host)
	assert.Regexp(t, `\[192\.0\.2\.\d+]`, out, "Expected output to contain one IP in the format '[IP]'")
}

func TestHostPortForURL(t *testing.T) {
	tests := []struct {
		host, port, want string
	}{
		{"192.0.2.1", "", "192.0.2.1"},
		{"192.0.2.1", "8080", "192.0.2.1:8080"},
		{"2001:db8::1", "", "[2001:db8::1]"},
		{"2001:db8::1", "8080", "[2001:db8::1]:8080"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, hostPortForURL(tt.host, tt.port), "host=%q port=%q", tt.host, tt.port)
	}
}

func TestSetLoadBalanceTarget_IPv6NoPort(t *testing.T) {
	client := NewClient().EnableLoadBalanceRandom(true)
	req, _ := http.NewRequest("GET", "http://example.com/resource", nil)
	target := "http://example.com/resource"

	origLookupHost := netLookupHost
	netLookupHost = func(ctx context.Context, host string) ([]string, error) {
		return []string{"2001:db8::1", "2001:db8::2"}, nil
	}
	defer func() { netLookupHost = origLookupHost }()

	out := client.setLoadBalanceTarget(req, target, req.URL.Hostname())
	assert.Equal(t, "example.com", req.URL.Host)
	assert.Equal(t, "example.com", req.Host)
	assert.Regexp(t, `\[2001:db8::\d+]`, out)
}

func TestSetLoadBalanceTarget_IPv6WithPort(t *testing.T) {
	client := NewClient().EnableLoadBalanceRandom(true)
	req, _ := http.NewRequest("GET", "http://example.com:8080/resource", nil)
	target := "http://example.com:8080/resource"

	origLookupHost := netLookupHost
	netLookupHost = func(ctx context.Context, host string) ([]string, error) {
		return []string{"2001:db8::1", "2001:db8::2"}, nil
	}
	defer func() { netLookupHost = origLookupHost }()

	out := client.setLoadBalanceTarget(req, target, req.URL.Hostname())
	assert.Equal(t, "example.com:8080", req.URL.Host)
	assert.Equal(t, "8080", req.URL.Port())
	assert.Equal(t, "example.com:8080", req.Host)
	assert.Regexp(t, `\[2001:db8::\d+]`, out)
}

func TestSetLoadBalanceTarget_IPv4WithPort(t *testing.T) {
	client := NewClient().EnableLoadBalanceRandom(true)
	req, _ := http.NewRequest("GET", "http://example.com:8080/resource", nil)
	target := "http://example.com:8080/resource"

	origLookupHost := netLookupHost
	netLookupHost = func(ctx context.Context, host string) ([]string, error) {
		return []string{"192.0.2.1", "192.0.2.2"}, nil
	}
	defer func() { netLookupHost = origLookupHost }()

	_ = client.setLoadBalanceTarget(req, target, req.URL.Hostname())
	assert.Equal(t, "example.com:8080", req.URL.Host)
	assert.Equal(t, "8080", req.URL.Port())
}
