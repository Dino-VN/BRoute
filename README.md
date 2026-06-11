# BRoute

BRoute is a local routing console for provider accounts and API access.

## Install

```bash
npx broute-cli
```

The launcher downloads the latest GitHub release into `~/.broute` and starts the bundled server and web UI.

## Update

```bash
npx broute-cli update
```

The in-app sidebar also checks GitHub releases and can trigger the same update flow.
After an in-app update finishes, BRoute restarts itself and the web console reloads when the server is back online.

## Data

By default BRoute stores runtime files in `~/.broute`. On first run it creates `.env` with `JWT_SECRET`, `STORAGE_ENCRYPTION_KEY`, and `STORAGE_ENCRYPTION_KEY_VERSION` when missing.
