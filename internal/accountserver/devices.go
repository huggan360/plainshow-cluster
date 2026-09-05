package accountserver

// Devices: the machines on an account, and the two things that can be done to
// one from somewhere else.
//
// Neither operation reaches into a device. Both write a row the device reads on
// its next check-in and acts on itself, which is what keeps a machine's own
// policy the last word about what happens on it.

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrDeviceNotFound means no such device belongs to this account.
var ErrDeviceNotFound = errors.New("device not found")

// deviceOnlineAfter is how recently a device must have checked in to count as
// online. Check-ins are once a minute, so this tolerates one missed beat
// without calling a working machine dead.
const deviceOnlineAfter = 3 * time.Minute

// AccountDevice is one machine as its owner sees it in the interface.
type AccountDevice struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Version        string       `json:"version"`
	OS             string       `json:"os"`
	Arch           string       `json:"arch"`
	GPUCount       int          `json:"gpu_count"`
	ProjectCount   int          `json:"project_count"`
	RunningJobs    int          `json:"running_jobs"`
	ActiveNetwork  string       `json:"active_network"`
	DesiredNetwork string       `json:"desired_network"`
	LastSeen       string       `json:"last_seen"`
	Online         bool         `json:"online"`
	Networks       []NetworkRef `json:"networks"`
}

// DevicesForAccount lists every machine signed in to this account.
func (s *Store) DevicesForAccount(accountID string) ([]AccountDevice, error) {
	rows, err := s.db.Query(`SELECT id,name,version,os,arch,gpu_count,project_count,
            running_jobs,active_network,desired_network,last_seen
        FROM node WHERE owner_account_id=? ORDER BY last_seen DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountDevice{}
	for rows.Next() {
		var device AccountDevice
		if err := rows.Scan(&device.ID, &device.Name, &device.Version, &device.OS,
			&device.Arch, &device.GPUCount, &device.ProjectCount, &device.RunningJobs,
			&device.ActiveNetwork, &device.DesiredNetwork, &device.LastSeen); err != nil {
			return nil, err
		}
		device.Online = seenRecently(device.LastSeen)
		device.Networks = []NetworkRef{}
		out = append(out, device)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range out {
		networks, err := s.networksOfDevice(out[index].ID)
		if err != nil {
			return nil, err
		}
		out[index].Networks = networks
	}
	return out, nil
}

func (s *Store) networksOfDevice(nodeID string) ([]NetworkRef, error) {
	rows, err := s.db.Query(`SELECT n.id,n.name FROM network n
        JOIN node_network d ON d.network_id=n.id WHERE d.node_id=? ORDER BY lower(n.name)`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetworkRef{}
	for rows.Next() {
		var ref NetworkRef
		if err := rows.Scan(&ref.ID, &ref.Name); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

func seenRecently(stamp string) bool {
	seen, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp))
	if err != nil {
		return false
	}
	return time.Since(seen) < deviceOnlineAfter
}

// SetDeviceNetwork asks one of this account's machines to work in a network.
//
// It records a wish, not a fact: the device reads it on its next check-in and
// moves itself. Both ends are checked here — the device must belong to this
// account and the account must belong to the network — so neither half of the
// pair can be used to reach the other.
func (s *Store) SetDeviceNetwork(accountID, nodeID, networkID string) error {
	if strings.TrimSpace(networkID) != "" {
		var member int
		if err := s.db.QueryRow(`SELECT count(*) FROM network_member
            WHERE network_id=? AND account_id=?`, networkID, accountID).Scan(&member); err != nil {
			return err
		}
		if member == 0 {
			return ErrNetworkMember
		}
	}
	return s.updateOwnDevice(accountID, nodeID,
		`UPDATE node SET desired_network=? WHERE id=? AND owner_account_id=?`, networkID)
}

// RequestDeviceSignOut asks a machine to forget its account credential.
func (s *Store) RequestDeviceSignOut(accountID, nodeID string) error {
	return s.updateOwnDevice(accountID, nodeID,
		`UPDATE node SET sign_out_at=? WHERE id=? AND owner_account_id=?`, now())
}

// RemoveDevice forgets a machine entirely.
//
// This is bookkeeping, not revocation: a machine that still holds a valid
// credential will register itself again on its next check-in. Signing it out
// first is what actually removes its access, and the interface says so.
func (s *Store) RemoveDevice(accountID, nodeID string) error {
	result, err := s.db.Exec(`DELETE FROM node WHERE id=? AND owner_account_id=?`,
		nodeID, accountID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// updateOwnDevice applies a one-value update scoped to this account's devices.
// An UPDATE that matches nothing reports no error, so the row count is the only
// thing standing between "changed it" and "there was nothing to change".
func (s *Store) updateOwnDevice(accountID, nodeID, statement string, value any) error {
	result, err := s.db.Exec(statement, value, nodeID, accountID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// networkRole reports what an account may do in a network, or ErrNotFound when
// it is not a member at all.
func (s *Store) networkRole(networkID, accountID string) (string, error) {
	var role string
	err := s.db.QueryRow(`SELECT role FROM network_member WHERE network_id=? AND account_id=?`,
		networkID, accountID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}
