# Contest Management System

High-performance contest management system for IOI/OMI-style programming
contests (optional ICPC mode), written in Go. Functionally modelled after
[CMS](https://github.com/cms-dev/cms) and re-implemented from scratch.

Sistema de gestión de concursos de programación estilo IOI/OMI (con modo ICPC
opcional) de alto rendimiento, escrito en Go. Inspirado funcionalmente en CMS
y reimplementado desde cero.

## Quick start / Inicio rápido

```sh
make dev          # docker compose: postgres, valkey, minio + all services
# or / o bien
make dev-native   # native processes (needs PostgreSQL 16 + Redis/Valkey locally)
```

- Contestant portal / Portal del concursante: http://localhost:8888
- Admin / Administración: http://localhost:8889 (admin / admin)
- Ranking: http://localhost:8890

## Production / Producción

```sh
make build && sudo scripts/install.sh --domain cms.example.org --email you@example.org
sudo cms-verify-host --config /etc/cms/cms.yaml
```

Step by step: [docs/en/deployment.md](docs/en/deployment.md) ·
[docs/es/despliegue.md](docs/es/despliegue.md). Contest-day runbook:
[en](docs/en/contest-day.md) · [es](docs/es/dia-del-concurso.md).

## Documentation / Documentación

- English: [docs/en](docs/en/README.md)
- Español: [docs/es](docs/es/README.md)

## Development / Desarrollo

```sh
make test         # full test suite (starts throwaway PostgreSQL/Redis)
make lint
make test-sandbox # sandbox security battery (root + isolate)
```

See `CLAUDE.md` for the repository layout and conventions, `SPEC.md` for the
requirements, `PROGRESS.md` for the status of each phase and `DECISIONS.md`
for design decisions.
