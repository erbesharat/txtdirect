package types

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"go.txtdirect.org/txtdirect/config"
	"go.txtdirect.org/txtdirect/plugins/prometheus"
	"go.txtdirect.org/txtdirect/record"
	"go.txtdirect.org/txtdirect/variables"
)

// Host keeps data for "host" type requests
type Host struct {
	rw  http.ResponseWriter
	req *http.Request
	c   config.Config
	rec record.Record
}

// NewHost returns a fresh instance of Host struct
func NewHost(w http.ResponseWriter, r *http.Request, rec record.Record, c config.Config) *Host {
	return &Host{
		rw:  w,
		req: r,
		rec: rec,
		c:   c,
	}
}

// Redirect redirects the request to the endpoint defined in the record
func (h *Host) Redirect() error {
	to, code, err := record.GetBaseTarget(h.rec, h.req)
	if err != nil {
		log.Print("Fallback is triggered because an error has occurred: ", err)
		record.Fallback(h.rw, h.req, "to", code, h.c)
		return nil
	}
	log.Printf("[txtdirect]: %s > %s", h.req.Host+h.req.URL.Path, to)
	if code == http.StatusMovedPermanently {
		h.rw.Header().Add("Cache-Control", fmt.Sprintf("max-age=%d", variables.Status301CacheAge))
	}
	h.rw.Header().Add("Status-Code", strconv.Itoa(code))
	http.Redirect(h.rw, h.req, to, code)
	if h.c.Prometheus.Enable {
		prometheus.RequestsByStatus.WithLabelValues(h.req.Host, strconv.Itoa(code)).Add(1)
	}
	return nil
}