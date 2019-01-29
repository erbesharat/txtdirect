package txtdirect

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/cretz/bine/tor"
)

// Tor type config struct
type Tor struct {
	Enable bool
	Port   int

	instance        *tor.Tor
	contextCanceler context.CancelFunc
	onion           *tor.OnionService
}

func (t *Tor) Start() {
	torInstance, err := tor.Start(nil, nil)
	if err != nil {
		log.Panicf("Unable to start Tor: %v", err)
	}
	t.instance = torInstance

	listenCtx, listenCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.contextCanceler = listenCancel

	onion, err := torInstance.Listen(listenCtx, &tor.ListenConf{Version3: true, LocalPort: t.Port})
	if err != nil {
		log.Panicf("Unable to create onion service: %v", err)
	}
	t.onion = onion

	errCh := make(chan error, 1)
	// TODO: change the http listener to proxy the request instead of serving current dir
	go func() { errCh <- http.Serve(onion, http.FileServer(http.Dir("."))) }()
	// End when enter is pressed
	go func() {
		fmt.Scanln()
		errCh <- nil
	}()
	if err = <-errCh; err != nil {
		log.Panicf("Failed serving: %v", err)
	}
}

// Stop stops the tor instance, context listener and the onion service
func (t *Tor) Stop() {
	if err := t.instance.Close(); err != nil {
		log.Fatalf("[txtdirect]: Couldn't close the tor instance. %s", err.Error())
	}

	t.contextCanceler()

	if err := t.onion.Close(); err != nil {
		log.Fatalf("[txtdirect]: Couldn't stop the onion service. %s", err.Error())
	}
}
