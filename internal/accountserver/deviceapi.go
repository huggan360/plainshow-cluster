package accountserver

// The account-facing half of the API: your machines, and invitations addressed
// to you. Everything here is scoped to the signed-in account — there is no
// administrator path through these handlers.

import (
	"errors"
	"net/http"
	"strings"
)

func (s *Server) myDevices(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	devices, err := s.store.DevicesForAccount(account.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *Server) setDeviceNetwork(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var body struct {
		NetworkID string `json:"network_id"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.store.SetDeviceNetwork(account.ID, r.PathValue("id"), strings.TrimSpace(body.NetworkID))
	switch {
	case errors.Is(err, ErrDeviceNotFound):
		fail(w, http.StatusNotFound, "No such device on this account.")
	case errors.Is(err, ErrNetworkMember):
		fail(w, http.StatusForbidden, "You are not a member of that network.")
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
	default:
		s.watchers.notify(account.ID, TopicDevices)
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "requested", "network_id": body.NetworkID,
		})
	}
}

func (s *Server) signOutDevice(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	err := s.store.RequestDeviceSignOut(account.ID, r.PathValue("id"))
	if errors.Is(err, ErrDeviceNotFound) {
		fail(w, http.StatusNotFound, "No such device on this account.")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.watchers.notify(account.ID, TopicDevices)
	writeJSON(w, http.StatusOK, map[string]string{"status": "requested"})
}

func (s *Server) removeDevice(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	err := s.store.RemoveDevice(account.ID, r.PathValue("id"))
	if errors.Is(err, ErrDeviceNotFound) {
		fail(w, http.StatusNotFound, "No such device on this account.")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.watchers.notify(account.ID, TopicDevices)
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// ------------------------------------------------------------ invitations --

func (s *Server) searchAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.store.SearchAccounts(r.URL.Query().Get("q"), 10)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

func (s *Server) createInvitation(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var body struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body.Role) == "" {
		body.Role = "member"
	}
	invitation, err := s.store.CreateInvitation(account.ID, r.PathValue("id"),
		body.Username, body.Role)
	switch {
	case errors.Is(err, ErrInviteNotAllowed):
		fail(w, http.StatusForbidden, "You cannot invite people to that network.")
	case errors.Is(err, ErrAlreadyMember):
		fail(w, http.StatusConflict, "That account is already a member of this network.")
	case errors.Is(err, ErrNotFound):
		fail(w, http.StatusNotFound, "No Plainshow account has that username.")
	case err != nil:
		fail(w, http.StatusBadRequest, err.Error())
	default:
		s.watchers.notify(invitation.AccountID, TopicInvitations)
		writeJSON(w, http.StatusCreated, invitation)
	}
}

func (s *Server) myInvitations(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	invitations, err := s.store.InvitationsForAccount(account.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": invitations})
}

func (s *Server) networkInvitations(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	invitations, err := s.store.InvitationsForNetwork(account.ID, r.PathValue("id"))
	if errors.Is(err, ErrInviteNotAllowed) {
		fail(w, http.StatusForbidden, "You are not a member of that network.")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": invitations})
}

func (s *Server) respondToInvitation(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	accept := strings.HasSuffix(r.URL.Path, "/accept")
	invitation, err := s.store.RespondToInvitation(account.ID, r.PathValue("id"), accept)
	switch {
	case errors.Is(err, ErrNotFound):
		fail(w, http.StatusNotFound, "That invitation is not addressed to you.")
	case errors.Is(err, ErrInviteSettled):
		fail(w, http.StatusConflict, "That invitation has already been answered.")
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
	default:
		// Both ends: the answer changes what the invitee can reach, and it is
		// what whoever asked has been waiting to see.
		s.watchers.notify(account.ID, TopicInvitations)
		s.watchers.notify(invitation.InvitedBy, TopicInvitations)
		writeJSON(w, http.StatusOK, invitation)
	}
}

func (s *Server) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	invitee, err := s.store.RevokeInvitation(account.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, ErrNotFound):
		fail(w, http.StatusNotFound, "No such invitation.")
	case errors.Is(err, ErrInviteNotAllowed):
		fail(w, http.StatusForbidden, "You cannot change invitations for that network.")
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
	default:
		s.watchers.notify(invitee, TopicInvitations)
		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
	}
}
