package wamp

import (
	"fmt"
	"sync"
)

// Session is an active WAMP session.  It associates a session ID and details
// with a connected Peer, which is the remote side of the session.  So, if the
// session owned by the router, then the Peer is the connected client.
type Session struct {
	// Interface for communicating with connected peer.
	Peer
	// Unique session ID.
	ID ID
	// Details about session.
	Details Dict

	// Roles and features supported by peer.
	roles map[string]map[string]struct{}

	mu      sync.Mutex
	done    chan struct{}
	goodbye *Goodbye
}

var (
	// NoGoodbye indicates that no Goodbye message was sent out
	NoGoodbye = &Goodbye{}
	// closedchan is a reusable closed channel.
	closedchan = make(chan struct{})
)

func init() {
	close(closedchan)
}

func NewSession(peer Peer, id ID, details Dict, greetDetails Dict) *Session {
	s := &Session{
		Peer:    peer,
		ID:      id,
		Details: details,
	}
	s.setRoles(greetDetails)
	return s
}

func (s *Session) SafeSession() *Session {
	return &Session{
		ID:      s.ID,
		Details: s.Details,
		roles:   s.roles,
	}
}

// setRoles extracts the specified roles from HELLO or WELCOME details, and
// configures the session with the roles and features for each role.
func (s *Session) setRoles(details Dict) {
	_roles, ok := details["roles"]
	if !ok {
		s.roles = nil // no roles
		return
	}
	roles, ok := AsDict(_roles)
	if !ok || len(roles) == 0 {
		s.roles = nil // no roles
		return
	}

	roleMap := make(map[string]map[string]struct{})
	for role, _roleDict := range roles {
		roleMap[role] = nil
		roleDict, ok := _roleDict.(Dict)
		if !ok {
			roleDict = NormalizeDict(_roleDict)
			if roleDict == nil {
				continue
			}
		}
		_features, ok := roleDict["features"]
		if !ok {
			continue
		}
		features, ok := _features.(Dict)
		if !ok {
			features = NormalizeDict(_features)
			if features == nil {
				continue
			}
		}
		featMap := make(map[string]struct{})
		for feature, iface := range features {
			if b, _ := iface.(bool); !b {
				continue
			}
			featMap[feature] = struct{}{}
		}
		roleMap[role] = featMap
	}
	s.roles = roleMap
}

func (s *Session) Lock()   { s.mu.Lock() }
func (s *Session) Unlock() { s.mu.Unlock() }

// String returns the session ID as a string.
func (s *Session) String() string { return fmt.Sprintf("%d", s.ID) }

// HasRole returns true if the session supports the specified role.
func (s *Session) HasRole(role string) bool {
	_, ok := s.roles[role]
	return ok
}

// HasFeature returns true if the session has the specified feature for the
// specified role.
func (s *Session) HasFeature(role, feature string) bool {
	features, ok := s.roles[role]
	if !ok {
		return false
	}
	_, ok = features[feature]
	return ok
}

func (s *Session) Done() <-chan struct{} {
	s.mu.Lock()
	if s.done == nil {
		s.done = make(chan struct{})
	}
	d := s.done
	s.mu.Unlock()
	return d
}

func (s *Session) Goodbye() *Goodbye {
	s.mu.Lock()
	g := s.goodbye
	s.mu.Unlock()
	return g
}

func (s *Session) End(goodbye *Goodbye) bool {
	s.mu.Lock()
	if s.goodbye != nil {
		s.mu.Unlock()
		return false // already ended
	}

	if goodbye == nil {
		s.goodbye = NoGoodbye
	} else {
		s.goodbye = goodbye
	}

	if s.done == nil {
		s.done = closedchan
	} else {
		close(s.done)
	}
	s.mu.Unlock()
	return true
}
