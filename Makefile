# Windows GUI is the only build target (cross-compiled from Linux).
# Keep this pinned to the mygo version in go.mod so the CLI tool and the
# library the app is built against never drift apart.
MYGO_VERSION := v0.7.4

.PHONY: build run-wsl test clean

# Windows GUI build, cross-compiled from Linux via mygo's own build tool:
# proper GUI-subsystem exe (no console flash), app name/version from
# mygo.json. mygo needs no cgo. Output lands under dist/.
build:
	go run github.com/egoist/mygo/cmd/mygo@$(MYGO_VERSION) build -platform windows/amd64 -skip-installer

# Dev loop under WSLg: runs the GUI natively on Linux (fast local disk, no
# WSL-to-Windows file bridge) against the synthetic fleet. Not a shipped
# target — Windows remains the only build.
run-wsl:
	LIBGL_ALWAYS_SOFTWARE=1 go run ./cmd/watchtower-gui -demo

test:
	go test ./... -race

clean:
	rm -rf dist
