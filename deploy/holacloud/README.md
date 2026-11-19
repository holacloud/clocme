# Deploy on hola cloud

The service is self-contained in `/containers/clocme`. A cron job runs
`ci.sh` every minute: when `master` has new commits it builds the docker
image and restarts the container. Docker keeps the container running
(`restart: unless-stopped`), so there is no systemd unit.

```
/containers/clocme/
  ci.sh          copy of deploy/holacloud/ci.sh
  ci.env         CI settings and secrets (from ci.env.example)
  deploy_key     read-only deploy key for the repo
  config.json    clocme configuration (created from config.example.json on the first run)
  data/          clones, series, caches (docker volume)
  ci/code/       checkout managed by ci.sh
  ci.log         cron output
```

## First install

Requirements on the server: docker with the compose plugin, git, curl, flock.

1. Folder, script and settings:

   ```sh
   mkdir -p /containers/clocme && cd /containers/clocme
   curl -fsSL -o ci.sh https://raw.githubusercontent.com/holacloud/clocme/master/deploy/holacloud/ci.sh   # or scp it
   chmod +x ci.sh
   # create ci.env from deploy/holacloud/ci.env.example
   ```

2. Deploy key (only if the repo is private):

   ```sh
   ssh-keygen -t ed25519 -N '' -C clocme-deploy -f /containers/clocme/deploy_key
   cat /containers/clocme/deploy_key.pub   # GitHub > repo Settings > Deploy keys (read-only)
   ```

3. First run (clones, builds, starts with the example config):

   ```sh
   /containers/clocme/ci.sh
   ```

4. Fill `config.json`: at least `"base_url": "https://cloc.hola.cloud"` and
   `github_token`; optionally `telegram` and `instantlogs`. Apply it:

   ```sh
   /containers/clocme/ci.sh --force
   ```

5. Cron (`crontab -e`):

   ```
   * * * * * /containers/clocme/ci.sh >> /containers/clocme/ci.log 2>&1
   ```

6. Reverse proxy: `cloc.hola.cloud` -> `http://127.0.0.1:7070` (`PORT` in
   `ci.env`). The port only listens on loopback (`LISTEN`).

## Operations

| What | How |
|---|---|
| Deploy now / apply `config.json` changes | `ci.sh --force` |
| Logs | `docker logs -f clocme` |
| Status / health | `docker ps --filter name=clocme` |
| Stop | `cd ci/code && docker compose -p clocme down` (cron would start it again only on new commits or `--force`) |
| CI history | `tail -f ci.log` |
