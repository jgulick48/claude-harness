The web UI is embedded from `web/` here. `make web` (or the Dockerfile)
builds it from `ui/` into `internal/webserve/dist/web`, which git ignores.
This file keeps the directory embeddable when the UI isn't built.
