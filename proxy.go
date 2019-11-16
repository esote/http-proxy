// Package proxy provides an interface to start reverse proxies.
package proxy

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Route describes the reverse proxy route: redirecting from From to To.
type Route struct {
	// From must be a path or hostname+path, such as "/index", or
	// "abc.example.com/", or "abc.example.com/xyz/".
	From string `json:"from"`

	// To is an HTTP URL including the protocol scheme.
	To string `json:"to"`
}

// ReverseProxy describes a reverse proxy server.
type ReverseProxy struct {
	// Cert and Key are optional. If specified, the reverse proxy will use
	// HTTPS.
	Cert string `json:"cert"`
	Key  string `json:"key"`

	// Port, in the form ":port" such as ":8080".
	Port string `json:"port"`

	Routes []Route `json:"routes"`

	// TLSConfig is ignored when parsing JSON. Manually define a
	// ReverseProxy variable to use it.
	TLSConfig *tls.Config `json:"-"`

	// Stop is ignored with parsing JSON. Sending "true" along the channel
	// will gracefully shutdown the proxy server.
	Stop <-chan bool `json:"-"`

	// StopTimeout is ignored when parsing JSON. If the stop channel is
	// used, StopTimeout determines how long to wait for graceful shutdown
	// before forceful shutdown. -1 indicates to wait forever.
	StopTimeout time.Duration `json:"-"`
}

// Proxies describes a list of reverse proxies.
type Proxies struct {
	Proxies []ReverseProxy `json:"proxies"`
}

var active sync.WaitGroup

// Proxy starts a list of reverse proxies. Errors are passed along an error
// channel. When all proxies die the error channel is closed. This should only
// be called once or until all previous proxies die.
func Proxy(p *Proxies) <-chan error {
	errs := make(chan error)

	// If Proxy has been called before, wait for existing proxies to die.
	active.Wait()
	active.Add(len(p.Proxies))

	for _, proxy := range p.Proxies {
		go listenAndServe(proxy, errs)
	}

	go func() {
		active.Wait()
		close(errs)
	}()

	return errs
}

// From golang src/net/http/httputil/reverseproxy.go:singleJoiningSlash()
func join(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")

	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}

	return a + b
}

func listenAndServe(r ReverseProxy, errs chan error) {
	mux := http.NewServeMux()

	for _, route := range r.Routes {
		to, err := url.Parse(route.To)

		if err != nil {
			errs <- err
			return
		}

		raw := to.RawQuery

		// Custom director to change Host header
		director := func(req *http.Request) {
			req.Host = to.Host
			req.Header.Set("Host", to.Host)

			// From director func in NewSingleHostReverseProxy
			req.URL.Scheme = to.Scheme
			req.URL.Host = to.Host
			req.URL.Path = join(to.Path, req.URL.Path)

			if raw == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = raw + req.URL.RawQuery
			} else {
				req.URL.RawQuery = raw + "&" + req.URL.RawQuery
			}

			if _, ok := req.Header["User-Agent"]; !ok {
				req.Header.Set("User-Agent", "")
			}
		}

		proxy := &httputil.ReverseProxy{Director: director}
		mux.Handle(route.From, proxy)
	}

	srv := &http.Server{
		Addr:    r.Port,
		Handler: mux,
	}

	var err error

	go func(stop <-chan bool, timeout time.Duration) {
		if stop == nil {
			return
		}
		for {
			select {
			case s, ok := <-stop:
				if !ok {
					return
				}
				if !s {
					continue
				}
				ctx := context.Background()
				if timeout != time.Duration(-1) {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, timeout)
					defer cancel()
				}
				_ = srv.Shutdown(ctx)
				return
			}
		}
	}(r.Stop, r.StopTimeout)

	if r.Key == "" {
		err = srv.ListenAndServe()
	} else {
		srv.TLSConfig = r.TLSConfig
		err = srv.ListenAndServeTLS(r.Cert, r.Key)
	}

	if err != nil && err != http.ErrServerClosed {
		errs <- err
	}

	active.Done()
}
