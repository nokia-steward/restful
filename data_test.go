// Copyright 2021-2024 Nokia
// Licensed under the BSD 3-Clause License.
// SPDX-License-Identifier: BSD-3-Clause

package restful

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type abcType struct {
	A      string
	B      []string
	CField int `schema:"c-field"`
}

func TestDataGetQuery(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var abc abcType
		GetRequestData(r, 0, &abc)
		assert.Equal("a", abc.A)
		assert.Equal("b", abc.B[0])
		assert.Equal("B", abc.B[1])
		assert.Equal(42, abc.CField)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Client
	{
		req, err := http.NewRequest("GET", srv.URL, nil)
		assert.Nil(err)
		q := url.Values{"a": {"a"}, "b": {"b", "B"}, "c-field": {"42"}}
		req.URL.RawQuery = q.Encode()
		_, err = NewClient().Do(req)
		assert.Nil(err)
	}
}

func TestDataGetQueryLambda(t *testing.T) {
	assert := assert.New(t)

	// Server
	// A real listening one, so that one can make a capture.
	mux := NewRouter()
	mux.HandleFunc("/", func(ab abcType) error {
		assert.Equal("a", ab.A)
		assert.Equal("b", ab.B[0])
		assert.Equal("B", ab.B[1])
		return nil
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
	}))
	defer srv.Close()

	// Client
	{
		req, err := http.NewRequest("GET", srv.URL, nil)
		assert.NoError(err)
		q := url.Values{"a": {"a"}, "b": {"b", "B"}}
		req.URL.RawQuery = q.Encode()
		_, err = NewClient().Do(req)
		assert.NoError(err)
	}
}

func TestDataPostForm(t *testing.T) {
	assert := assert.New(t)

	// Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ab abcType
		GetRequestData(r, 0, &ab)
		assert.Equal("a", ab.A)
		assert.Equal("b", ab.B[0])
		assert.Equal("B", ab.B[1])
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Client
	{
		f := url.Values{"a": {"a"}, "b": {"b", "B"}}
		req, err := http.NewRequest("POST", srv.URL, strings.NewReader(f.Encode()))
		assert.Nil(err)
		req.Header.Set("content-type", "application/x-www-form-urlencoded")
		_, err = NewClient().Do(req)
		assert.Nil(err)
	}
}

// countingReadCloser reports how many bytes were pulled from an oversized body.
type countingReadCloser struct {
	remain int
	read   int
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	if c.remain <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > c.remain {
		n = c.remain
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	c.remain -= n
	c.read += n
	return n, nil
}

func (c *countingReadCloser) Close() error { return nil }

func TestGetResponseData_NonJSONBodyRespectsMaxBytes(t *testing.T) {
	assert := assert.New(t)
	const maxBytes = 64
	body := &countingReadCloser{remain: 1_000_000}
	resp := &http.Response{
		Header: make(http.Header),
		Body:   body,
	}
	resp.Header.Set(ContentTypeHeader, "text/plain")
	var data struct{}
	err := GetResponseData(resp, maxBytes, &data)
	assert.Error(err)
	assert.Contains(err.Error(), "not JSON")
	assert.LessOrEqual(body.read, maxBytes+1)
}

func TestGetResponseData_NonJSONBodySmall(t *testing.T) {
	assert := assert.New(t)
	resp := &http.Response{
		Header: make(http.Header),
		Body:   io.NopCloser(strings.NewReader("not json")),
	}
	resp.Header.Set(ContentTypeHeader, "text/plain")
	var data struct{}
	err := GetResponseData(resp, 64, &data)
	assert.Error(err)
	assert.Contains(err.Error(), "not JSON")
}

func TestGetResponseData_NoDataMaxBytes(t *testing.T) {
	assert := assert.New(t)
	const maxBytes = 64
	body := &countingReadCloser{remain: 1_000_000}
	resp := &http.Response{
		Header: http.Header{ContentTypeHeader: []string{ContentTypeApplicationJSON}},
		Body:   body,
	}
	err := GetResponseData(resp, maxBytes, nil)
	assert.NoError(err)
	assert.LessOrEqual(body.read, maxBytes+1) // MaxBytesReader may read one extra byte
}

func TestSendRecv_NoContentJSONContentType(t *testing.T) {
	assert := assert.New(t)

	// Go's server deletes the body and Content-Length for 204 (RFC 9110).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ContentTypeHeader, ContentTypeApplicationJSON)
		w.WriteHeader(http.StatusNoContent)
		_, _ = w.Write([]byte(`{"ignored":true}`))
	}))
	defer srv.Close()

	var responseBody json.RawMessage
	resp, err := NewClient().SendRecv(context.Background(), http.MethodPost, srv.URL, nil, nil, &responseBody)
	assert.NoError(err)
	if assert.NotNil(resp) {
		assert.Equal(http.StatusNoContent, resp.StatusCode)
		resp.Body.Close()
	}
	assert.Nil(responseBody)
}

