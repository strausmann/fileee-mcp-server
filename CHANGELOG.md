## [0.7.0](https://github.com/strausmann/fileee-mcp-server/compare/v0.6.0...v0.7.0) (2026-08-24)

### Features

* **tools:** add box_add_document and box_remove_document write tools ([5aa92c1](https://github.com/strausmann/fileee-mcp-server/commit/5aa92c1514d67910ecba6110a2e3e5f0dd7143f3))
* **tools:** add create_contact write tool ([094e967](https://github.com/strausmann/fileee-mcp-server/commit/094e967f1336b4c7e8dd63a72316fe1d112d83fd))
* **tools:** add create_reminder and update_reminder write tools ([4d2d052](https://github.com/strausmann/fileee-mcp-server/commit/4d2d052ffae6853f06490461531c5e7e3aa32788))
* **tools:** add update_contact write tool (patch/merge) and write.go registration ([65b5ac3](https://github.com/strausmann/fileee-mcp-server/commit/65b5ac310de2b515dca3137de31c8d6b406baa97))
* **tools:** add update_document write tool (title patch/merge) ([11bf3a4](https://github.com/strausmann/fileee-mcp-server/commit/11bf3a4f09e59fe21d86c0ba095b3488b7866356))
* **tools:** add upload_document write tool ([c74ba4b](https://github.com/strausmann/fileee-mcp-server/commit/c74ba4be0830784195549a5dc295ea6c086d7ef8))
* **tools:** add whoami meta-tool ([#63](https://github.com/strausmann/fileee-mcp-server/issues/63)) ([f09fba5](https://github.com/strausmann/fileee-mcp-server/commit/f09fba5906e148535a27a6c521d54770c01bb8eb))
* **tools:** rename RegisterRead->RegisterAll and add a title annotation to every tool ([f124bba](https://github.com/strausmann/fileee-mcp-server/commit/f124bbaf8c603ae12c846c862caae144291c7e06))

### Bug Fixes

* **config:** saturate max request body size instead of overflowing ([e225f59](https://github.com/strausmann/fileee-mcp-server/commit/e225f595cc48e08ac94bcb71dabc641dc07f0a95))
* **config:** saturate only when the derived body size cannot fit ([8625ac5](https://github.com/strausmann/fileee-mcp-server/commit/8625ac5b62f60b65c5a4778d9bdc8ab14b93fc4d))
* **tools:** create untrusted boundary before persisting mutations ([5d6918e](https://github.com/strausmann/fileee-mcp-server/commit/5d6918e352cb2dd893ec72b9b68925a286d6ea48))
* **tools:** drop deleted capabilities.go from CI gate, fix stale gating claim ([55a1916](https://github.com/strausmann/fileee-mcp-server/commit/55a1916c203ba532dd334769deffd2cda863c68d))
* **tools:** drop firstName/lastName required from create_contact schema ([b26a13b](https://github.com/strausmann/fileee-mcp-server/commit/b26a13b7fe21f5e1902b98fa1f11b0a8d0b63ec3))
* **tools:** enforce configured upload size limit in upload_document ([fe54289](https://github.com/strausmann/fileee-mcp-server/commit/fe54289e188f8fb6a4ab9daeda176bba0e7b9f77))
* **tools:** guard upload_document against nil upload result ([ff3b8c4](https://github.com/strausmann/fileee-mcp-server/commit/ff3b8c440f0f9e6271616c70b312c5e78d246114))
* **tools:** manifest title, tool counts, scope wording, tools.md ADR-0018 ([e05b5b3](https://github.com/strausmann/fileee-mcp-server/commit/e05b5b3343d13c68db9062695a0289abd5ba80bb))
* **tools:** report write tools as kind=write in get_tool_manifest ([3b6edb1](https://github.com/strausmann/fileee-mcp-server/commit/3b6edb170261dc4ae33c94403b00d335beeacb5e))
* **tools:** saturate encoded upload ceiling instead of overflowing ([49230fc](https://github.com/strausmann/fileee-mcp-server/commit/49230fc28e8d50c13a1a6059453cdc588917c69c))
* **tools:** stop promising unverified backend behaviour in box_remove_document ([b141d3e](https://github.com/strausmann/fileee-mcp-server/commit/b141d3e6ec547160476b0e8142dd36aa2d57d380))

## [0.6.0](https://github.com/strausmann/fileee-mcp-server/compare/v0.5.1...v0.6.0) (2026-08-13)

### Features

* **server:** Diagnose-Protokoll für Werkzeugaufrufe und Fähigkeits-Auflösung ([#43](https://github.com/strausmann/fileee-mcp-server/issues/43)) ([e90d49c](https://github.com/strausmann/fileee-mcp-server/commit/e90d49cfe178546732b36e565f8b63e2886a2379))
* **tools:** Abgleich fuer alle sieben generischen Dienste ([#46](https://github.com/strausmann/fileee-mcp-server/issues/46)) ([85a0ae5](https://github.com/strausmann/fileee-mcp-server/commit/85a0ae553ac9bb891b70a159f0916f6ce9644f12)), closes [#26](https://github.com/strausmann/fileee-mcp-server/issues/26)
* **tools:** boxen, binaerdaten mit obergrenze, seiten-ocr, kontostand ([#55](https://github.com/strausmann/fileee-mcp-server/issues/55)) ([02b0ba9](https://github.com/strausmann/fileee-mcp-server/commit/02b0ba9821c63adbd9320d8edc9eae9126baebb3)), closes [#53](https://github.com/strausmann/fileee-mcp-server/issues/53) [#53](https://github.com/strausmann/fileee-mcp-server/issues/53) [#26](https://github.com/strausmann/fileee-mcp-server/issues/26)
* **tools:** generischer Registrierungs-Helfer fuer ReadService ([#45](https://github.com/strausmann/fileee-mcp-server/issues/45)) ([ed6c807](https://github.com/strausmann/fileee-mcp-server/commit/ed6c807bb60855b34711b3d02fe5c14f9ec4d0be)), closes [#26](https://github.com/strausmann/fileee-mcp-server/issues/26)
* **tools:** get_document, sync_documents und Konversationen je Dokument ([#53](https://github.com/strausmann/fileee-mcp-server/issues/53)) ([a0c323c](https://github.com/strausmann/fileee-mcp-server/commit/a0c323c73ae202b0ca06a9c4fa755f6783835925)), closes [#26](https://github.com/strausmann/fileee-mcp-server/issues/26)
* **tools:** get_runtime_stats und get_tool_manifest (Aufgabe C1+C2) ([#57](https://github.com/strausmann/fileee-mcp-server/issues/57)) ([10047a2](https://github.com/strausmann/fileee-mcp-server/commit/10047a27b8c62b18b91f46127fc027bc03e66fdb)), closes [#47](https://github.com/strausmann/fileee-mcp-server/issues/47) [#186](https://github.com/strausmann/fileee-mcp-server/issues/186) [#48](https://github.com/strausmann/fileee-mcp-server/issues/48)
* **tools:** logger durch registerReadService und registerSync reichen ([#47](https://github.com/strausmann/fileee-mcp-server/issues/47)) ([553f9bb](https://github.com/strausmann/fileee-mcp-server/commit/553f9bb04cb93ebcd2c705bc6bb0e95a781af327)), closes [strausmann/fileee-mcp-server#45](https://github.com/strausmann/fileee-mcp-server/issues/45) [strausmann/fileee-mcp-server#46](https://github.com/strausmann/fileee-mcp-server/issues/46)
* **tools:** self_check -- selbsttest mit getrennten zustaenden (Aufgabe C3) ([ea44711](https://github.com/strausmann/fileee-mcp-server/commit/ea44711f74d1f0f6810d1bb9ee023a653ce23d17)), closes [#26](https://github.com/strausmann/fileee-mcp-server/issues/26)
* **tools:** stammdaten-Werkzeuge fuer Schlagworte, Firmen, Dokumenttypen ([#48](https://github.com/strausmann/fileee-mcp-server/issues/48)) ([a3a7f33](https://github.com/strausmann/fileee-mcp-server/commit/a3a7f339077eb90b8cdd5084680eb54eabd0f0bd)), closes [#26](https://github.com/strausmann/fileee-mcp-server/issues/26)
* **tools:** werkzeuge fuer Kontakte, Erinnerungen und Konversationen ([#52](https://github.com/strausmann/fileee-mcp-server/issues/52)) ([7513c73](https://github.com/strausmann/fileee-mcp-server/commit/7513c73882b4710e285ccb479ec524020ad84d09)), closes [#26](https://github.com/strausmann/fileee-mcp-server/issues/26)

## [0.5.1](https://github.com/strausmann/fileee-mcp-server/compare/v0.5.0...v0.5.1) (2026-08-11)

### Bug Fixes

* **deps:** update module golang.org/x/sync to v0.22.0 ([#25](https://github.com/strausmann/fileee-mcp-server/issues/25)) ([7a955d4](https://github.com/strausmann/fileee-mcp-server/commit/7a955d40e432de28acda85c373188621765a8465))

## [0.5.0](https://github.com/strausmann/fileee-mcp-server/compare/v0.4.1...v0.5.0) (2026-08-10)

### Features

* MCP_OIDC_ADVERTISED_SCOPES getrennt von REQUIRED_SCOPES durchreichen ([#41](https://github.com/strausmann/fileee-mcp-server/issues/41)) ([80807e3](https://github.com/strausmann/fileee-mcp-server/commit/80807e3b20b6bf5c1a1177acbdd208b309ecbd5e))

## [0.4.1](https://github.com/strausmann/fileee-mcp-server/compare/v0.4.0...v0.4.1) (2026-08-09)

### Bug Fixes

* **server:** verlangte scopes an gangway durchreichen ([#40](https://github.com/strausmann/fileee-mcp-server/issues/40)) ([a35123e](https://github.com/strausmann/fileee-mcp-server/commit/a35123ecaf6d3aba8eeb09a7703485726979a4f7))

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
