package main

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/app"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/server"
)

// Server management: a modal listing the configured servers (the YAML files
// in serverDir) with add, edit and delete. Every change is written to disk
// and then applied live through the runner, which swaps the whole collection.
// It is unavailable in demo mode, where runner is nil.
var (
	runner        *app.Runner
	knownGroups   []string
	serverDir     string
	requestUpdate func(func()) // runs fn on the UI thread

	serversOpen    bool
	serverFormOpen bool
	serverEditing  *server.Config // nil while adding
	serverList     []*server.Config
	serverBusy     bool
	serverNotice   string
	serverErr      string
	serverDelete   string // name awaiting delete confirmation

	srvName, srvType, srvHost, srvPort, srvUser, srvKey, srvCluster string
	srvGroupOpts                                                    []string
	srvGroupSel                                                     []bool
	srvImport                                                       string
	sshHosts                                                        []server.SSHHost
)

const importPlaceholder = "import from ~/.ssh/config…"

var serverTypes = []string{string(server.ConnectionSSH), string(server.ConnectionLocal)}

func shellsOf(r *app.Runner) app.Shells {
	if r == nil {
		return nil
	}
	return r.Shells()
}

func serversTooltip() string {
	if runner == nil {
		return "Manage servers (not available in demo mode)"
	}
	return "Manage servers"
}

func waitingText() string {
	if runner != nil {
		return "waiting for data… (add servers with the servers button in the toolbar)"
	}
	return "waiting for data…"
}

// groupNames is every metric group any definition belongs to, sorted.
func groupNames(defs []*metric.Definition) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range defs {
		for _, g := range d.Groups {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	sort.Strings(out)
	return out
}

func openServers() {
	serversOpen, serverFormOpen = true, false
	serverNotice, serverErr, serverDelete = "", "", ""
	refreshServerList()
}

func refreshServerList() {
	list, err := server.LoadServers(serverDir)
	if err != nil {
		serverNotice = "cannot read servers: " + err.Error()
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	serverList = list
}

// openServerForm starts the editor on cfg, or on a blank new server when
// cfg is nil.
func openServerForm(cfg *server.Config) {
	serverFormOpen, serverErr, serverEditing = true, "", cfg
	srvImport = importPlaceholder
	sshHosts, _ = server.LoadSSHConfig()

	srvName, srvType, srvHost, srvPort, srvUser, srvKey, srvCluster = "", string(server.ConnectionSSH), "", "22", "", "~/.ssh/id_ed25519", ""
	var have []string
	if cfg != nil {
		srvName, srvType, srvCluster = cfg.Name, string(cfg.Connection.Type), cfg.Cluster
		srvHost, srvUser, srvKey = cfg.Connection.Host, cfg.Connection.User, cfg.Connection.Key
		srvPort = strconv.Itoa(cfg.Connection.Port)
		have = cfg.Groups
	}
	srvGroupOpts = append([]string(nil), knownGroups...)
	for _, g := range have {
		if !contains(srvGroupOpts, g) {
			srvGroupOpts = append(srvGroupOpts, g)
		}
	}
	srvGroupSel = make([]bool, len(srvGroupOpts))
	for i, g := range srvGroupOpts {
		srvGroupSel[i] = contains(have, g)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// applyImport fills the form from a ~/.ssh/config host.
func applyImport(alias string) {
	for _, h := range sshHosts {
		if h.Alias != alias {
			continue
		}
		if strings.TrimSpace(srvName) == "" {
			srvName = h.Alias
		}
		srvType, srvHost, srvUser = string(server.ConnectionSSH), h.HostName, h.User
		if h.Port != 0 {
			srvPort = strconv.Itoa(h.Port)
		}
		if h.IdentityFile != "" {
			srvKey = h.IdentityFile
		}
	}
}

// formConfig builds the server.Config the form describes. An edit starts
// from the existing config, so settings the form doesn't show (metric
// overrides) survive.
func formConfig() (*server.Config, error) {
	cfg := &server.Config{}
	if serverEditing != nil {
		cp := *serverEditing
		cfg = &cp
	}
	cfg.Name = strings.TrimSpace(srvName)
	cfg.Cluster = strings.TrimSpace(srvCluster)
	cfg.Groups = nil
	for i, g := range srvGroupOpts {
		if srvGroupSel[i] {
			cfg.Groups = append(cfg.Groups, g)
		}
	}
	cfg.Connection = server.Connection{Type: server.ConnectionType(srvType)}
	if cfg.Connection.Type == server.ConnectionSSH {
		cfg.Connection.Host = strings.TrimSpace(srvHost)
		cfg.Connection.User = strings.TrimSpace(srvUser)
		cfg.Connection.Key = strings.TrimSpace(srvKey)
		if p := strings.TrimSpace(srvPort); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("port %q is not a valid port", p)
			}
			cfg.Connection.Port = n
		}
	}
	return cfg, nil
}

func saveServerForm() {
	cfg, err := formConfig()
	if err == nil {
		err = server.Save(serverDir, cfg)
	}
	if err != nil {
		serverErr = err.Error()
		return
	}
	serverFormOpen = false
	applyServers("saved " + cfg.Name)
}

// applyServers reloads the collection off the UI thread (connecting to
// servers can take seconds) and reports back through serverNotice. A config
// that fails to load leaves the previous collection running.
func applyServers(what string) {
	refreshServerList()
	if serverBusy {
		serverNotice = what + " — reload already in progress, save again in a moment"
		return
	}
	serverBusy, serverNotice = true, what+" — applying…"
	go func() {
		err := runner.Reload()
		requestUpdate(func() {
			serverBusy = false
			if err != nil {
				log.Printf("servers: reload: %v", err)
				serverNotice = what + " — reload failed, still running the previous configuration: " + err.Error()
				return
			}
			serverNotice = what + " — applied"
		})
	}()
}

func serversModal(c *ui.Context) {
	if runner == nil {
		return
	}
	t := c.Theme()
	ui.Modal(c, &serversOpen, func() {
		ui.Column(c).Width(560).Background(t.Surface).Radius(8).Padding(16).Gap(10).Children(func() {
			if serverFormOpen {
				serverForm(c)
			} else {
				serverListView(c)
			}
		})
	})
}

func serverListView(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Servers").FontSize(13).Bold().Grow(1)
		if squareIconButton(c, "servers-add", addWidgetIcon, "Add a server").Clicked() {
			openServerForm(nil)
		}
		if squareIconButton(c, "servers-close", cancelIcon, "Close").Clicked() {
			serversOpen = false
		}
	})
	if len(serverList) == 0 {
		ui.Text(c, "no servers configured yet").FontSize(11).TextColor(t.TextMuted)
	}
	ui.Scroll(c).Height(280).Gap(2).Children(func() {
		for i, cfg := range serverList {
			cfg := cfg
			row := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(3, 4, 3, 4).Children(func() {
				ui.Column(c).Grow(1).Children(func() {
					ui.Text(c, cfg.Name).FontSize(12)
					ui.Text(c, serverSummary(cfg)).FontSize(10).TextColor(t.TextMuted)
				})
				if serverDelete == cfg.Name {
					ui.Text(c, "delete?").FontSize(11).TextColor(t.Danger)
					if squareIconButton(c, fmt.Sprintf("server-del-yes-%d", i), confirmIcon, "Yes, delete "+cfg.Name).Clicked() {
						serverDelete = ""
						if err := server.Delete(serverDir, cfg.Name); err != nil {
							serverNotice = "delete failed: " + err.Error()
						} else {
							applyServers("deleted " + cfg.Name)
						}
					}
					if squareIconButton(c, fmt.Sprintf("server-del-no-%d", i), cancelIcon, "Keep it").Clicked() {
						serverDelete = ""
					}
					return
				}
				if squareIconButton(c, fmt.Sprintf("server-edit-%d", i), editIcon, "Edit "+cfg.Name).Clicked() {
					openServerForm(cfg)
				}
				if squareIconButton(c, fmt.Sprintf("server-del-%d", i), removeIcon, "Delete "+cfg.Name).Clicked() {
					serverDelete = cfg.Name
				}
			})
			if serverDelete == "" && row.DoubleClicked() {
				openServerForm(cfg)
			}
		}
	})
	if serverNotice != "" {
		ui.Text(c, serverNotice).FontSize(11).TextColor(t.TextMuted)
	}
}

