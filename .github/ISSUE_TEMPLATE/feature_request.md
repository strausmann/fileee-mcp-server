---
name: Feature-Request
about: Eine neue Funktion für fileee-mcp-server vorschlagen
title: "feat: "
labels: enhancement
---

## Problem / Motivation

Welches Problem löst der Vorschlag? Welches Tool oder welcher Anwendungsfall fehlt aktuell?

## Vorschlag

Wie könnte die Lösung aussehen (neues Tool, geänderte Parameter, neue Konfigurationsoption)?

## Einordnung: MCP-Server oder Core-Lib?

`fileee-mcp-server` delegiert jede Fileee-Operation an [`go-fileee`](https://github.com/strausmann/go-fileee).
Bitte kurz einordnen:

- [ ] Der Vorschlag betrifft nur den MCP-Server (Tool-Zuschnitt, Auth, Konto-Auflösung,
      Capabilities, Deployment) — passt in dieses Repo.
- [ ] Der Vorschlag braucht neue Fileee-Protokoll-Abdeckung (neue Entity, neuer Endpunkt bei
      `my.fileee.com`) — gehört ins
      [go-fileee-Repo](https://github.com/strausmann/go-fileee), nicht hierher.

## Capability-Gruppe

In welche Gruppe gehört die Funktion, falls es ein neues Tool ist?

- [ ] `read` — nur lesend
- [ ] `write` — legt an oder ändert
- [ ] `share` — teilt Inhalte oder exportiert
- [ ] `destructive` — löscht unwiderruflich

## Alternativen

Welche Alternativen wurden erwogen?

## Zusätzlicher Kontext

Links, verwandte Issues, Beispiele aus anderen MCP-Servern.
