package prices_test

import (
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JustAzul/cc-msgate/internal/prices"
)

// seen records the requests a test server received.
type seen struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *seen) add(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.Clone(r.Context()))
}

func (s *seen) all() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request(nil), s.reqs...)
}

func serve(t *testing.T, h http.HandlerFunc) (*httptest.Server, *seen) {
	t.Helper()
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

func TestFetchReturnsTheBodyAndTheETag(t *testing.T) {
	srv, got := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"e1"`)
		w.Write([]byte(`{"a":1}`))
	})
	res, err := prices.Fetch(srv.URL, "", prices.FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(res.Body) != `{"a":1}` || res.ETag != `"e1"` || res.NotModified {
		t.Errorf("result = %+v", res)
	}
	reqs := got.all()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		t.Errorf("request = %s %s", r.Method, r.URL)
	}
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		t.Errorf("Accept-Encoding = %q, want gzip accepted", r.Header.Get("Accept-Encoding"))
	}
	for _, h := range []string{"Authorization", "Cookie", "If-None-Match"} {
		if r.Header.Get(h) != "" {
			t.Errorf("header %s = %q, want none", h, r.Header.Get(h))
		}
	}
}

func TestFetchDecodesAGzippedBody(t *testing.T) {
	srv, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		zw.Write([]byte(`{"zipped":true}`))
		zw.Close()
	})
	res, err := prices.Fetch(srv.URL, "", prices.FetchOptions{})
	if err != nil || string(res.Body) != `{"zipped":true}` {
		t.Errorf("body = %q, %v", res.Body, err)
	}
}

func TestFetchIsConditionalWhenAnETagIsGiven(t *testing.T) {
	srv, got := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"e1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write([]byte(`{}`))
	})
	res, err := prices.Fetch(srv.URL, `"e1"`, prices.FetchOptions{})
	if err != nil || !res.NotModified || len(res.Body) != 0 {
		t.Errorf("result = %+v, %v; want not modified", res, err)
	}
	if reqs := got.all(); len(reqs) != 1 || reqs[0].Header.Get("If-None-Match") != `"e1"` {
		t.Errorf("requests = %v, want one carrying If-None-Match", reqs)
	}
}

func TestFetchFailures(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		etag    string
		opts    prices.FetchOptions
	}{
		{"status 500", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, "", prices.FetchOptions{}},
		{"status 404", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }, "", prices.FetchOptions{}},
		{"not modified though nothing was conditional", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(304) }, "", prices.FetchOptions{}},
		{"body over the limit", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("x", 11))) }, "", prices.FetchOptions{MaxBytes: 10}},
		{"decoded body over the limit", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			zw.Write([]byte(strings.Repeat("x", 4096)))
			zw.Close()
		}, "", prices.FetchOptions{MaxBytes: 1024}},
		{"stalled server", func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) }, "", prices.FetchOptions{Timeout: 100 * time.Millisecond}},
		{"stalled body", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("{"))
			w.(http.Flusher).Flush()
			time.Sleep(2 * time.Second)
		}, "", prices.FetchOptions{Timeout: 100 * time.Millisecond}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := serve(t, c.handler)
			if res, err := prices.Fetch(srv.URL, c.etag, c.opts); err == nil {
				t.Errorf("result = %+v, want an error", res)
			}
		})
	}
}

func TestFetchBodyAtTheLimitIsAccepted(t *testing.T) {
	srv, _ := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("x", 10))) })
	if res, err := prices.Fetch(srv.URL, "", prices.FetchOptions{MaxBytes: 10}); err != nil || len(res.Body) != 10 {
		t.Errorf("body = %d bytes, %v", len(res.Body), err)
	}
}

func TestFetchRefusedConnection(t *testing.T) {
	srv, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {})
	url := srv.URL
	srv.Close()
	if _, err := prices.Fetch(url, "", prices.FetchOptions{}); err == nil {
		t.Error("want an error from a closed server")
	}
}