func serverSummary(cfg *server.Config) string {
	s := "local"
	if cfg.Connection.Type == server.ConnectionSSH {
		s = fmt.Sprintf("%s@%s:%d", cfg.Connection.User, cfg.Connection.Host, cfg.Connection.Port)
	}
	s += " · " + strings.Join(cfg.Groups, ", ")
	if cfg.Cluster != "" {
		s += " · cluster " + cfg.Cluster
	}
	return s
}

func serverForm(c *ui.Context) {
	t := c.Theme()
	title := "Add server"
	if serverEditing != nil {
		title = "Edit server — " + serverEditing.Name
	}
	ui.Text(c, title).FontSize(13).Bold()

	if serverEditing == nil && len(sshHosts) > 0 {
		aliases := []string{importPlaceholder}
		for _, h := range sshHosts {
			aliases = append(aliases, h.Alias)
		}
		before := srvImport
		ui.Select(c, &srvImport, aliases)
		if srvImport != before && srvImport != importPlaceholder {
			applyImport(srvImport)
		}
	}

	field := func(label string, v *string, placeholder string) {
		ui.Text(c, label).FontSize(11).TextColor(t.TextMuted)
		ui.TextInput(c, v).Placeholder(placeholder)
	}
	field("Name", &srvName, "how it appears in the sidebar")
	ui.Text(c, "Connection").FontSize(11).TextColor(t.TextMuted)
	ui.Select(c, &srvType, serverTypes)
	if srvType == string(server.ConnectionSSH) {
		field("Host", &srvHost, "hostname or IP")
		ui.Row(c).Gap(10).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() { field("User", &srvUser, "login name") })
			ui.Column(c).Gap(2).Width(90).Children(func() { field("Port", &srvPort, "22") })
		})
		field("Private key file (no passwords are stored)", &srvKey, "~/.ssh/id_ed25519")
	}
	field("Cluster (optional)", &srvCluster, "groups servers in the sidebar")

	ui.Text(c, "Metric groups").FontSize(11).TextColor(t.TextMuted)
	ui.Row(c).Wrap().Gap(12).Children(func() {
		for i, g := range srvGroupOpts {
			ui.Checkbox(c, &srvGroupSel[i], g)
		}
	})

	if serverErr != "" {
		ui.Text(c, serverErr).FontSize(11).TextColor(t.Danger)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if squareIconButton(c, "server-form-cancel", cancelIcon, "Back to the list").Clicked() {
			serverFormOpen = false
		}
		if squareIconButton(c, "server-form-save", confirmIcon, "Save and apply").Clicked() {
			saveServerForm()
		}
	})
}
