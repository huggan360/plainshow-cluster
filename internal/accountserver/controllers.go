package accountserver

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ControllerServer is a separately hosted Cowork relay registered with the
// account/key service. It never runs inside the account service process.
type ControllerServer struct {
	ID             string `json:"id"`
	OwnerAccountID string `json:"owner_account_id"`
	OwnerUsername  string `json:"owner_username,omitempty"`
	Name           string `json:"name"`
	PublicURL      string `json:"public_url"`
	Online         bool   `json:"online"`
	Networks       int    `json:"networks"`
	LastSeen       string `json:"last_seen"`
	Created        string `json:"created_at"`
}

// ControllerNetwork is one relay assignment. RelayToken is returned only to
// the controller itself and to nodes that prove membership with the network's
// management key.
type ControllerNetwork struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       string `json:"role,omitempty"`
	RelayToken string `json:"relay_token,omitempty"`
	Selected   bool   `json:"selected"`
}

// ControllerConfigure is the owner-authorized desired state of a controller.
type ControllerConfigure struct {
	Name             string   `json:"name"`
	PublicURL        string   `json:"public_url"`
	NetworkIDs       []string `json:"network_ids"`
	RotateCredential bool     `json:"rotate_credential,omitempty"`
}

// ControllerConfiguration contains secrets intended for the controller
// process. The credential is only populated when first claimed or rotated.
type ControllerConfiguration struct {
	Controller ControllerServer    `json:"controller"`
	Credential string              `json:"credential,omitempty"`
	Networks   []ControllerNetwork `json:"networks"`
}

// ControllerContext is the filtered view used after a global account signs in
// to a controller server.
type ControllerContext struct {
	Exists     bool                `json:"exists"`
	CanAccess  bool                `json:"can_access"`
	CanManage  bool                `json:"can_manage"`
	Controller ControllerServer    `json:"controller"`
	Networks   []ControllerNetwork `json:"networks"`
}

