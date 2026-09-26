# Contest Management System

High-performance contest management system for IOI/OMI-style programming
contests (optional ICPC mode), written in Go. Functionally modelled after
[CMS](https://github.com/cms-dev/cms) and re-implemented from scratch.

Sistema de gestión de concursos de programación estilo IOI/OMI (con modo ICPC
opcional) de alto rendimiento, escrito en Go. Inspirado funcionalmente en CMS
y reimplementado desde cero.

## Try it locally (development) / Probarlo localmente (desarrollo)

```sh
make dev          # docker compose: postgres, valkey, minio + all services
# or / o bien
make dev-native   # native processes (needs PostgreSQL 16 + Redis/Valkey locally)
```

- Contestant portal / Portal del concursante: http://localhost:8888
- Admin / Administración: http://localhost:8889 (development credentials printed by
  `make dev`; the first login asks for a new password / credenciales de desarrollo
  que imprime `make dev`; el primer inicio de sesión pide una contraseña nueva)
- Ranking: http://localhost:8890

## Production / Producción

Server / Servidor: **Linux** (Ubuntu 22.04/24.04/26.04, Debian 12/13) on a **KVM
virtual machine or a dedicated server**, **root**, **control groups v2**,
**amd64 or arm64**. Not for judging: Windows, macOS, OpenVZ/LXC container
VPSs, serverless platforms. A 2 vCPU / 4 GB server handles about 500
contestants ([sizes / tamaños](docs/en/deployment.md#sizes-by-number-of-contestants)).

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System/main/scripts/install.sh \
  | sudo bash -s -- --domain cms.example.org --email you@example.org
```

It checks the machine, downloads the release verifying its SHA-256
checksum, installs everything, prints the administrator's random password
once and runs `cms-verify-host`. / Comprueba la máquina, descarga la versión
verificando su suma SHA-256, instala todo, muestra una vez la contraseña
aleatoria del administrador y corre `cms-verify-host`.

- Upgrade / Actualizar: `sudo cmsctl upgrade` (backup, migrations, automatic
  rollback / respaldo, migraciones, vuelta atrás automática).
- Uninstall / Desinstalar: `... | sudo bash -s -- --uninstall [--purge]`.
- Docker Compose: [deploy/docker](deploy/docker/) · [en](docs/en/docker.md) · [es](docs/es/docker.md).
- Step by step / Paso a paso: [docs/en/deployment.md](docs/en/deployment.md) ·
  [docs/es/despliegue.md](docs/es/despliegue.md). Contest-day runbook:
  [en](docs/en/contest-day.md) · [es](docs/es/dia-del-concurso.md).
- Releases (binaries for linux/amd64 and arm64, checksums, container images on
  GHCR): [GitHub releases](https://github.com/D4ND3R/Contest-Management-System/releases).

## Documentation / Documentación

- English: [docs/en](docs/en/README.md)
- Español: [docs/es](docs/es/README.md)
- Project status / Estado del proyecto: [SUMMARY.md](SUMMARY.md) (what is
  complete and what to verify on the real server),
  [loadtest/README.md](loadtest/README.md) (measured capacity)

## Development / Desarrollo

```sh
make test         # full test suite (starts throwaway PostgreSQL/Redis)
make lint
make test-sandbox # sandbox security battery (root + isolate)
```

See `CLAUDE.md` for the repository layout and conventions, `SPEC.md` for the
requirements, `PROGRESS.md` for the status of each phase and `DECISIONS.md`
for design decisions.

## License / Licencia

[Apache License 2.0](LICENSE) (see [NOTICE](NOTICE)). An independent
implementation: no code from CMS (AGPL-3.0) is included. /
Licencia Apache 2.0; implementación independiente, sin código de CMS.
