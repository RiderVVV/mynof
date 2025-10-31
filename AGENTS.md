# Repository Guidelines

## Project Structure & Module Organization
- `main.go` bootstraps the trading manager, loads configs, and spawns the HTTP API.
- `config/`, `manager/`, `trader/`, `market/`, and `decision/` hold the Go core: configuration loading, multi-trader orchestration, exchange clients, market data helpers, and AI decision logic respectively.
- `api/` exposes Gin-based endpoints consumed by the dashboard; keep new handlers thin and delegate to managers.
- `pool/` manages shared coin lists; be mindful of concurrent access when extending it.
- `web/` is a Vite + React + TypeScript dashboard. Components live in `web/src/components`, shared stores in `web/src/contexts`, and styling in Tailwind-powered `web/src/index.css`.
- Deployment assets live under `docker/`, `nginx/`, and `pm2.*`; updating them requires matching documentation tweaks in `DOCKER_DEPLOY*.md`.

## Build, Test, and Development Commands
- `go run ./main.go config.json` starts the full backend with the provided config.
- `go build ./...` verifies the Go services compile; use before opening a PR.
- `go test ./...` should pass; add table-driven `_test.go` coverage for new logic.
- `cd web && npm install` sets up the dashboard; run once per dependency change.
- `cd web && npm run dev` launches the local UI against the running API.
- `cd web && npm run build` creates the production bundle; run when touching UI build config.

## Coding Style & Naming Conventions
- Format Go code with `gofmt`; use tabs (default) and keep functions focused. Exported symbols use PascalCase, package-private elements use camelCase.
- Prefer Go standard library patterns: return `(value, error)`, accept `context.Context` first when introducing long-running tasks.
- In TypeScript, keep component files PascalCase (`CompetitionPage.tsx`) and hooks prefixed with `use`. Co-locate utility modules under `web/src/utils` and type declarations under `web/src/types`.

## Testing Guidelines
- Extend backend coverage with deterministic unit tests; mock exchange clients instead of hitting live APIs.
- The dashboard currently lacks automated tests; add Vitest + React Testing Library suites when introducing complex UI state and document any new scripts.
- Capture regression cases in fixtures under `decision/` or `manager/` rather than hard-coding sample data.

## Commit & Pull Request Guidelines
- Follow the existing log style: short imperative subjects with optional scopes (`UI:`, `Docs:`, `feat:`) and clear English summaries; include Chinese context only when essential.
- Group related changes per commit, update `config.json.example` and docs when altering configuration fields, and avoid bundling formatting-only diffs with logic.
- PRs should describe behavior changes, link issues when available, and attach UI screenshots or API samples whenever the interface changes. Highlight any breaking config migrations and mention required environment secrets.
