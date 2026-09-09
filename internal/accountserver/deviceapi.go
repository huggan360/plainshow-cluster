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

// ------------------------------------------------------------- projects --

func (s *Server) myProjects(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	projects, err := s.store.ProjectsForAccount(account.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (s *Server) syncProject(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var input ProjectRegistration
	if err := decode(r, &input); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	project, err := s.store.RegisterProject(account.ID, input)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	// Everybody on it should see it appear rather than wait out a heartbeat.
	s.watchers.notify(account.ID, TopicProjects)
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) forgetProject(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	affected, err := s.store.ForgetProject(account.ID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, accountID := range affected {
		s.watchers.notify(accountID, TopicProjects)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "forgotten"})
}

// ------------------------------------------------------------ credentials --

// getGitHubToken hands a signed-in device the account's GitHub credential.
//
// Only over an authenticated call, and only ever to a machine already holding
// this account's bearer token — which can do everything this token can anyway.
func (s *Server) getGitHubToken(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	token, err := s.store.GitHubToken(account.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token, "connected": token != "", "login": account.GitHubLogin,
	})
}

// putGitHubToken records a credential connected on one machine so the account's
// other machines pick it up.
func (s *Server) putGitHubToken(w http.ResponseWriter, r *http.Request) {
	account, _ := s.currentAccount(r)
	var body struct {
		Token string `json:"token"`
		Login string `json:"login"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SetGitHubToken(account.ID, body.Token); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if login := strings.TrimSpace(body.Login); login != "" {
		_ = s.store.SetGitHubLogin(account.ID, login)
	}
	s.watchers.notify(account.ID, TopicCredentials)
	writeJSON(w, http.StatusOK, map[string]bool{"connected": strings.TrimSpace(body.Token) != ""})
}
