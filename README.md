# cloc.hola.cloud

Lines of code over time, per language, for any public GitHub repository, as an
embeddable SVG.

```markdown
[![Lines of code](https://cloc.hola.cloud/github.com/owner/repo)](https://cloc.hola.cloud/github.com/owner/repo)
```

The same URL serves the **SVG** to images/proxies (GitHub camo, `<img>`, curl)
and an **HTML page** with details and a chart builder to browsers.

`https://cloc.hola.cloud/github.com/owner` shows every public repository of a
user or organization (forks and empty repos excluded, up to
`maxownerrepos`). The first visit lists them on GitHub and queues them all in
a low priority queue (most starred first), so single repo requests are never
delayed by a big organization. Cards fill in as charts are ready.

## Chart options

| Param     | Values                                   | Default   |
|-----------|------------------------------------------|-----------|
| `top`     | number of languages, `0` = all           | `5`       |
| `x`       | `commits`, `date`                        | `commits` |
| `mode`    | `stacked`, `percent`, `lines`            | `stacked` |
| `metric`  | `code`, `lines`, `comments`, `files`     | `code`    |
| `exclude` | comma-separated languages                |           |
| `include` | data languages to show (`JSON`...), `all`|           |
| `theme`   | `auto`, `light`, `dark`                  | `auto`    |
| `w`, `h`  | size in px                               | `800x400` |
| `title`, `legend` | `0` to hide                      | `1`       |
| `format`  | `svg`, `html`, `json`, `png` (force response) | negotiated|
| `interactive` | `1`: embeds chart geometry (`data-layout`) and scopes CSS, used by the web page tooltip | `0` |

Extensions also force the format: `/github.com/owner/repo.svg`, `.json`, `.png`.

`.png` is the social preview (`og:image`, `twitter:image`): 1200x630, the
chart laid out at 800x420 and scaled x1.5 so text stays readable in
previews. It needs `rsvg-convert` (included in the docker image); without it
PNG endpoints answer 501 and pages omit `og:image`.

## How it works

1. Request → the repo is registered and queued (requests never wait for git).
2. A worker asks the GitHub API for the default branch and size. Repos over
   `max_repo_size_mb` are flagged and the operator gets a Telegram message.
3. Bare clone the first time, `git fetch` afterwards (only new objects).
4. Walk the first-parent history of the default branch; for each commit only
   the changed blobs are counted with [scc](https://github.com/boyter/scc).
   Counts are cached per blob, so incremental refreshes are cheap.
5. Vendored, generated and documentation files are excluded following GitHub
   Linguist rules, including `linguist-*` attributes in `.gitattributes`.
   Data languages (JSON, YAML, Markdown…) are counted but hidden by default.

Content negotiation: HTML when `Accept` contains `text/html` and
`Sec-Fetch-Dest` is `document` or absent; SVG otherwise.

## Dependencies

| Library | Why |
|---|---|
| `github.com/boyter/scc/v3` | Line counting per language (code/comments/blanks), fast, usable as a library |
| `github.com/fulldump/box` | HTTP router with interceptors |
| `github.com/fulldump/goconfig` | Config from file, env and flags |
| `github.com/holacloud/store` | Persistence (disk JSON + memory cache) |
| Vue 3.5.13 (vendored, `api/static/vendor`) | Dynamic parts of the page (option builder, status polling) |
| Pico CSS 2.0.6 (vendored) | Classless CSS, built-in dark mode |
| Devicon 2.16.0 icons (subset vendored, `api/static/icons/lang`, MIT) | Language icons in the language bar |
| `rsvg-convert` (librsvg, system binary) | SVG to PNG for social previews; pure Go rasterizers do not render text |

Runtime requirements: `git`, and optionally `rsvg-convert` + a font for PNG previews.

## Run

Everything runs in docker. Data (`./data`) and configuration
(`./config.json`) live on the host and are mounted into the container.

```sh
make up                # creates config.json from config.example.json if missing, builds, starts on :7070
$EDITOR config.json    # github_token, telegram, limits...
make restart           # apply config changes (recreates the container, no rebuild)
make logs              # follow logs
make ps                # status / health
make down              # stop (data is kept)
make docker-test       # vet + tests inside a golang container
```

Configuration is read by goconfig from `config.json` in the working
directory (`/app` in the container). Keys are the `json` tags of
`config/config.go`; see `config.example.json`. Precedence: flags > env vars
> config.json > defaults, so do not set app settings as environment
variables in docker-compose or they will override the file. `data_dir` and
`addr` are fixed by the image (`/data`, `:8080`).

Optional docker-side settings (host `PORT`, `DATA_DIR`, `CONFIG_FILE`) can go
in `.env`, see `.env.example`.

A `github_token` is strongly recommended: without it the GitHub API allows
only 60 requests/hour (one per analysis). A fine-grained token with
"Public repositories (read-only)" and no permissions is enough.

Without docker: `make run`, `make test` (needs Go and git; `config.json` in
the repo root is picked up too).

## Data layout

```
data/repos/      repo metadata (holacloud/store, disk + memory cache)
data/series/     one point per commit per repo (holacloud/store, disk)
data/clones/     bare git clones
data/blobcache/  per-blob line counts
```
