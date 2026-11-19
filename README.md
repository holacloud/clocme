# cloc.hola.cloud

Lines of code over time, per language, for any public GitHub repository, as an
embeddable SVG.

```markdown
[![Lines of code](https://cloc.hola.cloud/github.com/owner/repo)](https://cloc.hola.cloud/github.com/owner/repo)
```

The same URL serves the **SVG** to images/proxies (GitHub camo, `<img>`, curl)
and an **HTML page** with details and a chart builder to browsers.

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
| `format`  | `svg`, `html`, `json` (force response)   | negotiated|
| `interactive` | `1`: embeds chart geometry (`data-layout`) and scopes CSS, used by the web page tooltip | `0` |

Extensions also force the format: `/github.com/owner/repo.svg`, `.json`.

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

Runtime requirement: the `git` binary.

## Run

Everything runs in docker; data lives on the host (`./data`) and is mounted
as a volume, so it survives rebuilds and is owned by your user.

```sh
cp .env.example .env   # optional: GITHUBTOKEN, TELEGRAM_*, PORT, DATA_DIR, BASEURL...
make up                # build image + start on http://localhost:7070
make logs              # follow logs
make ps                # status / health
make down              # stop (data is kept)
make docker-test       # vet + tests inside a golang container
```

A `GITHUBTOKEN` is strongly recommended: without it the GitHub API allows
only 60 requests/hour (one per analysis).

Without docker: `make run`, `make test` (needs Go and git).

## Data layout

```
data/repos/      repo metadata (holacloud/store, disk + memory cache)
data/series/     one point per commit per repo (holacloud/store, disk)
data/clones/     bare git clones
data/blobcache/  per-blob line counts
```
