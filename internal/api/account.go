package api

// Devices and invitations, proxied through this node.
//
// The browser talks only to the node it is looking at. It never holds the
// account credential and never calls clusteradmin directly: that keeps one
// origin, one session, and one place where "is this account allowed to do that"
// is decided — the account service, which is the only thing that knows.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/accountclient"
	"github.com/huggan360/plainshow-cluster/internal/config"
)

// errNoAuthority means this node has no account service to ask.
var errNoAuthority = errors.New(
	"This node is not signed in to a Plainshow account, so it has nothing to ask about devices or invitations.")

// authority returns a client and the credential to use with it.
func (s *Server) authority(ctx context.Context) (*accountclient.Client, string, error) {
	if !s.usesCentralAccounts() {
		return nil, "", errNoAuthority
	}
	token, err := config.LoadAccountToken(s.layout)
	if err != nil || token == "" {
		return nil, "", errNoAuthority
	}
	client, err := accountclient.New(s.cfg.Account.Server)
	if err != nil {
		return nil, "", err
	}
	return client, token, nil
}

// withAuthority runs one call against the account service and renders whatever
// went wrong in the words the interface should show.
func (s *Server) withAuthority(w http.ResponseWriter, r *http.Request,
	do func(ctx context.Context, client *accountclient.Client, token string) (any, error)) {

	client, token, err := s.authority(r.Context())
	if err != nil {
		fail(w, http.StatusPreconditionRequired, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	result, err := do(ctx, client, token)
	if err != nil {
		// The account service already phrases its refusals for a person, so
		// they are passed through rather than replaced with a generic failure.
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ----------------------------------------------------------------- devices --

func (s *Server) getDevices(w http.ResponseWriter, r *http.Request) {
	s.withAuthority(w, r, func(ctx context.Context, client *accountclient.Client, token string) (any, error) {
		devices, err := client.Devices(ctx, token)
		if err != nil {
			return nil, err
		}
		// The node knows one thing the account service does not: which of these
		// machines is the one you are looking at.
		for i := range devices {
			if devices[i].ID == s.cfg.Node.ID {
				devices[i].Online = true
			} else if online, known := s.observedDevice(devices[i].ID); known {
				devices[i].Online = online
			}
		}
		return map[string]any{"devices": devices, "this_device": s.cfg.Node.ID}, nil
	})
}

func (s *Server) putDeviceNetwork(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NetworkID string `json:"network_id"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	s.withAuthority(w, r, func(ctx context.Context, client *accountclient.Client, token string) (any, error) {
		if err := client.SetDeviceNetwork(ctx, token, id, strings.TrimSpace(body.NetworkID)); err != nil {
			return nil, err
		}
		// Asking this machine to move and then waiting a minute to do it would
		// be a needless delay: it is right here.
		if id == s.cfg.Node.ID {
			go s.checkInAccountServer(context.Background())
		}
		return map[string]string{"status": "requested"}, nil
	})
}

func (s *Server) signOutDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.withAuthority(w, r, func(ctx context.Context, client *accountclient.Client, token string) (any, error) {
		if err := client.SignOutDevice(ctx, token, id); err != nil {
			return nil, err
		}
		if id == s.cfg.Node.ID {
			// Signing out the machine you are sitting at should not wait for a
			// heartbeat that needs the credential it is about to destroy.
			s.signOutThisMachine()
		}
		return map[string]string{"status": "signed out"}, nil
	})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.withAuthority(w, r, func(ctx context.Context, client *accountclient.Client, token string) (any, error) {
		if err := client.RemoveDevice(ctx, token, id); err != nil {
			return nil, err
		}
		return map[string]string{"status": "removed"}, nil
	})
}

// ------------------------------------------------------------- invitations --

func (s *Server) searchAccounts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	s.withAuthority(w, r, func(ctx context.Context, client *accountclient.Client, token string) (any, error) {
		accounts, err := client.SearchAccounts(ctx, token, query)
		if err != nil {
			return nil, err
		}
		return map[string]any{"accounts": accounts}, nil
	})
}

func (s *Server) getInvitations(w http.ResponseWriter, r *http.Request) {
	s.withAuthority(w, r, func(ctx context.Context, client *accountclient.Client, token string) (any, error) {
		invitations, err := client.Invitations(ctx, token)
		if err != nil {
			return nil, err
		}
		return map[string]any{"invitations": invitations}, nil
	})
}

func (s *Server) getNetworkInvitations(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.withAuthority(w, r, func(ctx context.Context, client *accountclient.Client, token string) (any, error) {
		invitations, err := client.NetworkInvitations(ctx, token, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"invitations": invitations}, nil
	})
}

func (s *Server) createNetworkInvitation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	s.withAuthority(w, r, func(ctx context.Context, client *accountclient.Client, token string) (any, error) {
		return client.CreateInvitation(ctx, token, id,
			strings.TrimSpace(body.Username), strings.TrimSpace(body.Role))
	})
}

func (s *Server) respondToInvitation(w http.ResponseWriter, r *http.Request) {
	accept := strings.HasSuffix(r.URL.Path, "/accept")
	id := r.PathValue("id")
	s.withAuthority(w, r, func(ctx context.Context, client *accountclient.Client, token string) (any, error) {
		invitation, err := client.RespondToInvitation(ctx, token, id, accept)
		if err != nil {
			return nil, err
		}
		if !accept {
			return invitation, nil
		}
		// Accepting creates the membership row on the account service. Adoption
		// is what turns that into a network this machine can use, and doing it
		// here rather than on the next heartbeat is the difference between
		// "accepted" and "accepted, and there it is".
		adopted, _ := s.AdoptAccountNetworks(ctx, client, token)
		return map[string]any{"invitation": invitation, "adopted": adopted}, nil
	})
}

func (s *Server) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.withAuthority(w, r, func(ctx context.Context, client *accountclient.Client, token string) (any, error) {
		if err := client.RevokeInvitation(ctx, token, id); err != nil {
			return nil, err
		}
		return map[string]string{"status": "revoked"}, nil
	})
}
