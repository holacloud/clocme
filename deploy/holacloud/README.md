# Deploy on hola cloud

The service is self-contained in `/containers/clocme`. A cron job runs
`ci.sh` every minute: when `master` has new commits it builds the docker
image and restarts the container. Docker keeps the container running
(`restart: unless-stopped`), so there is no systemd unit.

```
/containers/clocme/
  ci.sh          copy of deploy/holacloud/ci.sh
  ci.env         CI settings and secrets (from ci.env.example)
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

2. Check that the port is free (no output = free), otherwise change `PORT`
   in `ci.env`:

   ```sh
   ss -ltn 'sport = :7070'
   ```

   The repo is public and is cloned over https. A private repo would need
   an ssh `REPO` and a read-only deploy key in `PRIVATE_KEY_FILE`.

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

6. Glue2 (TLS and certificate are automatic): route the host
   `cloc.hola.cloud` to `http://127.0.0.1:7070` (`PORT` in `ci.env`). The
   port only listens on loopback (`LISTEN`), so Glue2 must run on the host.
   The route must be public (no Glue2 login) and forward the `Accept` and
   `Sec-Fetch-Dest` headers: the same URL serves the SVG or the HTML page
   depending on them.

## Operations

| What | How |
|---|---|
| Deploy now / apply `config.json` changes | `ci.sh --force` |
| Logs | `docker logs -f clocme` |
| Status / health | `docker ps --filter name=clocme` |
| Stop | `cd ci/code && docker compose -p clocme down` (cron would start it again only on new commits or `--force`) |
| CI history | `tail -f ci.log` |
