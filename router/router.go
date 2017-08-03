package router

import (
	"errors"
	"fmt"
	stdlog "log"
	"os"
	"sync"
	"time"

	"github.com/gammazero/nexus/logger"
	"github.com/gammazero/nexus/wamp"
)

// log is an instance of a logger that implements the logger.Logger interface.
// A stdlib logger is assigned by default (for convenience), but this can be
// reassigned, using SetLogger(), to use any other logging package.
var log logger.Logger = stdlog.New(os.Stdout, "", stdlog.LstdFlags)

// SetLogger assigns a logger instance to the router package.  Use this to
// assign an instance of anything that that implements the logger.Logger
// interface, before using the router package.
func SetLogger(logger logger.Logger) { log = logger }

// Log returns the logger that the router package is set to use.
func Logger() logger.Logger { return log }

// Enable debug logging for router package.
var DebugEnabled bool

const helloTimeout = 5 * time.Second

// A Router handles new Peers and routes requests to the requested Realm.
type Router interface {
	// AddRealm creates a new Realm and adds that to the router.
	AddRealm(*RealmConfig) (*realm, error)

	// Attach connects a client to the router and to the requested realm.
	Attach(wamp.Peer) error

	// Close stops the router and waits message processing to stop.
	Close()
}

// DefaultRouter is the default WAMP router implementation.
type router struct {
	realms map[wamp.URI]*realm

	actionChan chan func()
	waitRealms sync.WaitGroup

	autoRealmTemplate *RealmConfig
	strictURI         bool
	closed            bool
}

// NewRouter creates a WAMP router.
//
// If authRealm is true, realms that do not exist are automatically created on
// client HELLO.  Caution, enabling this allows unauthenticated clients to
// create new realms.
//
// The strictURI parameter enabled strict URI validation.
func NewRouter(autoRealmTemplate *RealmConfig, strictURI bool) Router {
	r := &router{
		realms:     map[wamp.URI]*realm{},
		actionChan: make(chan func()),

		autoRealmTemplate: autoRealmTemplate,
		strictURI:         strictURI,
	}
	go r.run()
	return r
}

// Single goroutine used to safely access router data.
func (r *router) run() {
	for action := range r.actionChan {
		action()
	}
}

// AddRealm creates a new Realm and adds that to the router.
//
// At least one realm is needed, unless automatic realm creation is enabled.
func (r *router) AddRealm(config *RealmConfig) (*realm, error) {
	if !config.URI.ValidURI(r.strictURI, "") {
		return nil, fmt.Errorf(
			"invalid realm URI %v (URI strict checking %v)", config.URI, r.strictURI)
	}
	var realm *realm
	sync := make(chan error)
	r.actionChan <- func() {
		if r.closed {
			sync <- errors.New("router closed")
			return
		}
		if _, ok := r.realms[config.URI]; ok {
			sync <- errors.New("realm already exists: " + string(config.URI))
			return
		}
		realm = NewRealm(config)
		r.realms[config.URI] = realm
		sync <- nil
	}
	err := <-sync
	if err != nil {
		return nil, fmt.Errorf("error adding realm: %v", err)
	}

	r.waitRealms.Add(1)
	go func() {
		realm.run()
		r.waitRealms.Done()
	}()

	log.Print("Added realm: ", config.URI)
	return realm, nil
}

