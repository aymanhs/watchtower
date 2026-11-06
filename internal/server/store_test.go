package server

import (
	"strings"
	"testing"
)

func sshCfg(name string) *Config {
	return &Config{
		Name:       name,
		Connection: Connection{Type: ConnectionSSH, Host: "h", User: "u", Key: "~/.ssh/id"},
		Groups:     []string{"system"},
	}
}

func TestSaveLoadEditDelete(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, sshCfg("web 1")); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, sshCfg("web 1")); err == nil {
		t.Fatal("saving a second new config with the same name must fail")
	}
	if err := Save(dir, sshCfg("db")); err != nil {
		t.Fatal(err)
	}

	list, err := LoadServers(dir)
	if err != nil || len(list) != 2 {
		t.Fatalf("load: %v %d", err, len(list))
	}
	var web *Config
	for _, c := range list {
		if c.Name == "web 1" {
			web = c
		}
	}
	if web == nil || web.Connection.Port != 22 {
		t.Fatalf("web = %+v", web)
	}

	// An edit rewrites the same file, even when renamed.
	file := web.SourceFile
	web.Name = "web-one"
	if err := Save(dir, web); err != nil {
		t.Fatal(err)
	}
	if web.SourceFile != file {
		t.Fatalf("rename moved the file: %s -> %s", file, web.SourceFile)
	}
	web.Name = "db"
	if err := Save(dir, web); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("rename onto another server must fail, got %v", err)
	}

	if err := Delete(dir, "web-one"); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir, "web-one"); err == nil {
		t.Fatal("deleting twice must fail")
	}
	if list, _ := LoadServers(dir); len(list) != 1 || list[0].Name != "db" {
		t.Fatalf("after delete: %+v", list)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	bad := sshCfg("x")
	bad.Connection.Key = ""
	if err := Save(t.TempDir(), bad); err == nil {
		t.Fatal("invalid config must not be written")
	}
}

func TestParseSSHConfig(t *testing.T) {
	const cfg = `
# comment
Host *
    User fallback
    IdentityFile ~/.ssh/default

Host web db
    HostName=10.0.0.5
    Port 2222

Host jump
    User ayman
    IdentityFile ~/.ssh/jump
    HostName jump.example.com
    IdentityFile ~/.ssh/ignored

Host *.internal
    User nobody

Match host foo
    User skipped
`
	hosts, err := ParseSSHConfig(strings.NewReader(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 3 {
		t.Fatalf("hosts = %+v", hosts)
	}
	web, jump := hosts[0], hosts[2]
	if web.Alias != "web" || web.HostName != "10.0.0.5" || web.Port != 2222 || web.User != "fallback" || web.IdentityFile != "~/.ssh/default" {
		t.Errorf("web = %+v", web)
	}
	if jump.User != "ayman" || jump.HostName != "jump.example.com" || jump.IdentityFile != "~/.ssh/jump" {
		t.Errorf("jump = %+v", jump)
	}
}
