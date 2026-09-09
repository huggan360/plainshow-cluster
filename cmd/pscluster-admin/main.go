// Command pscluster-admin runs the central Plainshow account authority and its
// lightweight global statistics page.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/huggan360/plainshow-cluster/adminweb"
	"github.com/huggan360/plainshow-cluster/internal/accountserver"
	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "pscluster-admin: %v\n", err)
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
	case "status":
		return status(flags)
	case "version", "--version", "-v":
		fmt.Printf("%s Admin %s (%s, %s)\n", version.Product, version.Version, version.Commit, config.Platform())
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func usage() {
	fmt.Printf(`%s Admin %s

  pscluster-admin init [--root DIR] [--bind ADDR] [--port N]
                       [--public-url HTTPS_URL] [--registration-open true|false]
      Create the account database. The first registered account is its admin.

  pscluster-admin serve [--root DIR]
      Serve the admin page and identity API for a TLS reverse proxy.

  pscluster-admin status [--root DIR]
  pscluster-admin version

The root is taken from --root, then %s, then a platform default.
`, version.Product, version.Version, accountserver.EnvRoot)
}

type flags map[string]string

func parseFlags(args []string) flags {
	out := flags{}
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
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
	return config.NewLayout(f.get("root", accountserver.DefaultRoot()))
}

func initialise(flags flags) error {
	layout, err := layoutFor(flags)
	if err != nil {
		return err
	}
	if _, err := os.Stat(layout.AdminConfigFile()); err == nil {
		return fmt.Errorf("an account server already exists at %s", layout.Root)
	}
	if err := layout.EnsureAdminDirs(); err != nil {
		return err
	}
	value := accountserver.Defaults()
	value.Listen.Bind = flags.get("bind", value.Listen.Bind)
	value.PublicURL = strings.TrimRight(flags.get("public-url", ""), "/")
	if value.PublicURL != "" {
		parsed, err := url.Parse(value.PublicURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return errors.New("--public-url must be a complete https:// URL")
		}
	}
	if raw := flags.get("registration-open", ""); raw != "" {
		value.RegistrationOpen, err = strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("--registration-open expects true or false, got %q", raw)
		}
	}
	if raw := flags.get("port", ""); raw != "" {
		value.Listen.Port, err = strconv.Atoi(raw)
		if err != nil || value.Listen.Port < 1 || value.Listen.Port > 65535 {
			return fmt.Errorf("--port must be a number between 1 and 65535, got %q", raw)
		}
	} else {
		value.Listen.Port, err = accountserver.ProbePort(value.Listen.Bind)
		if err != nil {
			return err
		}
	}
	if err := accountserver.Save(layout, value); err != nil {
		return err
	}
	store, err := accountserver.Open(layout.AdminDatabase())
	if err != nil {
		return err
	}
	defer store.Close()
	fmt.Printf("\n  %s Admin\n\n", version.Product)
	fmt.Printf("  Root        %s\n", layout.Root)
	fmt.Printf("  Listen      %s:%d\n", value.Listen.Bind, value.Listen.Port)
	if value.PublicURL != "" {
		fmt.Printf("  Public URL  %s\n", value.PublicURL)
	}
	fmt.Printf("\n  Start it: pscluster-admin serve --root %s\n\n", layout.Root)
	return nil
}

func open(flags flags) (config.Layout, *accountserver.Config, *accountserver.Store, error) {
	layout, err := layoutFor(flags)
	if err != nil {
		return layout, nil, nil, err
	}
	value, err := accountserver.Load(layout)
	if err != nil {
		return layout, nil, nil, err
	}
	store, err := accountserver.Open(layout.AdminDatabase())
	return layout, value, store, err
}

func status(flags flags) error {
	layout, value, store, err := open(flags)
	if err != nil {
		return err
	}
	defer store.Close()
	stats, err := store.Stats()
	if err != nil {
		return err
	}
	fmt.Printf("\n  %s Admin\n\n", version.Product)
	fmt.Printf("  Root      %s\n", layout.Root)
	fmt.Printf("  Listen    %s:%d\n", value.Listen.Bind, value.Listen.Port)
	fmt.Printf("  Accounts  %d\n", stats.Accounts)
	fmt.Printf("  Nodes     %d (%d online)\n", stats.Nodes, stats.OnlineNodes)
	fmt.Printf("  Networks  %d\n\n", stats.Networks)
	return nil
}

func serve(flags flags) error {
	layout, value, store, err := open(flags)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := accountserver.NewServer(value, store, adminweb.Assets)
	return server.ListenAndServe(ctx, func(address string) {
		fmt.Printf("\n  %s Admin\n\n", version.Product)
		fmt.Printf("  local     %s\n", address)
		if value.PublicURL != "" {
			fmt.Printf("  public    %s\n", value.PublicURL)
		}
		fmt.Printf("  root      %s\n\n", layout.Root)
	})
}
