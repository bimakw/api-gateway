package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/bimakw/api-gateway/config"
)

type ReverseProxy struct {
	services map[string]*serviceProxy
}

type serviceProxy struct {
	config config.ServiceConfig
	proxy  *httputil.ReverseProxy
}

func New(services []config.ServiceConfig) (*ReverseProxy, error) {
	rp := &ReverseProxy{
		services: make(map[string]*serviceProxy),
	}

	for _, svc := range services {
		targetURL, err := url.Parse(svc.TargetURL)
		if err != nil {
			return nil, err
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		// Customize the director to handle path manipulation
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)

			if svc.StripPath {
				req.URL.Path = strings.TrimPrefix(req.URL.Path, svc.PathPrefix)
				if req.URL.Path == "" {
					req.URL.Path = "/"
				}
			}

			req.Host = targetURL.Host
		}

		// Custom error handler
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"error":"Service unavailable","message":"` + err.Error() + `"}`))
		}

		rp.services[svc.PathPrefix] = &serviceProxy{
			config: svc,
			proxy:  proxy,
		}
	}

	return rp, nil
}

func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Find matching service
	for prefix, svc := range rp.services {
		if strings.HasPrefix(r.URL.Path, prefix) {
			svc.proxy.ServeHTTP(w, r)
			return
		}
	}

	// No matching service found
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"error":"Not found","message":"No service matches the requested path"}`))
}

func (rp *ReverseProxy) GetServices() []config.ServiceConfig {
	services := make([]config.ServiceConfig, 0, len(rp.services))
	for _, svc := range rp.services {
		services = append(services, svc.config)
	}
	return services
}