// Attach connects a client to the router and to the requested realm.
func (r *router) Attach(client wamp.Peer) error {
	sendAbort := func(reason wamp.URI, abortErr error) {
		abortMsg := wamp.Abort{Reason: reason}
		if abortErr != nil {
			abortMsg.Details = map[string]interface{}{"error": abortErr.Error()}
			log.Print("Aborting client connection: ", abortErr)
		}
		client.Send(&abortMsg)
		client.Close()
	}

	// Receive HELLO message from the client.
	msg, err := wamp.RecvTimeout(client, helloTimeout)
	if err != nil {
		return errors.New("did not receive HELLO: " + err.Error())
	}
	if DebugEnabled {
		log.Printf("New client sent: %s: %+v", msg.MessageType(), msg)
	}

	// A WAMP session is initiated by the Client sending a HELLO message to the
	// Router.  The HELLO message MUST be the very first message sent by the
	// Client after the transport has been established.
	hello, ok := msg.(*wamp.Hello)
	if !ok {
		// Note: This URI is not official and there is no requirement to send
		// an error back to the client in this case.  Seems helpful to at least
		// let the client know what was wrong.
		err = fmt.Errorf("protocol error: expected HELLO, received %s",
			msg.MessageType())
		sendAbort(wamp.URI("wamp.exception.protocol_violation"), err)
		return err
	}

	// Client is required to provide a non-empty realm.
	if string(hello.Realm) == "" {
		err = errors.New("no realm requested")
		sendAbort(wamp.ErrNoSuchRealm, err)
		return err
	}
	// Lookup or create realm to attach to.
	var realm *realm
	sync := make(chan error)
	r.actionChan <- func() {
		if r.closed {
			sendAbort(wamp.ErrSystemShutdown, nil)
			sync <- errors.New("router is closing, not accepting new clients")
			return
		}
		// Realm is a string identifying the realm this session should attach
		// to.  Check if the requested realm exists.
		var ok bool
		realm, ok = r.realms[hello.Realm]
		if !ok {
			// If the router is not configured to automatically create the
			// realm, then respond with an ABORT message.
			if r.autoRealmTemplate == nil {
				sendAbort(wamp.ErrNoSuchRealm, nil)
				sync <- fmt.Errorf("no realm \"%s\" exists on this router",
					string(hello.Realm))
				return
			}
			// Create the new realm that allows anonymous authentication and
			// allows disclosing caller ID.
			config := r.autoRealmTemplate
			config.URI = hello.Realm
			config.StrictURI = r.strictURI
			realm = NewRealm(config)
			r.realms[hello.Realm] = realm
			log.Print("Auto-added realm: ", hello.Realm)
		}
		sync <- nil
	}
	err = <-sync
	if err != nil {
		return err
	}

	hello.Details = wamp.NormalizeDict(hello.Details)

	// A Client must announce the roles it supports via
	// Hello.Details.roles|dict, where the keys can be: publisher, subscriber,
	// caller, callee.  If the client announces any roles, to list specific
	// features for the role, then check that the role is something this router
	// recognizes.
	_roleVals, err := wamp.DictValue(hello.Details, []string{"roles"})
	if err != nil {
		err = errors.New("no client roles specified")
		sendAbort(wamp.ErrNoSuchRole, err)
		return err
	}
	roleVals, ok := _roleVals.(map[string]interface{})
	if !ok || len(roleVals) == 0 {
		err = errors.New("no client roles specified")
		sendAbort(wamp.ErrNoSuchRole, err)
		return err
	}
	for roleName, _ := range roleVals {
		switch roleName {
		case "publisher", "subscriber", "caller", "callee":
		default:
			err = errors.New("invalid client role specified: " + roleName)
			sendAbort(wamp.ErrNoSuchRole, err)
			return err
		}
	}

	// The default authentication method is "WAMP-Anonymous" if client does not
	// specify otherwise.
	if _, ok = hello.Details["authmethods"]; !ok {
		if hello.Details == nil {
			hello.Details = map[string]interface{}{}
		}
		hello.Details["authmethods"] = []string{"anonymous"}
	}

	// Handle any necessary client auth.  This results in either a WELCOME
	// message or an error.
	//
	// Authentication may take some some.
	welcome, err := realm.authClient(client, hello.Details)
	if err != nil {
		sendAbort(wamp.ErrAuthenticationFailed, err)
		return errors.New("authentication error: " + err.Error())
	}

	// Fill in the values of the welcome message and send to client.
	welcome.ID = wamp.GlobalID()

	// Populate session details.
	details := map[string]interface{}{}
	details["realm"] = hello.Realm
	details["roles"] = welcome.Details["roles"]
	details["authid"] = welcome.Details["authid"]
	details["authrole"] = welcome.Details["authrole"]
	details["authmethod"] = welcome.Details["authmethod"]
	details["authprovider"] = welcome.Details["authprovider"]

	// Create new session.
	sess := &Session{
		Peer:    client,
		ID:      welcome.ID,
		Details: details,
		stop:    make(chan wamp.URI, 1),
	}

	if err := realm.handleSession(sess); err != nil {
		// N.B. assume, for now, that any error is a shutdown error
		sendAbort(wamp.ErrSystemShutdown, nil)
		return err
	}

	client.Send(welcome)
	log.Print("Created session: ", welcome.ID)
	return nil
}

// Close stops the router and waits message processing to stop.
func (r *router) Close() {
	sync := make(chan struct{})
	r.actionChan <- func() {
		// Prevent new or attachment to existing realms.
		r.closed = true
		// Close all existing realms.
		for uri, realm := range r.realms {
			realm.close()
			// Delete the realm
			delete(r.realms, uri)
		}
		sync <- struct{}{}
	}
	<-sync
	// Wait for all existing realms to close.
	r.waitRealms.Wait()
}
