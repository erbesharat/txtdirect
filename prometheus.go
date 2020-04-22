/*
Copyright 2019 - The TXTDirect Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package txtdirect

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy"
	"github.com/caddyserver/caddy/caddyhttp/httpserver"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Prometheus contains Prometheus's configuration
type Prometheus struct {
	Enable        bool
	Address       string
	Path          string
	PathWhitelist []string

	next    httpserver.Handler
	handler http.Handler
}

var (
	// RequestsCount counts the total requests per host
	RequestsCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "txtdirect",
		Name:      "redirect_count_total",
		Help:      "Total requests per host",
	}, []string{"host"})

	// RequestsByStatus counts the total returned statuses per host
	RequestsByStatus = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "txtdirect",
		Name:      "redirect_status_count_total",
		Help:      "Total returned statuses per host",
	}, []string{"host", "status"})

	// RequestsCountBasedOnType counts the total requests for each host based on type
	RequestsCountBasedOnType = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "txtdirect",
		Name:      "redirect_type_count_total",
		Help:      "Total requests for each host based on type",
	}, []string{"host", "type"})

	// FallbacksCount counts the total fallbacks triggered for each type
	FallbacksCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "txtdirect",
		Name:      "fallback_type_count_total",
		Help:      "Total fallbacks triggered for each type",
	}, []string{"host", "type", "fallback"})

	// PathRedirectCount counts the total redirects per path for each host
	PathRedirectCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "txtdirect",
		Name:      "redirect_path_count_total",
		Help:      "Total redirects per path for each host",
	}, []string{"host", "path"})

	once sync.Once
)

const (
	shutdownTimeout time.Duration = time.Second * 5
	// prometheusAddr is the address the where the metrics are exported by default.
	prometheusAddr string = "localhost:9183"
	prometheusPath string = "/metrics"
)

// SetDefaults sets the default values for prometheus config
// if the fields are empty
func (p *Prometheus) SetDefaults() {
	if p.Address == "" {
		p.Address = prometheusAddr
	}
	if p.Path == "" {
		p.Path = prometheusPath
	}
}

func (p *Prometheus) start() error {
	prometheus.MustRegister(RequestsCount)
	prometheus.MustRegister(RequestsByStatus)
	prometheus.MustRegister(RequestsCountBasedOnType)
	prometheus.MustRegister(FallbacksCount)
	prometheus.MustRegister(PathRedirectCount)
	http.Handle(p.Path, p.handler)
	go func() {
		err := http.ListenAndServe(p.Address, nil)
		if err != nil {
			log.Printf("[txtdirect]: Couldn't start http handler for prometheus metrics. %s", err.Error())
		}
	}()
	return nil
}

// Setup registers the metrics on startup and creates the promethues request handler
func (p *Prometheus) Setup(c *caddy.Controller) {
	p.handler = promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
		ErrorHandling: promhttp.HTTPErrorOnError,
		ErrorLog:      log.New(os.Stderr, "", log.LstdFlags),
	})

	once.Do(func() {
		c.OnStartup(p.start)
	})

	cfg := httpserver.GetConfig(c)
	cfg.AddMiddleware(func(next httpserver.Handler) httpserver.Handler {
		p.next = next
		return p
	})
}

func (p *Prometheus) ServeHTTP(w http.ResponseWriter, r *http.Request) (int, error) {
	next := p.next

	rw := httpserver.NewResponseRecorder(w)

	status, err := next.ServeHTTP(rw, r)

	return status, err
}

// ParsePrometheus parses the txtdirect config for Prometheus
func (p *Prometheus) ParsePrometheus(c *caddy.Controller, key, value string) error {
	switch key {
	case "enable":
		value, err := strconv.ParseBool(value)
		if err != nil {
			return c.ArgErr()
		}
		p.Enable = value
	case "address":
		// TODO: validate the given address
		p.Address = value
	case "path":
		p.Path = value
	case "path_metrics_whitelist":
		p.PathWhitelist = strings.Split(value, ",")
	default:
		return c.ArgErr() // unhandled option for prometheus
	}
	return nil
}
