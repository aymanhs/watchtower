# Windows is the primary target: that's what most users run.
# Keep this pinned to the mygo version in go.mod so the CLI tool and the
# library the app is built against never drift apart.
MYGO_VERSION := v0.7.4

.PHONY: build build-windows build-linux test run clean kill

# Default: the Windows GUI build most users want.
build: build-windows

# Kill any watchtower-gui already running before a build or a dev run —
# the app now holds a single-instance lock (RequestSingleInstanceLock,
# main.go), so a stale instance from a previous `make run` would just
# silently absorb the next one's command line and keep running the old
# code (built-in protection against *accidentally* running two at once
# is not what we want to fight here, it is still asked for explicitly).
# $(OS) is "Windows_NT" when make itself runs under Windows, unset under
# WSL/Linux, so this picks the right command either way without caring
# which platform the build output targets. Best-effort: "no such
# process" (nothing was running) is not a failure.
kill:
ifeq ($(OS),Windows_NT)
	-taskkill /IM watchtower-gui.exe /F >NUL 2>&1
else
	-pkill -f watchtower-gui >/dev/null 2>&1
endif

# Native GUI app, via mygo's own build tool: proper GUI-subsystem exe (no
# console flash), app name/version from mygo.json, icon/installer-ready
# once we add those. Cross-compiles cleanly from Linux — mygo needs no cgo.
build-windows: kill
	go run github.com/egoist/mygo/cmd/mygo@$(MYGO_VERSION) build -platform windows/amd64
	GOOS=windows GOARCH=amd64 go build -o dist/windows-amd64/watchtower-console.exe ./cmd/watchtower

# Local dev builds for this machine, for quick iteration without the
# mygo.json packaging step.
build-linux: kill
	mkdir -p dist/linux-amd64
	go build -o dist/linux-amd64/watchtower-gui ./cmd/watchtower-gui
	go build -o dist/linux-amd64/watchtower ./cmd/watchtower

test:
	go test ./... -race

# Run the GUI directly against data/servers, no packaging.
run: kill
	go run ./cmd/watchtower-gui

clean:
	rm -rf dist
