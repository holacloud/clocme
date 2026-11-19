#!/bin/bash
# CI/CD of clocme on hola cloud: the docker flavor of the usual ci.sh.
#
# Everything lives in this script's directory (e.g. /containers/clocme):
#
#   ci.sh         this script (copied from deploy/holacloud/ci.sh)
#   ci.env        settings and secrets of the CI (see ci.env.example)
#   deploy_key    read-only deploy key of the repo (PRIVATE_KEY_FILE)
#   config.json   clocme configuration (mounted read-only in the container)
#   data/         clocme data (mounted as /data)
#   ci/code/      git checkout managed by this script, do not edit
#   ci.log        output of the cron runs
#
# Run it once by hand, then every minute from cron:
#
#   * * * * * /containers/clocme/ci.sh >> /containers/clocme/ci.log 2>&1
#
# When the branch has new commits it builds the image and restarts the
# container. No systemd unit: docker restarts the container on failures and
# reboots (restart: unless-stopped). `ci.sh --force` redeploys the current
# code.

set -u

SCRIPT_FILENAME=$(readlink -f "$0")
SCRIPT_DIR=$(dirname "$SCRIPT_FILENAME")

source "${SCRIPT_DIR}/ci.env"

FORCE=NO
[ "${1:-}" == "--force" ] && FORCE=YES

# One run at a time: a build can take longer than the cron period.
exec 9>"${SCRIPT_DIR}/ci.lock"
if ! flock -n 9; then
  echo "$(date) previous run still in progress"
  exit 0
fi

notify() {
  echo "$(date) $1"
  if [ -n "${TELEGRAM_BOT:-}" ] && [ -n "${TELEGRAM_CHAT_ID:-}" ]; then
    curl -s -o /dev/null -H 'Content-Type: application/json' \
      -d "{\"chat_id\":\"${TELEGRAM_CHAT_ID}\",\"text\":\"$1\"}" \
      "https://api.telegram.org/bot${TELEGRAM_BOT}/sendMessage"
  fi
}

CI_DIR="${SCRIPT_DIR}/ci"
CODE_DIR="${CI_DIR}/code"
mkdir -p "${CI_DIR}"

# 1 # ensure repo

if [ -n "${PRIVATE_KEY_FILE:-}" ]; then
  chmod 0600 "${PRIVATE_KEY_FILE}"
  export GIT_SSH_COMMAND="ssh -i ${PRIVATE_KEY_FILE} -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new"
fi

DEPLOY=${FORCE}
if [ ! -d "${CODE_DIR}/.git" ]; then
  echo "$(date) Cloning ${REPO}..."
  if ! git clone --branch "${BRANCH}" "${REPO}" "${CODE_DIR}"; then
    notify "clone failed! ${REPO}:${BRANCH}"
    exit 1
  fi
  DEPLOY=YES
fi

if ! git -C "${CODE_DIR}" fetch --quiet origin "${BRANCH}"; then
  echo "$(date) fetch failed"
  exit 1
fi
LOCAL=$(git -C "${CODE_DIR}" rev-parse HEAD)
REMOTE=$(git -C "${CODE_DIR}" rev-parse "origin/${BRANCH}")

if [ "${LOCAL}" != "${REMOTE}" ]; then
  echo "$(date) New version ${LOCAL:0:7} -> ${REMOTE:0:7}"
  git -C "${CODE_DIR}" checkout --quiet --force "${BRANCH}"
  git -C "${CODE_DIR}" reset --quiet --hard "origin/${BRANCH}"
  DEPLOY=YES
fi

if [ "${DEPLOY}" != "YES" ]; then
  exit 0 # already updated (silent: this runs every minute)
fi

# 2 # host side: data dir and config, owned by the container user

RUN_UID=${RUN_UID:-1000}
RUN_GID=${RUN_GID:-1000}
DATA_DIR="${SCRIPT_DIR}/data"
CONFIG_FILE="${SCRIPT_DIR}/config.json"

if [ ! -f "${CONFIG_FILE}" ]; then
  cp "${CODE_DIR}/config.example.json" "${CONFIG_FILE}"
  notify "clocme: created ${CONFIG_FILE} from the example, fill it in (base_url, github_token...) and run ci.sh --force"
fi
mkdir -p "${DATA_DIR}"
if [ "$(id -u)" == "0" ]; then
  chown "${RUN_UID}:${RUN_GID}" "${DATA_DIR}" "${CONFIG_FILE}"
fi
chmod 0600 "${CONFIG_FILE}"

# 3 # build and restart

cd "${CODE_DIR}"
export VERSION=$(git describe --tags --always)
export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-clocme}"
export CONTAINER_NAME="${CONTAINER_NAME:-clocme}"
export IMAGE_NAME="${IMAGE_NAME:-clocme}"
export PORT="${PORT:-7070}"
export LISTEN="${LISTEN:-127.0.0.1}"
export DATA_DIR CONFIG_FILE

# UID is a read-only variable in bash: pass it through env(1).
compose() {
  env UID="${RUN_UID}" GID="${RUN_GID}" docker compose "$@"
}

echo "$(date) Building ${VERSION}..."
if ! compose build; then
  notify "build failed! ${REPO}:${BRANCH} ${VERSION}"
  exit 1
fi

echo "$(date) Starting ${VERSION}..."
if ! compose up -d --remove-orphans; then
  notify "start failed! ${REPO}:${BRANCH} ${VERSION}"
  exit 1
fi

# wait for the container healthcheck
STATUS=unknown
for _ in $(seq 1 30); do
  STATUS=$(docker inspect -f '{{.State.Health.Status}}' "${CONTAINER_NAME}" 2>/dev/null)
  [ "${STATUS}" == "healthy" ] && break
  sleep 2
done

if [ "${STATUS}" == "healthy" ]; then
  notify "new version ${REPO}:${BRANCH} ${VERSION}"
else
  notify "new version ${REPO}:${BRANCH} ${VERSION} is NOT healthy (${STATUS}), check: docker logs ${CONTAINER_NAME}"
fi

# 4 # cleanup: images of previous versions and dangling layers

docker images "${IMAGE_NAME}" --format '{{.Tag}}' | grep -vx "${VERSION}" \
  | xargs -r -I{} docker rmi "${IMAGE_NAME}:{}" >/dev/null 2>&1
docker image prune -f >/dev/null 2>&1
exit 0