// ConfigureController claims or updates a controller. A controller has one
// durable owner, and that owner may only supply networks where their global
// role is owner or admin.
func (s *Store) ConfigureController(account Account, id string, input ControllerConfigure) (ControllerConfiguration, error) {
	id, input.Name, input.PublicURL = strings.TrimSpace(id), strings.TrimSpace(input.Name), strings.TrimRight(strings.TrimSpace(input.PublicURL), "/")
	if id == "" || input.Name == "" || input.PublicURL == "" || len(input.NetworkIDs) == 0 || len(input.NetworkIDs) > 100 {
		return ControllerConfiguration{}, errors.New("controller configuration is incomplete")
	}
	wanted := make([]string, 0, len(input.NetworkIDs))
	seenIDs := map[string]bool{}
	for _, raw := range input.NetworkIDs {
		networkID := strings.TrimSpace(raw)
		if networkID == "" || seenIDs[networkID] {
			continue
		}
		seenIDs[networkID] = true
		wanted = append(wanted, networkID)
	}
	if len(wanted) == 0 {
		return ControllerConfiguration{}, errors.New("select at least one network")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return ControllerConfiguration{}, err
	}
	defer tx.Rollback()

	var server ControllerServer
	var credentialHash string
	err = tx.QueryRow(`SELECT id,owner_account_id,name,public_url,credential_hash,last_seen,created_at
		FROM controller_server WHERE id=?`, id).Scan(&server.ID, &server.OwnerAccountID,
		&server.Name, &server.PublicURL, &credentialHash, &server.LastSeen, &server.Created)
	created := errors.Is(err, sql.ErrNoRows)
	if err != nil && !created {
		return ControllerConfiguration{}, err
	}
	if !created && server.OwnerAccountID != account.ID {
		return ControllerConfiguration{}, ErrControllerOwner
	}

	networks := make([]ControllerNetwork, 0, len(wanted))
	for _, networkID := range wanted {
		var network ControllerNetwork
		err := tx.QueryRow(`SELECT n.id,n.name,m.role FROM network n
			JOIN network_member m ON m.network_id=n.id
			WHERE n.id=? AND m.account_id=?`, networkID, account.ID).Scan(&network.ID, &network.Name, &network.Role)
		if err != nil || (network.Role != "owner" && network.Role != "admin") {
			return ControllerConfiguration{}, ErrControllerAccess
		}
		network.Selected = true
		networks = append(networks, network)
	}

	credential := ""
	if created || input.RotateCredential {
		credential, err = randomSecret()
		if err != nil {
			return ControllerConfiguration{}, err
		}
		credentialHash = TokenHash(credential)
	}
	timestamp := now()
	if created {
		server = ControllerServer{ID: id, OwnerAccountID: account.ID, Name: input.Name,
			PublicURL: input.PublicURL, LastSeen: timestamp, Created: timestamp}
		if _, err := tx.Exec(`INSERT INTO controller_server
			(id,owner_account_id,name,public_url,credential_hash,last_seen,created_at)
			VALUES(?,?,?,?,?,?,?)`, server.ID, server.OwnerAccountID, server.Name,
			server.PublicURL, credentialHash, server.LastSeen, server.Created); err != nil {
			return ControllerConfiguration{}, err
		}
	} else {
		server.Name, server.PublicURL, server.LastSeen = input.Name, input.PublicURL, timestamp
		if _, err := tx.Exec(`UPDATE controller_server SET name=?,public_url=?,credential_hash=?,last_seen=? WHERE id=?`,
			server.Name, server.PublicURL, credentialHash, timestamp, id); err != nil {
			return ControllerConfiguration{}, err
		}
	}

	existing := map[string]string{}
	rows, err := tx.Query(`SELECT network_id,relay_token FROM controller_network WHERE controller_id=?`, id)
	if err != nil {
		return ControllerConfiguration{}, err
	}
	for rows.Next() {
		var networkID, token string
		if err := rows.Scan(&networkID, &token); err != nil {
			rows.Close()
			return ControllerConfiguration{}, err
		}
		existing[networkID] = token
	}
	if err := rows.Close(); err != nil {
		return ControllerConfiguration{}, err
	}
	if _, err := tx.Exec(`DELETE FROM controller_network WHERE controller_id=?`, id); err != nil {
		return ControllerConfiguration{}, err
	}
	for index := range networks {
		token := existing[networks[index].ID]
		if token == "" {
			token, err = randomSecret()
			if err != nil {
				return ControllerConfiguration{}, err
			}
		}
		networks[index].RelayToken = token
		if _, err := tx.Exec(`INSERT INTO controller_network(controller_id,network_id,relay_token,updated_at)
			VALUES(?,?,?,?)`, id, networks[index].ID, token, timestamp); err != nil {
			return ControllerConfiguration{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ControllerConfiguration{}, err
	}
	server.Networks = len(networks)
	server.Online = true
	return ControllerConfiguration{Controller: server, Credential: credential, Networks: networks}, nil
}

// ControllerContextFor authorizes a global account against a controller. An
// unclaimed controller may be claimed by an account that administers at least
// one network. Afterward the owner can configure it; members of supplied
// networks may sign in and see its non-secret status.
func (s *Store) ControllerContextFor(account Account, id string) (ControllerContext, error) {
	server, err := s.Controller(id)
	if errors.Is(err, ErrNotFound) {
		networks, listErr := s.manageableNetworks(account.ID)
		if listErr != nil {
			return ControllerContext{}, listErr
		}
		allowed := len(networks) > 0
		return ControllerContext{CanAccess: allowed, CanManage: allowed, Networks: networks}, nil
	}
	if err != nil {
		return ControllerContext{}, err
	}
	owner := server.OwnerAccountID == account.ID
	if owner {
		networks, err := s.manageableNetworks(account.ID)
		if err != nil {
			return ControllerContext{}, err
		}
		selected, err := s.ControllerNetworks(id, false)
		if err != nil {
			return ControllerContext{}, err
		}
		selectedIDs := map[string]bool{}
		for _, network := range selected {
			selectedIDs[network.ID] = true
		}
		for index := range networks {
			networks[index].Selected = selectedIDs[networks[index].ID]
		}
		return ControllerContext{Exists: true, CanAccess: true, CanManage: true,
			Controller: server, Networks: networks}, nil
	}

	rows, err := s.db.Query(`SELECT n.id,n.name,m.role FROM controller_network cn
		JOIN network n ON n.id=cn.network_id
		JOIN network_member m ON m.network_id=n.id
		WHERE cn.controller_id=? AND m.account_id=? ORDER BY lower(n.name)`, id, account.ID)
	if err != nil {
		return ControllerContext{}, err
	}
	defer rows.Close()
	networks := []ControllerNetwork{}
	for rows.Next() {
		var network ControllerNetwork
		if err := rows.Scan(&network.ID, &network.Name, &network.Role); err != nil {
			return ControllerContext{}, err
		}
		network.Selected = true
		networks = append(networks, network)
	}
	if err := rows.Err(); err != nil {
		return ControllerContext{}, err
	}
	return ControllerContext{Exists: true, CanAccess: len(networks) > 0,
		CanManage: false, Controller: server, Networks: networks}, nil
}

func (s *Store) manageableNetworks(accountID string) ([]ControllerNetwork, error) {
	rows, err := s.db.Query(`SELECT n.id,n.name,m.role FROM network n
		JOIN network_member m ON m.network_id=n.id
		WHERE m.account_id=? AND m.role IN ('owner','admin') ORDER BY lower(n.name)`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ControllerNetwork{}
	for rows.Next() {
		var item ControllerNetwork
		if err := rows.Scan(&item.ID, &item.Name, &item.Role); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Controller returns public registry metadata without credentials.
func (s *Store) Controller(id string) (ControllerServer, error) {
	var item ControllerServer
	err := s.db.QueryRow(`SELECT c.id,c.owner_account_id,a.username,c.name,c.public_url,
		(SELECT count(*) FROM controller_network cn WHERE cn.controller_id=c.id),
		c.last_seen,c.created_at FROM controller_server c JOIN account a ON a.id=c.owner_account_id
		WHERE c.id=?`, id).Scan(&item.ID, &item.OwnerAccountID, &item.OwnerUsername,
		&item.Name, &item.PublicURL, &item.Networks, &item.LastSeen, &item.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	item.Online = item.LastSeen >= time.Now().UTC().Add(-2*time.Minute).Format(time.RFC3339)
	return item, err
}

// Controllers lists every registered controller for the global admin page.
func (s *Store) Controllers() ([]ControllerServer, error) {
	rows, err := s.db.Query(`SELECT c.id,c.owner_account_id,a.username,c.name,c.public_url,
		(SELECT count(*) FROM controller_network cn WHERE cn.controller_id=c.id),
		c.last_seen,c.created_at FROM controller_server c JOIN account a ON a.id=c.owner_account_id
		ORDER BY lower(c.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	threshold := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	out := []ControllerServer{}
	for rows.Next() {
		var item ControllerServer
		if err := rows.Scan(&item.ID, &item.OwnerAccountID, &item.OwnerUsername,
			&item.Name, &item.PublicURL, &item.Networks, &item.LastSeen, &item.Created); err != nil {
			return nil, err
		}
		item.Online = item.LastSeen >= threshold
		out = append(out, item)
	}
	return out, rows.Err()
}

// ControllerNetworks returns assignments, optionally including relay tokens.
func (s *Store) ControllerNetworks(id string, secrets bool) ([]ControllerNetwork, error) {
	rows, err := s.db.Query(`SELECT n.id,n.name,cn.relay_token FROM controller_network cn
		JOIN network n ON n.id=cn.network_id WHERE cn.controller_id=? ORDER BY lower(n.name)`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ControllerNetwork{}
	for rows.Next() {
		var item ControllerNetwork
		if err := rows.Scan(&item.ID, &item.Name, &item.RelayToken); err != nil {
			return nil, err
		}
		item.Selected = true
		if !secrets {
			item.RelayToken = ""
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ControllerCheckIn authenticates a controller credential, marks it online,
// and returns its current assignments and relay tokens.
func (s *Store) ControllerCheckIn(id, credential string) (ControllerConfiguration, error) {
	var hash string
	err := s.db.QueryRow(`SELECT credential_hash FROM controller_server WHERE id=?`, id).Scan(&hash)
	got := TokenHash(credential)
	if err != nil || len(hash) != len(got) || subtle.ConstantTimeCompare([]byte(hash), []byte(got)) != 1 {
		return ControllerConfiguration{}, ErrControllerAccess
	}
	timestamp := now()
	if _, err := s.db.Exec(`UPDATE controller_server SET last_seen=? WHERE id=?`, timestamp, id); err != nil {
		return ControllerConfiguration{}, err
	}
	server, err := s.Controller(id)
	if err != nil {
		return ControllerConfiguration{}, err
	}
	networks, err := s.ControllerNetworks(id, true)
	if err != nil {
		return ControllerConfiguration{}, err
	}
	return ControllerConfiguration{Controller: server, Networks: networks}, nil
}

// ControllerForNetwork finds a currently connected relay. Multiple controller
// servers may supply one network; nodes choose the most recently seen one.
func (s *Store) ControllerForNetwork(networkID string) (ControllerServer, string, error) {
	var item ControllerServer
	var token string
	threshold := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	err := s.db.QueryRow(`SELECT c.id,c.owner_account_id,a.username,c.name,c.public_url,
		(SELECT count(*) FROM controller_network x WHERE x.controller_id=c.id),
		c.last_seen,c.created_at,cn.relay_token FROM controller_network cn
		JOIN controller_server c ON c.id=cn.controller_id JOIN account a ON a.id=c.owner_account_id
		WHERE cn.network_id=? AND c.last_seen>=? ORDER BY c.last_seen DESC LIMIT 1`, networkID, threshold).Scan(
		&item.ID, &item.OwnerAccountID, &item.OwnerUsername, &item.Name, &item.PublicURL,
		&item.Networks, &item.LastSeen, &item.Created, &token)
	if errors.Is(err, sql.ErrNoRows) {
		return item, "", ErrNotFound
	}
	item.Online = err == nil
	return item, token, err
}
