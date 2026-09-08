# Midden development shortcuts.
#
# GNU Make is optional. The cross-platform source of truth is documented in
# docs/DEVELOPMENT.md and uses direct Go commands.

BINARY ?= midden
PKG    := ./cmd/midden

.PHONY: all build test vet fmt check clean install start ui sync-content

all: check build

# The descriptor declares a digest over the agent overlay and skills, and a host
# REFUSES content whose digest does not match. Editing a document without
# rebuilding therefore makes it silently vanish from agent context: the module
# installs cleanly, the capabilities work, and the guidance is simply absent.
# Syncing before every build is what keeps the two from drifting apart.
build: sync-content
	go build -trimpath -o $(BINARY) $(PKG)

# Copy the published documents into the package that embeds them. go:embed
# cannot reach above its own directory, so the bytes the descriptor digests live
# beside the code while the paths it advertises point at the repository root.
sync-content:
	@cp agents/midden-recovery.md internal/module/content/agents/
	@cp skills/session-recovery/SKILL.md internal/module/content/skills/session-recovery/
	@cp skills/evidence-selection/SKILL.md internal/module/content/skills/evidence-selection/
	@cp skills/content-seed/SKILL.md internal/module/content/skills/content-seed/

test:
	go test ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

check: fmt vet test

install:
	go install $(PKG)

clean:
	$(RM) $(BINARY) $(BINARY).exe

start: build
	./$(BINARY) start

ui: build
	./$(BINARY) ui
