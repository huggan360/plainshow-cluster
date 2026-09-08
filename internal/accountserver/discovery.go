package accountserver

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
)

// Compare discovery facts, not heartbeat timestamps or job counters. Waking
// peers on every check-in would cause their own check-ins to wake each other.
type deviceDiscovery struct {
	Name, Address, PublicKey, Fingerprint, ActiveNetwork, Networks string
	Online                                                         bool
}

func (s *Store) deviceDiscovery(nodeID string) (deviceDiscovery, error) {
	var state deviceDiscovery
	var seen string
	err := s.db.QueryRow(`SELECT name,address,public_key,fingerprint,active_network,last_seen
		FROM node WHERE id=?`, nodeID).Scan(&state.Name, &state.Address,
		&state.PublicKey, &state.Fingerprint, &state.ActiveNetwork, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	state.Online = seenRecently(seen)
	networks, err := s.networksOfDevice(nodeID)
	if err != nil {
		return state, err
	}
	ids := make([]string, 0, len(networks))
	for _, network := range networks {
		ids = append(ids, network.ID)
	}
	sort.Strings(ids)
	state.Networks = strings.Join(ids, "\x00")
	return state, nil
}

func (s *Server) notifyDeviceDiscovery(accountID string, before, after deviceDiscovery) {
	if before == after {
		return
	}
	s.watchers.notify(accountID, TopicDevices)
	accounts := map[string]bool{accountID: true}
	// Notify both sides of a move/removal so former peers also refresh.
	for _, networkID := range strings.Split(before.Networks+"\x00"+after.Networks, "\x00") {
		if networkID == "" {
			continue
		}
		members, err := s.store.NetworkMembers(networkID)
		if err != nil {
			continue // The ordinary heartbeat remains a fallback.
		}
		for _, member := range members {
			if !accounts[member.AccountID] {
				accounts[member.AccountID] = true
				s.watchers.notify(member.AccountID, TopicNetworks)
			}
		}
	}
}
