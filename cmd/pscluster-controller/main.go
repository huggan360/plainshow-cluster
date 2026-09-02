// Command pscluster-controller runs the optional always-reachable
// collaboration service. It is not a cluster device and never runs jobs.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/controller"
	"github.com/huggan360/plainshow-cluster/internal/identity"
	"github.com/huggan360/plainshow-cluster/internal/mesh"
	"github.com/huggan360/plainshow-cluster/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "pscluster-controller: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("choose a command")
	}
	command, flags := args[0], parseFlags(args[1:])
	switch command {
	case "init":
		return initialise(flags)
	case "serve":
		return serve(flags)
	case "attach":
		return attach(flags)
	case "status":
		return status(flags)
	case "version", "--version", "-v":
		fmt.Printf("%s Controller %s (%s, %s)\n", version.Product, version.Version, version.Commit, config.Platform())
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func usage() {
	fmt.Printf(`%s Controller %s

  pscluster-controller init [--root DIR] [--name NAME]
                            [--bind ADDR] [--port N] [--advertise URL]
      Create an independent controller root, identity and TLS certificate.

  pscluster-controller serve [--root DIR]
      Serve collaboration and network overview over HTTPS.

  pscluster-controller attach CODE [--root DIR] [--advertise HTTPS_URL]
      Attach this controller to a network using an admin-minted code.

  pscluster-controller status [--root DIR]
  pscluster-controller version

The root is taken from --root, then %s, then a platform default.
`, version.Product, version.Version, controller.EnvRoot)
}

type flags map[string]string

func parseFlags(args []string) flags {
	out := flags{}
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			if out["_arg"] == "" {
				out["_arg"] = args[i]
			}
			continue
		}
		key := strings.TrimPrefix(args[i], "--")
		if name, value, ok := strings.Cut(key, "="); ok {
			out[name] = value
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			out[key] = args[i+1]
			i++
		} else {
			out[key] = "true"
		}
	}
	return out
}

func (f flags) get(key, fallback string) string {
	if value := strings.TrimSpace(f[key]); value != "" {
		return value
	}
	return fallback
}

func layoutFor(f flags) (config.Layout, error) {
	return config.NewLayout(f.get("root", controller.DefaultRoot()))
}

func initialise(flags flags) error {
	layout, err := layoutFor(flags)
	if err != nil {
		return err
	}
	if _, err := os.Stat(layout.ControllerConfigFile()); err == nil {
		return fmt.Errorf("a controller already exists at %s", layout.Root)
	}
	if err := layout.EnsureControllerDirs(); err != nil {
		return err
	}
	value := controller.Defaults()
	value.Name = flags.get("name", value.Name)
	value.Listen.Bind = flags.get("bind", value.Listen.Bind)
	value.Listen.Advertise = strings.TrimRight(flags.get("advertise", ""), "/")
	if raw := flags.get("port", ""); raw != "" {
		value.Listen.Port, err = strconv.Atoi(raw)
		if err != nil || value.Listen.Port < 1 || value.Listen.Port > 65535 {
			return fmt.Errorf("--port must be a number between 1 and 65535, got %q", raw)
		}
	} else {
		value.Listen.Port, err = controller.ProbePort(value.Listen.Bind)
		if err != nil {
			return err
		}
	}
	key, err := identity.LoadOrCreate(layout.ControllerKey())
	if err != nil {
		return fmt.Errorf("create controller identity: %w", err)
	}
	value.ID = key.ID
	_, fingerprint, err := identity.TLSCertificate(key, layout.ControllerCert())
	if err != nil {
		return fmt.Errorf("create controller TLS certificate: %w", err)
	}
	if err := controller.Save(layout, value); err != nil {
		return err
	}
	fmt.Printf("\n  %s Controller\n\n", version.Product)
	fmt.Printf("  Name         %s\n", value.Name)
	fmt.Printf("  Root         %s\n", layout.Root)
	fmt.Printf("  Listen       %s:%d\n", value.Listen.Bind, value.Listen.Port)
	fmt.Printf("  Fingerprint  %s\n\n", fingerprint)
	fmt.Printf("  Start it: pscluster-controller serve --root %s\n\n", layout.Root)
	return nil
}

