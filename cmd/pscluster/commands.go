package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// ---------------------------------------------------------------- networks --

func cmdNetwork(args []string) error {
	f := parseFlags(args)
	d, _, cfg, err := openDaemon(f)
	if err != nil {
		return err
	}

	action := "list"
	if len(f.rest) > 0 {
		action = f.rest[0]
	}

	switch action {
	case "list":
		var out struct {
			Networks []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Nodes int    `json:"nodes"`
			} `json:"networks"`
		}
		if err := d.call("GET", "/api/networks", nil, &out); err != nil {
			return err
		}
		if len(out.Networks) == 0 {
			fmt.Print("\n  This node belongs to no networks.\n\n")
			return nil
		}
		fmt.Printf("\n  %-20s %-18s %s\n", "NETWORK", "ID", "")
		for _, n := range out.Networks {
			marker := " "
			if n.ID == cfg.ActiveNetwork {
				marker = "*"
			}
			fmt.Printf("  %s %-18s %-18s\n", marker, n.Name, n.ID)
		}
		fmt.Printf("\n  * is the active network.\n\n")
		return nil

	case "use":
		if len(f.rest) < 2 {
			return errors.New("usage: pscluster network use <id>")
		}
		if err := d.call("PUT", "/api/networks/"+f.rest[1]+"/active", nil, nil); err != nil {
			return err
		}
		fmt.Printf("Active network is now %s.\n", f.rest[1])
		return nil

	default:
		return fmt.Errorf("unknown network command %q (list, use)", action)
	}
}

// ----------------------------------------------------------------- invites --

func cmdInvite(args []string) error {
	f := parseFlags(args)
	d, _, cfg, err := openDaemon(f)
	if err != nil {
		return err
	}
	network := f.get("network", cfg.ActiveNetwork)
	if network == "" {
		return errors.New("this node has no active network")
	}
	role := f.get("role", "member")

	var out struct {
		Code    string `json:"code"`
		Expires string `json:"expires_at"`
	}
	body := map[string]any{"role": role}
	if key := f.get("tailnet-auth-key", ""); key != "" {
		body["tailnet_auth_key"] = key
	}
	if server := f.get("tailnet-login-server", ""); server != "" {
		body["tailnet_login_server"] = server
	}
	if err := d.call("POST", "/api/networks/"+network+"/invites", body, &out); err != nil {
		return err
	}

	fmt.Printf("\n  Join code (%s, single use)\n\n", role)
	fmt.Printf("  %s\n\n", out.Code)
	fmt.Printf("  On the other machine:\n\n")
	fmt.Printf("    pscluster join %s\n\n", shorten(out.Code))
	if out.Expires != "" {
		fmt.Printf("  Expires %s.\n\n", out.Expires)
	}
	return nil
}

// shorten keeps the printed example readable without implying the code is short.
func shorten(code string) string {
	if len(code) <= 28 {
		return code
	}
	return code[:24] + "…"
}

// -------------------------------------------------------------------- join --