func TestGetResponseData_StreamingZeroContentLengthDrainRespectsMaxBytes(t *testing.T) {
	assert := assert.New(t)
	const maxBytes = 64
	body := &countingReadCloser{remain: 1_000_000}
	resp := &http.Response{
		Header: http.Header{
			ContentTypeHeader: []string{ContentTypeApplicationJSON},
			"Content-Length":  []string{"0"},
		},
		Body: body,
	}
	var data struct{}
	err := GetResponseData(resp, maxBytes, &data)
	assert.NoError(err)
	assert.LessOrEqual(body.read, maxBytes+1) // MaxBytesReader may read one extra byte
}

func TestCreateLimitedReader_TooBigContentLengthRespectsMaxBytes(t *testing.T) {
	assert := assert.New(t)
	const maxBytes = 64
	body := &countingReadCloser{remain: 1_000_000}
	h := http.Header{"Content-Length": []string{"1000000"}}
	limited, err := createLimitedReader(nil, h, body, maxBytes)
	assert.Nil(limited)
	assert.Error(err)
	assert.Contains(err.Error(), "too big Content-Length")
	assert.LessOrEqual(body.read, maxBytes+1) // MaxBytesReader may read one extra byte
}

func TestGetRequestData_NonJSONBodyRespectsMaxBytes(t *testing.T) {
	assert := assert.New(t)
	const maxBytes = 64
	body := &countingReadCloser{remain: 1_000_000}
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(ContentTypeHeader, "text/plain")
	var data struct{}
	err := GetRequestData(req, maxBytes, &data)
	assert.Error(err)
	assert.Equal(http.StatusBadRequest, GetErrStatusCode(err))
	assert.Contains(err.Error(), "not JSON")
	assert.LessOrEqual(body.read, maxBytes+1)
}

func TestGetRequestData_NonJSONBodySmall(t *testing.T) {
	assert := assert.New(t)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not json"))
	req.Header.Set(ContentTypeHeader, "text/plain")
	var data struct{}
	err := GetRequestData(req, 64, &data)
	assert.Error(err)
	assert.Equal(http.StatusBadRequest, GetErrStatusCode(err))
	assert.Contains(err.Error(), "not JSON")
}

func TestGetDataBytes_WithinLimit(t *testing.T) {
	assert := assert.New(t)
	body := io.NopCloser(strings.NewReader(`{"ok":true}`))
	got, err := GetDataBytes(http.Header{}, body, 64)
	assert.NoError(err)
	assert.Equal([]byte(`{"ok":true}`), got)
}

func TestGetDataBytes_TooBigContentLengthDropsPayload(t *testing.T) {
	assert := assert.New(t)
	const maxBytes = 64
	const size = 1_000_000
	body := &countingReadCloser{remain: size}
	h := http.Header{"Content-Length": []string{"1000000"}}
	got, err := GetDataBytes(h, body, maxBytes)
	assert.Error(err)
	assert.Nil(got)
	assert.Contains(err.Error(), "content too large")
	assert.Equal(size, body.remain)
	assert.Equal(0, body.read)
}