func open(flags flags) (config.Layout, *controller.Config, error) {
	layout, err := layoutFor(flags)
	if err != nil {
		return layout, nil, err
	}
	value, err := controller.Load(layout)
	return layout, value, err
}

func status(flags flags) error {
	layout, value, err := open(flags)
	if err != nil {
		return err
	}
	fmt.Printf("\n  %s Controller\n\n", version.Product)
	fmt.Printf("  Name      %s\n", value.Name)
	fmt.Printf("  ID        %s\n", value.ID)
	fmt.Printf("  Root      %s\n", layout.Root)
	fmt.Printf("  Listen    %s:%d\n", value.Listen.Bind, value.Listen.Port)
	fmt.Printf("  Networks  %d\n\n", len(value.Networks))
	return nil
}

func serve(flags flags) error {
	layout, value, err := open(flags)
	if err != nil {
		return err
	}
	key, err := identity.LoadOrCreate(layout.ControllerKey())
	if err != nil {
		return err
	}
	if key.ID != value.ID {
		return errors.New("controller identity does not match controller.yaml; restore its key or initialise a new root")
	}
	certificate, fingerprint, err := identity.TLSCertificate(key, layout.ControllerCert())
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := controller.NewServer(value, fingerprint)
	server.SetOverviewPath(layout.ControllerOverview())
	return server.ListenAndServe(ctx, certificate, func(address string) {
		fmt.Printf("\n  %s Controller  ·  %s\n\n", version.Product, value.Name)
		fmt.Printf("  %s\n", address)
		fmt.Printf("  root      %s\n", layout.Root)
		fmt.Printf("  networks  %d\n\n", len(value.Networks))
	})
}

func attach(flags flags) error {
	layout, value, err := open(flags)
	if err != nil {
		return err
	}
	code := flags.get("_arg", "")
	if code == "" {
		return errors.New("usage: pscluster-controller attach CODE [--advertise HTTPS_URL]")
	}
	invite, err := mesh.DecodeInvite(code)
	if err != nil {
		return err
	}
	if invite.Role != "controller" {
		return errors.New("that code enrolls a device, not a controller")
	}
	key, err := identity.LoadOrCreate(layout.ControllerKey())
	if err != nil {
		return err
	}
	_, fingerprint, err := identity.TLSCertificate(key, layout.ControllerCert())
	if err != nil {
		return err
	}
	address := strings.TrimRight(flags.get("advertise", value.Listen.Advertise), "/")
	if address == "" {
		return errors.New("set --advertise to the HTTPS address devices can reach")
	}
	request := controller.EnrollmentRequest{Token: invite.Token, ID: value.ID,
		Name: value.Name, PublicKey: base64.RawURLEncoding.EncodeToString(key.Public),
		Fingerprint: fingerprint, Address: address}
	client := mesh.NewClient(invite.Endpoint, invite.Fingerprint, invite.NetworkID, key)
	var response controller.EnrollmentResponse
	if err := client.JSON("POST", "/mesh/v1/controllers/join/"+invite.NetworkID,
		request, &response, false); err != nil {
		return fmt.Errorf("attach controller: %w", err)
	}
	network := controller.NetworkConfig{ID: response.Network.ID, Name: response.Network.Name,
		CollabToken: response.CollabToken}
	for _, node := range response.Nodes {
		network.Nodes = append(network.Nodes, controller.NodeKey{ID: node.NodeID, PublicKey: node.PublicKey})
	}
	replaced := false
	for i := range value.Networks {
		if value.Networks[i].ID == network.ID {
			value.Networks[i], replaced = network, true
		}
	}
	if !replaced {
		value.Networks = append(value.Networks, network)
	}
	value.Listen.Advertise = address
	if err := controller.Save(layout, value); err != nil {
		return err
	}
	fmt.Printf("Attached %s to %s.\n", value.Name, network.Name)
	return nil
}