func cmdJoin(args []string) error {
	f := parseFlags(args)
	if len(f.rest) == 0 {
		return errors.New("usage: pscluster join <code>\n\n" +
			"  Get a code on the other machine with:  pscluster invite")
	}
	d, _, _, err := openDaemon(f)
	if err != nil {
		return err
	}

	body := map[string]any{"code": f.rest[0]}
	if endpoint := f.get("endpoint", ""); endpoint != "" {
		body["endpoint"] = endpoint
	}

	var out struct {
		Network struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"network"`
		Role string `json:"role"`
	}
	if err := d.call("POST", "/api/networks/join", body, &out); err != nil {
		return err
	}
	fmt.Printf("\n  Joined %s as %s.\n\n", out.Network.Name, out.Role)
	fmt.Printf("  This machine now shares that network's projects and jobs.\n\n")
	return nil
}

func cmdController(args []string) error {
	f := parseFlags(args)
	action := ""
	if len(f.rest) > 0 {
		action = f.rest[0]
	}
	if action != "invite" {
		return errors.New("usage: pscluster controller invite [--network ID]")
	}
	d, _, cfg, err := openDaemon(f)
	if err != nil {
		return err
	}
	network := f.get("network", cfg.ActiveNetwork)
	var out struct {
		Code string `json:"code"`
	}
	if err := d.call("POST", "/api/networks/"+network+"/controller-invites", map[string]any{}, &out); err != nil {
		return err
	}
	fmt.Printf("\n  Controller enrollment code\n\n  %s\n\n", out.Code)
	fmt.Printf("  pscluster-controller attach %s --advertise https://controller.example\n\n", shorten(out.Code))
	return nil
}

// ------------------------------------------------------------------ github --

func cmdGitHub(args []string) error {
	f := parseFlags(args)
	d, _, _, err := openDaemon(f)
	if err != nil {
		return err
	}

	action := "status"
	if len(f.rest) > 0 {
		action = f.rest[0]
	}

	switch action {
	case "status":
		var out struct {
			Connected    bool   `json:"connected"`
			Account      string `json:"account"`
			Name         string `json:"name"`
			GitReady     bool   `json:"git_ready"`
			Error        string `json:"error"`
			Repositories struct {
				Total   int `json:"total"`
				Private int `json:"private"`
			} `json:"repositories"`
		}
		if err := d.call("GET", "/api/github", nil, &out); err != nil {
			return err
		}
		if !out.Connected {
			fmt.Printf("\n  GitHub is not connected.\n")
			if out.Error != "" {
				fmt.Printf("  %s\n", out.Error)
			}
			fmt.Printf("\n  Connect it:  pscluster github connect\n\n")
			return nil
		}
		fmt.Printf("\n  Connected as @%s", out.Account)
		if out.Name != "" {
			fmt.Printf("  (%s)", out.Name)
		}
		fmt.Printf("\n  %d repositories, %d private\n", out.Repositories.Total, out.Repositories.Private)
		if !out.GitReady && out.Error != "" {
			fmt.Printf("\n  %s\n", out.Error)
		}
		fmt.Println()
		return nil

	case "connect":
		token, err := readSecret("GitHub token (classic, repo scope): ")
		if err != nil {
			return err
		}
		if len(strings.TrimSpace(token)) < 20 {
			return errors.New("that does not look like a GitHub token")
		}
		var out struct {
			Account string `json:"account"`
		}
		if err := d.call("POST", "/api/github",
			map[string]string{"token": strings.TrimSpace(token)}, &out); err != nil {
			return err
		}
		fmt.Printf("Connected as @%s.\n", out.Account)
		return nil

	case "disconnect":
		if err := d.call("DELETE", "/api/github", nil, nil); err != nil {
			return err
		}
		fmt.Println("GitHub disconnected.")
		return nil

	default:
		return fmt.Errorf("unknown github command %q (status, connect, disconnect)", action)
	}
}

// readSecret prompts without echoing when there is a terminal, and reads a
// piped line when there is not, so this works in a script.
func readSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Print(prompt)
		raw, err := term.ReadPassword(fd)
		fmt.Println()
		return string(raw), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no token on standard input")
	}
	return line, nil
}

// ------------------------------------------------------------------ update --

func cmdUpdate(args []string) error {
	f := parseFlags(args)
	d, _, _, err := openDaemon(f)
	if err != nil {
		return err
	}

	action := "check"
	if len(f.rest) > 0 {
		action = f.rest[0]
	}

	type status struct {
		Current   string `json:"current"`
		Latest    string `json:"latest"`
		Available bool   `json:"available"`
		Error     string `json:"error"`
		Managed   bool   `json:"managed"`
	}

	switch action {
	case "status", "check":
		var out status
		path := "/api/update"
		method := "GET"
		if action == "check" {
			path, method = "/api/update/check", "POST"
		}
		if err := d.call(method, path, nil, &out); err != nil {
			return err
		}
		fmt.Printf("\n  Installed  %s\n", out.Current)
		if out.Latest != "" {
			fmt.Printf("  Available  %s\n", out.Latest)
		}
		if out.Error != "" {
			fmt.Printf("\n  %s\n", out.Error)
		}
		if out.Available {
			fmt.Printf("\n  Install it:  pscluster update apply\n")
			if !out.Managed {
				fmt.Printf("  This node is not under a service manager, so it will\n")
				fmt.Printf("  restart itself in place.\n")
			}
		} else if out.Error == "" {
			fmt.Printf("\n  This node is up to date.\n")
		}
		fmt.Println()
		return nil

	case "apply":
		var out struct {
			Version string `json:"version"`
		}
		if err := d.call("POST", "/api/update/apply", nil, &out); err != nil {
			return err
		}
		fmt.Printf("Installed %s. The node is restarting into it.\n", out.Version)
		return nil

	default:
		return fmt.Errorf("unknown update command %q (check, status, apply)", action)
	}
}
