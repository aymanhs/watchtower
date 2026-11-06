package server

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SSHHost is one concrete Host entry of an OpenSSH client config, with
// values inherited from "Host *" blocks filled in. It is only a source of
// suggestions for a new server's connection; nothing here is authoritative.
type SSHHost struct {
	Alias        string
	HostName     string // the real address; falls back to Alias
	User         string
	Port         int
	IdentityFile string
}

// LoadSSHConfig reads ~/.ssh/config. A missing file is not an error.
func LoadSSHConfig() ([]SSHHost, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	f, err := os.Open(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return ParseSSHConfig(f)
}

// ParseSSHConfig understands the common subset: Host blocks (wildcard
// patterns are not hosts, but their values act as defaults), HostName,
// User, Port and IdentityFile. Match and Include are ignored. As in
// OpenSSH, the first value given for a keyword wins.
func ParseSSHConfig(r io.Reader) ([]SSHHost, error) {
	type block struct {
		aliases []string
		host    SSHHost
	}
	var blocks []*block
	var cur *block

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val := splitKeyword(line)
		if key == "" {
			continue
		}
		switch key {
		case "host":
			cur = &block{aliases: strings.Fields(val)}
			blocks = append(blocks, cur)
			continue
		case "match":
			cur = nil
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "hostname":
			if cur.host.HostName == "" {
				cur.host.HostName = val
			}
		case "user":
			if cur.host.User == "" {
				cur.host.User = val
			}
		case "port":
			if n, err := strconv.Atoi(val); err == nil && cur.host.Port == 0 {
				cur.host.Port = n
			}
		case "identityfile":
			if cur.host.IdentityFile == "" {
				cur.host.IdentityFile = strings.Trim(val, `"`)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	var defaults SSHHost
	for _, b := range blocks {
		for _, a := range b.aliases {
			if a == "*" {
				fill(&defaults, b.host)
			}
		}
	}
	var out []SSHHost
	seen := map[string]bool{}
	for _, b := range blocks {
		for _, a := range b.aliases {
			if strings.ContainsAny(a, "*?!") || seen[a] {
				continue
			}
			seen[a] = true
			h := b.host
			h.Alias = a
			fill(&h, defaults)
			if h.HostName == "" {
				h.HostName = a
			}
			out = append(out, h)
		}
	}
	return out, nil
}

// fill sets any empty field of h from def.
func fill(h *SSHHost, def SSHHost) {
	if h.HostName == "" {
		h.HostName = def.HostName
	}
	if h.User == "" {
		h.User = def.User
	}
	if h.Port == 0 {
		h.Port = def.Port
	}
	if h.IdentityFile == "" {
		h.IdentityFile = def.IdentityFile
	}
}

// splitKeyword splits "Keyword value" or "Keyword=value", lower-casing the
// keyword.
func splitKeyword(line string) (key, val string) {
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return "", ""
	}
	return strings.ToLower(line[:i]), strings.TrimSpace(strings.TrimLeft(line[i:], " \t="))
}
