package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/mcpregistration"
	"github.com/samibel/graphi/internal/state"
)

type setupPerRepoOptions struct {
	CWD            string
	Binary         string
	Root           string
	ClientID       string
	ConfigPath     string
	ClientExplicit bool
	AllRepos       bool
	Name           string
	Adopt          bool
	Unregister     bool
	DryRun         bool
	AutoRegister   bool
	NoAutoRegister bool
	Yes            bool
}

var setupStdinIsTerminal = func() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runSetupPerRepo(options setupPerRepoOptions) int {
	if err := validatePerRepoOptions(options); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup --per-repo: %v\n", err)
		return 1
	}
	stateDir, err := state.StrictStateDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup --per-repo: %v\n", err)
		return 1
	}
	clients, err := perRepoClients(options.ClientID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup --per-repo: %v\n", err)
		return 1
	}
	if options.ConfigPath != "" {
		clients[0] = clients[0].WithConfigPath(options.ConfigPath)
	}
	if options.AutoRegister && !options.Yes {
		if !setupStdinIsTerminal() {
			fmt.Fprintln(os.Stderr, "graphi: setup --per-repo: --auto-register requires an interactive confirmation or --yes")
			return 1
		}
		if !confirmAutoRegistration(clients) {
			fmt.Fprintln(os.Stderr, "graphi: setup --per-repo: auto-registration not confirmed")
			return 1
		}
	}
	var roots []string
	if !options.AllRepos && !options.NoAutoRegister {
		root := options.Root
		if root == "" {
			var ok bool
			root, ok = state.DetectRepo(options.CWD)
			if !ok {
				fmt.Fprintln(os.Stderr, "graphi: setup --per-repo: current directory is not a repository")
				return 1
			}
		}
		roots = []string{root}
	}
	result, err := (mcpregistration.Service{StateDir: stateDir, Binary: options.Binary}).Execute(mcpregistration.ServiceRequest{
		Roots: roots, AllRepos: options.AllRepos, Clients: clients, ExplicitName: options.Name,
		Adopt: options.Adopt, Unregister: options.Unregister, DryRun: options.DryRun,
		EnableAutoRegister: options.AutoRegister, DisableAutoRegister: options.NoAutoRegister,
	})
	for _, issue := range result.Issues {
		fmt.Fprintf(os.Stderr, "graphi: setup --per-repo: skipped descriptor: %v\n", issue)
	}
	for _, change := range result.Changes {
		if options.DryRun {
			fmt.Printf("[dry-run] %s (%s): no changes written\n", change.ClientID, change.ConfigPath)
		}
		fmt.Print(change.Result.Diff)
		fmt.Printf("client: %s\ntarget: %s\ncheckout: %s\n", change.ClientID, change.ConfigPath, change.CheckoutID)
		if change.Result.BackupPath != "" {
			fmt.Printf("backup: %s\n", change.Result.BackupPath)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup --per-repo: %v\n", err)
		return 1
	}
	if options.NoAutoRegister {
		if options.DryRun {
			fmt.Println("[dry-run] auto-registration policy would be disabled; no policy written")
		} else {
			fmt.Println("auto-registration policy disabled for the selected client targets")
		}
	}
	if options.AutoRegister {
		if options.DryRun {
			fmt.Println("[dry-run] consent snapshot not persisted")
		} else {
			fmt.Println("auto-registration consent saved for the concrete client targets above")
		}
	}
	return 0
}

func validatePerRepoOptions(options setupPerRepoOptions) error {
	if options.ConfigPath != "" && (!options.ClientExplicit || options.ClientID == "all") {
		return errorsText("--config requires exactly one explicit --client")
	}
	if options.AutoRegister && options.NoAutoRegister {
		return errorsText("--auto-register and --no-auto-register are mutually exclusive")
	}
	if options.AllRepos && (options.Root != "" || options.Name != "" || options.Adopt || options.Unregister) {
		return errorsText("--all-repos conflicts with --root, --name, --adopt, and --unregister")
	}
	if options.NoAutoRegister && (options.Root != "" || options.AllRepos || options.Name != "" || options.Adopt || options.Unregister || options.AutoRegister) {
		return errorsText("--no-auto-register changes policy only and conflicts with repository modifiers")
	}
	if options.Adopt && options.Name == "" {
		return errorsText("--adopt requires an explicit --name")
	}
	if options.Yes && !options.AutoRegister {
		return errorsText("--yes applies only to --auto-register")
	}
	return nil
}

type errorsText string

func (e errorsText) Error() string { return string(e) }

func perRepoClients(id string) ([]mcpconfig.Client, error) {
	supported := map[string]bool{"claude": true, "codex": true, "devin": true}
	if id != "all" {
		if !supported[id] {
			return nil, fmt.Errorf("unsupported per-repo client %q (want claude, codex, devin, or all)", id)
		}
		client, ok := mcpconfig.ClientByID(id)
		if !ok {
			return nil, fmt.Errorf("client %q is unavailable", id)
		}
		return []mcpconfig.Client{client}, nil
	}
	var clients []mcpconfig.Client
	for _, client := range mcpconfig.Clients() {
		if supported[client.ID] && client.Configurable() {
			clients = append(clients, client)
		}
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].ID < clients[j].ID })
	if len(clients) == 0 {
		return nil, fmt.Errorf("no supported local clients detected; select one explicitly with --client")
	}
	return clients, nil
}

func confirmAutoRegistration(clients []mcpconfig.Client) bool {
	var targets []string
	for _, client := range clients {
		path, err := client.ConfigPath()
		if err != nil {
			return false
		}
		targets = append(targets, client.Display+" ("+path+")")
	}
	fmt.Printf("Allow future explicit 'graphi sync' commands to update these global MCP configs?\n  %s\n[y/N]: ", strings.Join(targets, "\n  "))
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}
