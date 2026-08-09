## [0.4.0](https://github.com/strausmann/fileee-mcp-server/compare/v0.3.0...v0.4.0) (2026-08-09)

### Features

* **config:** ein variablen-namensraum je identity provider ([9c05414](https://github.com/strausmann/fileee-mcp-server/commit/9c05414b9e36a54d4905eb36f68e8f9e516efe0f)), closes [#286](https://github.com/strausmann/fileee-mcp-server/issues/286)

### Bug Fixes

* **deploy:** start ohne infisical ermoeglichen, halbes zugangsdaten-paar abweisen ([4cba291](https://github.com/strausmann/fileee-mcp-server/commit/4cba2918ac2a4086f85f85b8d88bf101ced6fc76)), closes [#286](https://github.com/strausmann/fileee-mcp-server/issues/286)

## [0.3.0](https://github.com/strausmann/fileee-mcp-server/compare/v0.2.0...v0.3.0) (2026-08-09)

### Features

* **deploy:** bau-anweisung und healthcheck-unterbefehl fuer das abbild ([#32](https://github.com/strausmann/fileee-mcp-server/issues/32)) ([2bb8e90](https://github.com/strausmann/fileee-mcp-server/commit/2bb8e90b3378f8c112e2c679da7fbbc051390a5d))

### Bug Fixes

* **server:** mcp_oidc_required_scopes tatsaechlich durchgesetzt ([#29](https://github.com/strausmann/fileee-mcp-server/issues/29)) ([6641aea](https://github.com/strausmann/fileee-mcp-server/commit/6641aeab2e0808f3f1d25856a62a30699a34cd08))
* **server:** rate-limit- und inflight-einstellungen tatsaechlich durchgesetzt ([#30](https://github.com/strausmann/fileee-mcp-server/issues/30)) ([da0da84](https://github.com/strausmann/fileee-mcp-server/commit/da0da8427edbc2193b33ff357e16096428ba75a9))

## [0.2.0](https://github.com/strausmann/fileee-mcp-server/compare/v0.1.0...v0.2.0) (2026-08-09)

### Features

* **accounts:** Zuordnung von Identität auf Fileee-Konto ([#19](https://github.com/strausmann/fileee-mcp-server/issues/19)) ([dee76a8](https://github.com/strausmann/fileee-mcp-server/commit/dee76a8933755359edde7f3b11414c3b698c7013))
* **clientpool:** verbindungspool je konto mit vereinzeltem login ([#20](https://github.com/strausmann/fileee-mcp-server/issues/20)) ([11e39b2](https://github.com/strausmann/fileee-mcp-server/commit/11e39b292e57053e520a59c609616cb06183f7cb))
* **config:** env-matrix und capability-aufloesung mit fail-fast-validierung ([#9](https://github.com/strausmann/fileee-mcp-server/issues/9)) ([9b15519](https://github.com/strausmann/fileee-mcp-server/commit/9b15519fe78a0278615b75ce795f2b1062bf9602))
* **server:** berechtigungsstufen ueber getrennte werkzeugkataloge eingefuehrt ([#23](https://github.com/strausmann/fileee-mcp-server/issues/23)) ([5de95f8](https://github.com/strausmann/fileee-mcp-server/commit/5de95f81b004989c8fb20e627ccd20afa2762374))
* **server:** Gangway als Unterbau eingebunden, Server startet und weist ab ([#17](https://github.com/strausmann/fileee-mcp-server/issues/17)) ([f708231](https://github.com/strausmann/fileee-mcp-server/commit/f708231417eccadc17e2d7420597ac2f5fbccbd0))
* **tools:** erste lesende Werkzeuge, je Anrufer getrennt ([#22](https://github.com/strausmann/fileee-mcp-server/issues/22)) ([1609ee8](https://github.com/strausmann/fileee-mcp-server/commit/1609ee8d6a3d33b64c87168e07b6e9ba0af877bc))

## [0.1.0](https://github.com/strausmann/fileee-mcp-server/compare/v0.0.0...v0.1.0) (2026-08-07)

### Features

* **server:** repo-geruest mit tooling, ci und idp-anleitungen ([#1](https://github.com/strausmann/fileee-mcp-server/issues/1)) ([ea89d17](https://github.com/strausmann/fileee-mcp-server/commit/ea89d1757532645b36c0ec36ecf55aa14b768cde))