func TestGetDataBytes_StreamingTooLongNonJSONDropsPayload(t *testing.T) {
	assert := assert.New(t)
	const maxBytes = 64
	const size = 1_000_000
	body := &countingReadCloser{remain: size}
	got, err := GetDataBytes(http.Header{}, body, maxBytes)
	assert.Error(err)
	assert.Nil(got)
	assert.Contains(err.Error(), "content too large")
	assert.Equal(maxBytes+1, body.read)
}

func TestGetResponseData_StreamingTooLongJSONDropsPayload(t *testing.T) {
	assert := assert.New(t)
	const maxBytes = 32
	payload := `{"x":"` + strings.Repeat("a", 1000) + `"}`
	body := &readCounter{Reader: strings.NewReader(payload)}
	resp := &http.Response{
		Header: make(http.Header),
		Body:   body,
	}
	resp.Header.Set(ContentTypeHeader, ContentTypeApplicationJSON)
	data := map[string]string{}
	err := GetResponseData(resp, maxBytes, &data)
	assert.Error(err)
	assert.Contains(err.Error(), "too long content")
	assert.LessOrEqual(body.read, maxBytes+1) // MaxBytesReader may read one extra byte
}

func TestGetResponseData_ChunkedStreamTooLongDropsPayload(t *testing.T) {
	assert := assert.New(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ContentTypeHeader, ContentTypeApplicationJSON)
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			_, _ = w.Write([]byte(`{"x":"`))
			f.Flush()
			_, _ = w.Write([]byte(strings.Repeat("a", 10_000)))
			_, _ = w.Write([]byte(`"}`))
			return
		}
		_, _ = w.Write([]byte(`{"x":"` + strings.Repeat("a", 10_000) + `"}`))
	}))
	defer srv.Close()

	data := map[string]string{}
	err := NewClient().Root(srv.URL).SetMaxBytesToParse(32).Get(context.Background(), "/", &data)
	assert.Error(err)
	assert.Contains(err.Error(), "too long content")
}

func TestGetResponseData_NonStreamedJSONWithinLimit(t *testing.T) {
	assert := assert.New(t)
	payload := `{"x":"ok"}`
	resp := &http.Response{
		Header: make(http.Header),
		Body:   io.NopCloser(strings.NewReader(payload)),
	}
	resp.Header.Set(ContentTypeHeader, ContentTypeApplicationJSON)
	resp.Header.Set("Content-Length", strconv.Itoa(len(payload)))
	data := map[string]string{}
	err := GetResponseData(resp, 64, &data)
	assert.NoError(err)
	assert.Equal("ok", data["x"])
}

func TestGetResponseData_StreamingJSONWithinLimit(t *testing.T) {
	assert := assert.New(t)
	payload := `{"x":"ok"}`
	resp := &http.Response{
		Header: make(http.Header),
		Body:   io.NopCloser(strings.NewReader(payload)),
	}
	resp.Header.Set(ContentTypeHeader, ContentTypeApplicationJSON)
	data := map[string]string{}
	err := GetResponseData(resp, 64, &data)
	assert.NoError(err)
	assert.Equal("ok", data["x"])
}

func TestGetResponseData_TooBigContentLengthDropsPayload(t *testing.T) {
	assert := assert.New(t)
	const maxBytes = 32
	const size = 1000
	body := &countingReadCloser{remain: size}
	resp := &http.Response{
		Header: make(http.Header),
		Body:   body,
	}
	resp.Header.Set(ContentTypeHeader, ContentTypeApplicationJSON)
	resp.Header.Set("Content-Length", "1000")
	data := map[string]string{}
	err := GetResponseData(resp, maxBytes, &data)
	assert.Error(err)
	assert.Contains(err.Error(), "too big Content-Length")
	assert.LessOrEqual(body.read, maxBytes+1) // MaxBytesReader may read one extra byte
}

type readCounter struct {
	io.Reader
	read int
}

func (c *readCounter) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.read += n
	return n, err
}

func (c *readCounter) Close() error { return nil }
