# Deploy on hola cloud

The service is self-contained in `/containers/clocme`. A cron job runs
`ci.sh` every minute: when `master` has new commits it builds the docker
image and restarts the container. Docker keeps the container running
(`restart: unless-stopped`), so there is no systemd unit.

```
/containers/clocme/
  ci.sh          copy of deploy/holacloud/ci.sh
  ci.env         CI settings and secrets (from ci.env.example)
  deploy_key     optional: read-only deploy key, only for private repos
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

3. Deploy key (only for private repos; clocme is public and uses https):

   ```sh
   ssh-keygen -t ed25519 -N '' -C clocme-deploy -f /containers/clocme/deploy_key
   cat /containers/clocme/deploy_key.pub
   ```

   Add the public key in GitHub > repo Settings > Deploy keys (read-only,
   no write access) and set in `ci.env`:

   ```sh
   REPO=git@github.com:holacloud/clocme.git
   PRIVATE_KEY_FILE=/containers/clocme/deploy_key
   ```

   Switching an existing install from https to ssh: also update the remote
   of the checkout (or delete `ci/code` and let the next run clone again):

   ```sh
   git -C /containers/clocme/ci/code remote set-url origin git@github.com:holacloud/clocme.git
   ```

4. First run (clones, builds, starts with the example config):

   ```sh
   /containers/clocme/ci.sh
   ```

5. Fill `config.json`: at least `"base_url": "https://cloc.hola.cloud"` and
   `github_token`; optionally `telegram` and `instantlogs`. Apply it:

   ```sh
   /containers/clocme/ci.sh --force
   ```

6. Cron (`crontab -e`):

   ```
   * * * * * /containers/clocme/ci.sh >> /containers/clocme/ci.log 2>&1
   ```

7. Glue2 (TLS and certificate are automatic): route the host
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

## Reusing it for another docker service

`ci.sh` is generic: copy it to `/containers/<service>/` and the service name
(compose project, container, image) is taken from the directory name. The
repo's `docker-compose.yml` must honor the same variables as clocme's:
`IMAGE_NAME`, `CONTAINER_NAME`, `VERSION`, `UID`/`GID`, `PORT`, `LISTEN`,
`DATA_DIR`, `CONFIG_FILE`, and define a healthcheck (the deploy waits for
it). Use a different `PORT` per service.
