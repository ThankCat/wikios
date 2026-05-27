#!/bin/sh
set -eu

wiki_root="${WIKIOS_WIKI_ROOT:-/data/wiki-repo}"
wiki_url="${WIKIOS_WIKI_GIT_URL:-}"
wiki_token="${WIKIOS_WIKI_GIT_TOKEN:-}"
wiki_username="${WIKIOS_WIKI_GIT_USERNAME:-x-access-token}"
wiki_remote="${WIKIOS_WIKI_GIT_REMOTE:-origin}"
wiki_branch="${WIKIOS_WIKI_GIT_BRANCH:-main}"
pull_on_start="${WIKIOS_WIKI_GIT_PULL_ON_START:-true}"
reset_on_start="${WIKIOS_WIKI_GIT_RESET_ON_START:-false}"
qmd_auto_collection="${WIKIOS_QMD_AUTO_COLLECTION:-true}"
qmd_index="${WIKIOS_QMD_INDEX:-knowledge-base}"

log() {
  printf '[wikios-entrypoint] %s\n' "$*"
}

redact_url() {
  printf '%s' "$1" | sed -E 's#(https?://)[^/@]+@#\1redacted@#'
}

setup_git_noninteractive() {
  ssh_cmd="${GIT_SSH_COMMAND:-ssh}"
  case "$ssh_cmd" in *BatchMode*) ;; *) ssh_cmd="$ssh_cmd -o BatchMode=yes" ;; esac
  case "$ssh_cmd" in *NumberOfPasswordPrompts*) ;; *) ssh_cmd="$ssh_cmd -o NumberOfPasswordPrompts=0" ;; esac
  export GIT_TERMINAL_PROMPT=0
  export GCM_INTERACTIVE=never
  export SSH_ASKPASS=/bin/false
  export SSH_ASKPASS_REQUIRE=never
  export GIT_SSH_COMMAND="$ssh_cmd"

  if [ -z "$wiki_token" ]; then
    export GIT_ASKPASS=/bin/false
    return 0
  fi

  askpass_dir="$(mktemp -d)"
  askpass_path="$askpass_dir/askpass.sh"
  cat > "$askpass_path" <<'EOF'
#!/bin/sh
case "$1" in
  *sername*|*Username*) printf '%s\n' "$WIKIOS_GIT_ASKPASS_USERNAME" ;;
  *) printf '%s\n' "$WIKIOS_GIT_ASKPASS_TOKEN" ;;
esac
EOF
  chmod 700 "$askpass_path"
  export GIT_ASKPASS="$askpass_path"
  export WIKIOS_GIT_ASKPASS_USERNAME="$wiki_username"
  export WIKIOS_GIT_ASKPASS_TOKEN="$wiki_token"
}

cleanup_git_noninteractive() {
  if [ -n "${askpass_dir:-}" ] && [ -d "$askpass_dir" ]; then
    rm -rf "$askpass_dir"
  fi
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
    log "cloning wiki repo $(redact_url "$wiki_url") into $wiki_root"
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
      log "updating git remote $wiki_remote to $(redact_url "$wiki_url")"
      git remote set-url "$wiki_remote" "$wiki_url"
    fi
  else
    log "adding git remote $wiki_remote as $(redact_url "$wiki_url")"
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

setup_git_noninteractive
trap cleanup_git_noninteractive EXIT
sync_wiki_repo
cleanup_git_noninteractive

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
