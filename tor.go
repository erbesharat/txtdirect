package txtdirect

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cretz/bine/tor"
	"github.com/mholt/caddy"
	cproxy "github.com/mholt/caddy/caddyhttp/proxy"
	"golang.org/x/net/proxy"
)

// DefaultOnionServicePort is the port used to serve the onion service on
const DefaultOnionServicePort = 4242

// Tor type config struct
type Tor struct {
	Enable bool
	Port   int
	To     string

	instance        *tor.Tor
	contextCanceler context.CancelFunc
	onion           *tor.OnionService
}

func (t *Tor) Start(c *caddy.Controller) {
	torInstance, err := tor.Start(nil, &tor.StartConf{
		NoAutoSocksPort: true,
		ExtraArgs:       []string{"--SocksPort", strconv.Itoa(t.Port)},
	})
	if err != nil {
		log.Panicf("Unable to start Tor: %v", err)
	}

	listenCtx, _ := context.WithTimeout(context.Background(), 3*time.Minute)

	onion, err := torInstance.Listen(listenCtx, &tor.ListenConf{LocalPort: 8868, RemotePorts: []int{80}})
	if err != nil {
		log.Panicf("Unable to start onion service: %v", err)
	}
	t.onion = onion

	t.instance = torInstance
}

// Stop stops the tor instance, context listener and the onion service
func (t *Tor) Stop() error {
	if err := t.instance.Close(); err != nil {
		return fmt.Errorf("[txtdirect]: Couldn't close the tor instance. %s", err.Error())
	}
	t.onion.Close()
	return nil
}

// Proxy redirects the request to the local onion serivce and the actual proxying
// happens inside onion service's http handler
func (t *Tor) Proxy(w http.ResponseWriter, r *http.Request, rec record, c Config) error {
	u, err := url.Parse(fmt.Sprintf("http://%s", rec.To))
	if err != nil {
		return err
	}

	// Create a socks5 dialer
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", t.Port), nil, proxy.Direct)
	if err != nil {
		log.Fatal(err)
	}

	reverseProxy := cproxy.NewSingleHostReverseProxy(u, "", proxyKeepalive, proxyTimeout, fallbackDelay)
	reverseProxy.Transport = &http.Transport{
		Dial: dialer.Dial,
	}

	if err := reverseProxy.ServeHTTP(w, r, nil); err != nil {
		return fmt.Errorf("[txtdirect]: Coudln't proxy the request to the background onion service. %s", err.Error())
	}

	return nil
}

// ParseTor parses the txtdirect config for Tor proxy
func (t *Tor) ParseTor(c *caddy.Controller) error {
	switch c.Val() {
	case "port":
		value, err := strconv.Atoi(c.RemainingArgs()[0])
		if err != nil {
			return fmt.Errorf("The given value for port field is not standard. It should an integer")
		}
		t.Port = value

	default:
		return c.ArgErr() // unhandled option for tor
	}
	return nil
}

// SetDefaults sets the default values for prometheus config
// if the fields are empty
func (t *Tor) SetDefaults() {
	if t.Port == 0 {
		t.Port = DefaultOnionServicePort
	}
}
