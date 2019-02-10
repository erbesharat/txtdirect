/*
Copyright 2017 - The TXTdirect Authors
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
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mholt/caddy/caddyhttp/proxy"
)

const (
	basezone          = "_redirect"
	defaultSub        = "www"
	defaultProtocol   = "https"
	proxyKeepalive    = 30
	fallbackDelay     = 300 * time.Millisecond
	proxyTimeout      = 30 * time.Second
	status301CacheAge = 604800
)

type record struct {
	Version string
	To      string
	Code    int
	Type    string
	Vcs     string
	Website string
	From    string
	Root    string
	Re      string
}

// Config contains the middleware's configuration
type Config struct {
	Enable     []string
	Redirect   string
	Resolver   string
	Gomods     Gomods
	Prometheus Prometheus
	Tor        Tor
}

// Parse takes a string containing the DNS TXT record and returns
// a TXTDirect record struct instance.
// It will return an error if the DNS TXT record is not standard or
// if the record type is not enabled in the TXTDirect's config.
func (r *record) Parse(str string, req *http.Request, c Config) error {
	s := strings.Split(str, ";")
	for _, l := range s {
		switch {
		case strings.HasPrefix(l, "code="):
			l = strings.TrimPrefix(l, "code=")
			i, err := strconv.Atoi(l)
			if err != nil {
				return fmt.Errorf("could not parse status code: %s", err)
			}
			r.Code = i

		case strings.HasPrefix(l, "from="):
			l = strings.TrimPrefix(l, "from=")
			l, err := parsePlaceholders(l, req, []string{})
			if err != nil {
				return err
			}
			r.From = l

		case strings.HasPrefix(l, "re="):
			l = strings.TrimPrefix(l, "re=")
			r.Re = l

		case strings.HasPrefix(l, "root="):
			l = strings.TrimPrefix(l, "root=")
			r.Root = l

		case strings.HasPrefix(l, "to="):
			l = strings.TrimPrefix(l, "to=")
			l, err := parsePlaceholders(l, req, []string{})
			if err != nil {
				return err
			}
			r.To = l

		case strings.HasPrefix(l, "type="):
			l = strings.TrimPrefix(l, "type=")
			r.Type = l

		case strings.HasPrefix(l, "v="):
			l = strings.TrimPrefix(l, "v=")
			r.Version = l
			if r.Version != "txtv0" {
				return fmt.Errorf("unhandled version '%s'", r.Version)
			}
			log.Print("WARN: txtv0 is not suitable for production")

		case strings.HasPrefix(l, "vcs="):
			l = strings.TrimPrefix(l, "vcs=")
			r.Vcs = l

		case strings.HasPrefix(l, "website="):
			l = strings.TrimPrefix(l, "website=")
			r.Website = l

		default:
			tuple := strings.Split(l, "=")
			if len(tuple) != 2 {
				return fmt.Errorf("arbitrary data not allowed")
			}
			continue
		}
		if len(l) > 255 {
			return fmt.Errorf("TXT record cannot exceed the maximum of 255 characters")
		}
		if r.Type == "dockerv2" && r.To == "" {
			return fmt.Errorf("[txtdirect]: to= field is required in dockerv2 type")
		}
	}

	if r.Code == 0 {
		r.Code = http.StatusFound
	}

	if r.Type == "" {
		r.Type = "host"
	}

	if !contains(c.Enable, r.Type) {
		return fmt.Errorf("%s type is not enabled in configuration", r.Type)
	}

	return nil
}

// getBaseTarget parses the placeholder in the given record's To= field
// and returns the final address and http status code
func getBaseTarget(rec record, r *http.Request) (string, int, error) {
	if strings.ContainsAny(rec.To, "{}") {
		to, err := parsePlaceholders(rec.To, r, []string{})
		if err != nil {
			return "", 0, err
		}
		rec.To = to
	}
	return rec.To, rec.Code, nil
}

// contains checks the given slice to see if an item exists
// in that slice or not
func contains(array []string, word string) bool {
	for _, w := range array {
		if w == word {
			return true
		}
	}
	return false
}

// getRecord uses the given host to find a TXT record
// and then parses the txt record and returns a TXTDirect record
// struct instance. It returns an error when it can't find any txt
// records or if the TXT record is not standard.
func getRecord(host string, ctx context.Context, c Config, r *http.Request) (record, error) {
	txts, err := query(host, ctx, c)
	if err != nil {
		log.Printf("Initial DNS query failed: %s", err)
	}
	// if error present or record empty, jump into wildcards
	if err != nil || txts[0] == "" {
		hostSlice := strings.Split(host, ".")
		hostSlice[0] = "_"
		host = strings.Join(hostSlice, ".")
		txts, err = query(host, ctx, c)
		if err != nil {
			log.Printf("Wildcard DNS query failed: %s", err.Error())
			return record{}, err
		}
	}

	if len(txts) != 1 {
		return record{}, fmt.Errorf("could not parse TXT record with %d records", len(txts))
	}

	rec := record{}
	if err = rec.Parse(txts[0], r, c); err != nil {
		return rec, fmt.Errorf("could not parse record: %s", err)
	}

	return rec, nil
}

// fallback redirects the request to the given fallback address
// and if it's not provided it will check txtdirect config for
// default fallback address
func fallback(w http.ResponseWriter, r *http.Request, fallback string, code int, c Config) {
	if fallback != "" {
		log.Printf("[txtdirect]: %s > %s", r.Host+r.URL.Path, fallback)
		if code == http.StatusMovedPermanently {
			w.Header().Add("Cache-Control", fmt.Sprintf("max-age=%d", status301CacheAge))
		}
		http.Redirect(w, r, fallback, code)
		if c.Prometheus.Enable {
			RequestsByStatus.WithLabelValues(r.URL.Host, string(code)).Add(1)
		}
	} else if c.Redirect != "" {
		if contains(c.Enable, "www") {
			log.Printf("[txtdirect]: %s > %s", r.Host+r.URL.Path, c.Redirect)
			w.Header().Set("Content-Type", "")
			http.Redirect(w, r, c.Redirect, http.StatusForbidden)
			if c.Prometheus.Enable {
				RequestsByStatus.WithLabelValues(r.URL.Host, string(http.StatusForbidden)).Add(1)
			}
		}
	} else {
		http.NotFound(w, r)
	}
}

// customResolver returns a net.Resolver instance based
// on the given txtdirect config to use a custom DNS resolver.
func customResolver(c Config) net.Resolver {
	return net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, network, c.Resolver)
		},
	}
}

// query checks the given zone using net.LookupTXT to
// find TXT records in that zone
func query(zone string, ctx context.Context, c Config) ([]string, error) {
	// Removes port from zone
	if strings.Contains(zone, ":") {
		zoneSlice := strings.Split(zone, ":")
		zone = zoneSlice[0]
	}

	if !strings.HasPrefix(zone, basezone) {
		zone = strings.Join([]string{basezone, zone}, ".")
	}

	// Use absolute zone
	var absoluteZone string
	if strings.HasSuffix(zone, ".") {
		absoluteZone = zone
	} else {
		absoluteZone = strings.Join([]string{zone, "."}, "")
	}

	var txts []string
	var err error
	if c.Resolver != "" {
		net := customResolver(c)
		txts, err = net.LookupTXT(ctx, absoluteZone)
	} else {
		txts, err = net.LookupTXT(absoluteZone)
	}
	if err != nil {
		return nil, fmt.Errorf("could not get TXT record: %s", err)
	}
	return txts, nil
}

// Redirect the request depending on the redirect record found
func Redirect(w http.ResponseWriter, r *http.Request, c Config) error {
	host := r.Host
	path := r.URL.Path
	if c.Prometheus.Enable {
		RequestsCount.WithLabelValues(host).Add(1)
	}
	bl := make(map[string]bool)
	bl["/favicon.ico"] = true

	if bl[path] {
		redirect := strings.Join([]string{host, path}, "")
		log.Printf("[txtdirect]: %s > %s", r.Host+r.URL.Path, redirect)
		// Empty Content-Type to prevent http.Redirect from writing an html response body
		w.Header().Set("Content-Type", "")
		http.Redirect(w, r, redirect, http.StatusNotFound)
		if c.Prometheus.Enable {
			RequestsByStatus.WithLabelValues(host, string(http.StatusOK)).Add(1)
		}
		return nil
	}

	rec, err := getRecord(host, r.Context(), c, r)
	if err != nil {
		if strings.HasSuffix(err.Error(), "no such host") {
			if c.Redirect != "" {
				log.Printf("[txtdirect]: %s > %s", r.Host+r.URL.Path, c.Redirect)
				w.Header().Add("Cache-Control", fmt.Sprintf("max-age=%d", status301CacheAge))
				http.Redirect(w, r, c.Redirect, http.StatusMovedPermanently)
				if c.Prometheus.Enable {
					RequestsByStatus.WithLabelValues(host, string(http.StatusMovedPermanently)).Add(1)
				}
				return nil
			}
			if contains(c.Enable, "www") {
				s := strings.Join([]string{defaultProtocol, "://", defaultSub, ".", host}, "")
				log.Printf("[txtdirect]: %s > %s", r.Host+r.URL.Path, s)
				w.Header().Add("Cache-Control", fmt.Sprintf("max-age=%d", status301CacheAge))
				http.Redirect(w, r, s, http.StatusMovedPermanently)
				if c.Prometheus.Enable {
					RequestsByStatus.WithLabelValues(host, string(http.StatusMovedPermanently)).Add(1)
				}
				return nil
			}
			http.NotFound(w, r)
			if c.Prometheus.Enable {
				RequestsByStatus.WithLabelValues(host, string(http.StatusNotFound)).Add(1)
			}
			return nil
		}
		return err
	}

	if !contains(c.Enable, rec.Type) {
		return fmt.Errorf("option disabled")
	}

	fallbackURL, code, err := getBaseTarget(rec, r)
	if err != nil {
		return err
	}

	if rec.Re != "" && rec.From != "" {
		fallback(w, r, fallbackURL, code, c)
		return nil
	}

	if rec.Type == "path" {
		if path == "/" {
			if rec.Root == "" {
				fallback(w, r, fallbackURL, code, c)
				return nil
			}
			log.Printf("[txtdirect]: %s > %s", r.Host+r.URL.Path, rec.Root)
			if rec.Code == http.StatusMovedPermanently {
				w.Header().Add("Cache-Control", fmt.Sprintf("max-age=%d", status301CacheAge))
			}
			http.Redirect(w, r, rec.Root, rec.Code)
			if c.Prometheus.Enable {
				RequestsByStatus.WithLabelValues(host, string(rec.Code)).Add(1)
			}
			return nil
		}

		if path != "" {
			zone, from, pathSlice, err := zoneFromPath(host, path, rec)
			rec, err = getFinalRecord(zone, from, r.Context(), c, r, pathSlice)
			if err != nil {
				log.Print("Fallback is triggered because an error has occurred: ", err)
				fallback(w, r, fallbackURL, code, c)
				return nil
			}
		}
	}

	if rec.Type == "proxy" {
		log.Printf("[txtdirect]: %s > %s", rec.From, rec.To)
		to, _, err := getBaseTarget(rec, r)
		if err != nil {
			log.Print("Fallback is triggered because an error has occurred: ", err)
			fallback(w, r, fallbackURL, code, c)
			return nil
		}
		u, err := url.Parse(to)
		if err != nil {
			return err
		}
		reverseProxy := proxy.NewSingleHostReverseProxy(u, "", proxyKeepalive, proxyTimeout, fallbackDelay)
		reverseProxy.ServeHTTP(w, r, nil)
		return nil
	}

	if rec.Type == "dockerv2" {
		if !strings.Contains(r.Header.Get("User-Agent"), "Docker-Client") {
			log.Println("[txtdirect]: The request is not from docker client, fallback triggered.")
			fallback(w, r, fallbackURL, code, c)
			return nil
		}
		err := redirectDockerv2(w, r, rec)
		if err != nil {
			log.Printf("[txtdirect]: couldn't redirect to the requested container: %s", err.Error())
			fallback(w, r, fallbackURL, code, c)
			return nil
		}
		return nil
	}

	if rec.Type == "host" {
		to, code, err := getBaseTarget(rec, r)
		if err != nil {
			log.Print("Fallback is triggered because an error has occurred: ", err)
			fallback(w, r, fallbackURL, code, c)
			return nil
		}
		log.Printf("[txtdirect]: %s > %s", r.Host+r.URL.Path, to)
		if code == http.StatusMovedPermanently {
			w.Header().Add("Cache-Control", fmt.Sprintf("max-age=%d", status301CacheAge))
		}
		http.Redirect(w, r, to, code)
		if c.Prometheus.Enable {
			RequestsByStatus.WithLabelValues(host, string(code)).Add(1)
		}
		return nil
	}

	if rec.Type == "gometa" {
		return gometa(w, rec, host)
	}

	if rec.Type == "gomods" {
		return gomods(w, r, path, c)
	}

	if rec.Type == "torproxy" {
		c.Tor.To = rec.To
		return c.Tor.Proxy(w, r, rec, c)
	}

	return fmt.Errorf("record type %s unsupported", rec.Type)
}
