#!/bin/sh
set -eu

wiki_root="${WIKIOS_WIKI_ROOT:-/data/wiki-repo}"
wiki_url="${WIKIOS_WIKI_GIT_URL:-}"
wiki_remote="${WIKIOS_WIKI_GIT_REMOTE:-origin}"
wiki_branch="${WIKIOS_WIKI_GIT_BRANCH:-main}"
pull_on_start="${WIKIOS_WIKI_GIT_PULL_ON_START:-true}"
reset_on_start="${WIKIOS_WIKI_GIT_RESET_ON_START:-false}"
qmd_auto_collection="${WIKIOS_QMD_AUTO_COLLECTION:-true}"
qmd_index="${WIKIOS_QMD_INDEX:-zy-knowledge-base}"

log() {
  printf '[wikios-entrypoint] %s\n' "$*"
}

is_true() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|on) return 0 ;;
    *) return 1 ;;
  esac
}

dir_is_empty() {
  [ -d "$1" ] || return 0
  [ -z "$(find "$1" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]
}

sync_wiki_repo() {
  [ -n "$wiki_url" ] || return 0

  mkdir -p "$wiki_root"

  if dir_is_empty "$wiki_root"; then
    log "cloning wiki repo into $wiki_root"
    if [ -n "$wiki_branch" ]; then
      git clone --branch "$wiki_branch" "$wiki_url" "$wiki_root"
    else
      git clone "$wiki_url" "$wiki_root"
    fi
    return 0
  fi

  if [ ! -d "$wiki_root/.git" ]; then
    log "ERROR: $wiki_root is not empty and is not a git repository; refusing to overwrite it"
    exit 1
  fi

  if ! is_true "$pull_on_start"; then
    log "wiki git pull skipped by WIKIOS_WIKI_GIT_PULL_ON_START=$pull_on_start"
    return 0
  fi

  cd "$wiki_root"

  if git remote get-url "$wiki_remote" >/dev/null 2>&1; then
    current_url="$(git remote get-url "$wiki_remote")"
    if [ "$current_url" != "$wiki_url" ]; then
      log "updating git remote $wiki_remote"
      git remote set-url "$wiki_remote" "$wiki_url"
    fi
  else
    log "adding git remote $wiki_remote"
    git remote add "$wiki_remote" "$wiki_url"
  fi

  if [ -n "$wiki_branch" ]; then
    log "fetching $wiki_remote/$wiki_branch"
    git fetch "$wiki_remote" "$wiki_branch"
    if git rev-parse --verify "$wiki_branch" >/dev/null 2>&1; then
      git checkout "$wiki_branch"
    else
      git checkout -B "$wiki_branch" "$wiki_remote/$wiki_branch"
    fi
    git branch --set-upstream-to="$wiki_remote/$wiki_branch" "$wiki_branch" >/dev/null 2>&1 || true

    if is_true "$reset_on_start"; then
      log "resetting wiki repo to $wiki_remote/$wiki_branch"
      git reset --hard "$wiki_remote/$wiki_branch"
    else
      log "pulling wiki repo with --ff-only"
      git pull --ff-only "$wiki_remote" "$wiki_branch"
    fi
  else
    log "pulling current wiki branch with --ff-only"
    git pull --ff-only
  fi
}

sync_wiki_repo

if is_true "$qmd_auto_collection" && [ -d "$wiki_root/wiki" ]; then
  log "ensuring qmd collection 'wiki' exists"
  (
    cd "$wiki_root"
    qmd --index "$qmd_index" collection add wiki/ --name wiki >/dev/null 2>&1 || true
    qmd --index "$qmd_index" update >/dev/null 2>&1 || true
  )
fi

cd /app
exec "$@"
