# Windows is the primary target: that's what most users run.
# Keep this pinned to the mygo version in go.mod so the CLI tool and the
# library the app is built against never drift apart.
MYGO_VERSION := v0.7.3

.PHONY: build build-windows build-linux test run clean

# Default: the Windows GUI build most users want.
build: build-windows

# Native GUI app, via mygo's own build tool: proper GUI-subsystem exe (no
# console flash), app name/version from mygo.json, icon/installer-ready
# once we add those. Cross-compiles cleanly from Linux — mygo needs no cgo.
build-windows:
	go run github.com/egoist/mygo/cmd/mygo@$(MYGO_VERSION) build -platform windows/amd64
	GOOS=windows GOARCH=amd64 go build -o dist/windows-amd64/watchtower-console.exe ./cmd/watchtower

# Local dev builds for this machine, for quick iteration without the
# mygo.json packaging step.
build-linux:
	mkdir -p dist/linux-amd64
	go build -o dist/linux-amd64/watchtower-gui ./cmd/watchtower-gui
	go build -o dist/linux-amd64/watchtower ./cmd/watchtower

test:
	go test ./... -race

# Run the GUI directly against data/servers, no packaging.
run:
	go run ./cmd/watchtower-gui

clean:
	rm -rf dist
