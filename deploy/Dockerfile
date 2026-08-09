# syntax=docker/dockerfile:1
#
# fileee-mcp-server — Container-Abbild mit Infisical-CLI im Einstiegspunkt.
#
# Anders als der Geschwister-Dienst fileee-server hat dieser Server KEIN
# eingebautes Infisical-Backend (im Code existiert weder SECRET_BACKEND noch
# eine Infisical-Anbindung) — die Geheimnis-Zustellung laeuft deshalb ueber
# das externe Infisical-Werkzeug, das den eigentlichen Prozess umschliesst:
# `infisical run -- /usr/local/bin/fileee-mcp-server`. Deshalb ist die
# Laufzeit-Stufe bewusst KEIN distroless-Abbild, sondern ein schlankes
# Debian mit Shell — das Infisical-Werkzeug muss ausfuehrbar vorhanden sein,
# nicht bloss das Server-Binary.
#
# Siehe docs/betrieb/aufbau.md fuer den Bau-/Pruef-Ablauf.

# =============================================================================
# Bau-Stufe
# =============================================================================
# go.mod verlangt "go 1.25.0" ohne toolchain-Override -> golang:1.26-bookworm
# erfuellt das (1.26 >= 1.25), kein GOTOOLCHAIN-Zwangsupgrade noetig.
FROM golang:1.26-bookworm AS build

# Build-Metadaten fuer die Version (ldflags-Override, siehe
# internal/config/version.go) UND die OCI-Labels weiter unten. Defaults
# ("dev"/"unknown") gelten fuer einen lokalen `docker build .` ohne
# --build-arg — der CI-Release-Ablauf (.github/workflows/release.yml,
# docker-Job) setzt alle drei explizit aus dem semantic-release-Tag, der
# Git-SHA und der Build-Zeit (RFC3339).
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown

# Infisical-CLI-Version. Dieselbe verifizierte Version wie im
# Geschwister-Dienst fileee-server (deploy/Dockerfile) — dort wurde die
# Checksumme gegen die von Infisical veroeffentlichte checksums.txt
# geprueft UND die Statisch-Eigenschaft (ldd meldet "not a dynamic
# executable") bestaetigt. Diese Stufe verifiziert die Checksumme erneut
# beim Bau.
ARG INFISICAL_CLI_VERSION=0.41.90
ARG INFISICAL_CLI_SHA256=ff67e4b0f5fafe1b33ea1606767e6235a8742cc47e6af634cdff54b8d1fccc0a

WORKDIR /src

# Go-Modul-Cache zuerst kopieren (Docker-Layer-Caching: `go mod download`
# laeuft nur erneut, wenn sich go.mod/go.sum aendern, nicht bei jeder
# Quelltext-Aenderung).
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 erzwingt ein vollstatisches Go-Binary (kein libc-Link).
# -X setzt internal/config/version.go's ldflags-Override.
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -trimpath \
      -ldflags="-s -w -X github.com/strausmann/fileee-mcp-server/internal/config.version=${VERSION}" \
      -o /fileee-mcp-server ./cmd/fileee-mcp-server

# Statische Infisical-CLI holen (offizielles Release-Binary, kein
# Drittanbieter-Mirror), Checksumme verifizieren, bevor sie ins
# Laufzeit-Abbild wandert.
RUN set -eux; \
    curl -fsSL -o /tmp/infisical.tar.gz \
      "https://github.com/Infisical/infisical/releases/download/infisical-cli/v${INFISICAL_CLI_VERSION}/infisical_${INFISICAL_CLI_VERSION}_linux_amd64.tar.gz"; \
    echo "${INFISICAL_CLI_SHA256}  /tmp/infisical.tar.gz" | sha256sum -c -; \
    mkdir -p /tmp/infisical-extract; \
    tar -xzf /tmp/infisical.tar.gz -C /tmp/infisical-extract; \
    mv /tmp/infisical-extract/infisical /infisical; \
    chmod 755 /infisical

# =============================================================================
# Laufzeit-Stufe: schlankes Debian MIT Shell (kein distroless)
# =============================================================================
# Begruendung siehe Dateikopf: das Infisical-Werkzeug muss als ausfuehrbares
# Programm vorhanden sein und den Server-Prozess im Einstiegspunkt umschliessen
# koennen.
FROM debian:bookworm-slim

# ARGs muessen in JEDER Stage, die sie nutzt, neu deklariert werden
# (Docker-Multi-Stage-Regel) — der Bau-Stufen-Wert wird NICHT automatisch
# vererbt.
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown

# ca-certificates: Go-Binary UND Infisical-CLI brauchen das
# System-CA-Bundle fuer HTTPS (my.fileee.com bzw.
# secretsmanager.strausmann.cloud). Nicht-root-Benutzer 65532 passend zum
# Geschwister-Dienst fileee-server (dort distroless-"nonroot"-Vorgabe-UID) —
# hier manuell angelegt, da debian:bookworm-slim keinen solchen Benutzer
# mitbringt.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates; \
    rm -rf /var/lib/apt/lists/*; \
    groupadd --system --gid 65532 nonroot; \
    useradd --system --uid 65532 --gid 65532 --no-create-home --shell /usr/sbin/nologin nonroot

# OCI-Labels (HomeLab-Standard, siehe documentation-policy.md — ALLE Images
# tragen diese Labels). VERSION/REVISION/CREATED kommen aus den Build-ARGs
# oben (CI setzt sie explizit, siehe .github/workflows/release.yml
# docker-Job); title/description/licenses sind statisch.
LABEL org.opencontainers.image.source="https://github.com/strausmann/fileee-mcp-server" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.title="fileee-mcp-server" \
      org.opencontainers.image.description="MCP-Server (Streamable HTTP) fuer Fileee-Inhalte; tritt als OAuth-2.1-Resource-Server auf (Gangway-Unterbau, ADR-0015)."

COPY --from=build --chmod=755 /fileee-mcp-server /usr/local/bin/fileee-mcp-server
COPY --from=build --chmod=755 /infisical /usr/local/bin/infisical

USER nonroot

EXPOSE 8080

# Kein curl/wget im Abbild -> der Server bringt dafuer ein eigenes
# Unterkommando mit (cmd/fileee-mcp-server/main.go: `healthcheck` ruft
# MCP_HEALTHCHECK_URL ab, Vorgabe http://127.0.0.1:8080/healthz, und liefert
# Exit-Code 0/1 — exec-Form, kein Shell-Aufruf).
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/usr/local/bin/fileee-mcp-server", "healthcheck"]

# Infisical umschliesst den eigentlichen Server-Prozess: Geheimnisse werden
# von Infisical als Umgebungsvariablen injiziert, bevor fileee-mcp-server
# startet. Exec-Form (kein "sh -c") — infisical uebernimmt selbst den
# exec()-Wechsel in den Kindprozess.
ENTRYPOINT ["/usr/local/bin/infisical", "run", "--", "/usr/local/bin/fileee-mcp-server"]
