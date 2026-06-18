#!/bin/sh
set -eu

wiki_root="${WIKIOS_WIKI_ROOT:-/data/wiki-repo}"
wiki_url="${WIKIOS_WIKI_GIT_URL:-}"
wiki_token="${WIKIOS_WIKI_GIT_TOKEN:-}"
wiki_username="${WIKIOS_WIKI_GIT_USERNAME:-x-access-token}"
wiki_remote="${WIKIOS_WIKI_GIT_REMOTE:-origin}"
wiki_branch="${WIKIOS_WIKI_GIT_BRANCH:-main}"
wiki_git_user_name="${WIKIOS_WIKI_GIT_USER_NAME:-WikiOS Bot}"
wiki_git_user_email="${WIKIOS_WIKI_GIT_USER_EMAIL:-wikios-bot@users.noreply.github.com}"
wiki_git_askpass_path="${WIKIOS_WIKI_GIT_ASKPASS_PATH:-/usr/local/bin/wikios-git-askpass}"
pull_on_start="${WIKIOS_WIKI_GIT_PULL_ON_START:-true}"
reset_on_start="${WIKIOS_WIKI_GIT_RESET_ON_START:-false}"
qmd_auto_collection="${WIKIOS_QMD_AUTO_COLLECTION:-true}"
qmd_index="${WIKIOS_QMD_INDEX:-knowledge-base}"
qmd_http_enabled="${WIKIOS_QMD_HTTP_ENABLED:-true}"
retrieval_mode="${WIKIOS_RETRIEVAL_MODE:-qmd}"

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

  cat > "$wiki_git_askpass_path" <<'EOF'
#!/bin/sh
case "$1" in
  *sername*|*Username*) printf '%s\n' "${WIKIOS_WIKI_GIT_USERNAME:-x-access-token}" ;;
  *) printf '%s\n' "$WIKIOS_WIKI_GIT_TOKEN" ;;
esac
EOF
  chmod 700 "$wiki_git_askpass_path"
  export GIT_ASKPASS="$wiki_git_askpass_path"
}

cleanup_git_noninteractive() {
  if [ -n "${askpass_dir:-}" ] && [ -d "$askpass_dir" ]; then
    rm -rf "$askpass_dir"
  fi
}

configure_wiki_git_repo() {
  [ -d "$wiki_root/.git" ] || return 0
  (
    cd "$wiki_root"
    git config core.askPass "$wiki_git_askpass_path" || true
    git config credential.username "$wiki_username" || true
    git config user.name "$wiki_git_user_name" || true
    git config user.email "$wiki_git_user_email" || true
  )
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
    configure_wiki_git_repo
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
  configure_wiki_git_repo

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

if [ "$(printf '%s' "$retrieval_mode" | tr '[:upper:]' '[:lower:]')" = "wiki" ]; then
  log "retrieval mode is wiki; skipping qmd collection/embed initialization and qmd mcp daemon"
  qmd_auto_collection=false
  qmd_http_enabled=false
fi

if is_true "$qmd_auto_collection" && [ -d "$wiki_root/wiki" ]; then
  log "ensuring qmd collection 'wiki' exists"
  (
    cd "$wiki_root"
    qmd --index "$qmd_index" collection add wiki/ --name wiki >/dev/null 2>&1 || true
    qmd --index "$qmd_index" update >/dev/null 2>&1 || true
    qmd --index "$qmd_index" embed >/dev/null 2>&1 || true
  )
fi

# The warm `qmd mcp --http` daemon only serves qmd's DEFAULT index (index.sqlite),
# not the --index used by the CLI path, so the default index must be prepared
# separately here. The index lives under /root/.cache/qmd, which is persisted by
# the qmd-cache volume, so the embed cost is only paid on the first start.
if is_true "$qmd_http_enabled" && [ -d "$wiki_root/wiki" ]; then
  log "preparing default qmd index for the warm 'qmd mcp --http' daemon (first run downloads models; can take a few minutes)"
  (
    cd "$wiki_root"
    qmd collection add wiki/ --name wiki >/dev/null 2>&1 || true
    qmd update >/dev/null 2>&1 || true
    qmd embed >/dev/null 2>&1 || true
  )
  log "starting 'qmd mcp --http' daemon on :8181 (retrieval falls back to qmd CLI if it is unavailable)"
  ( cd "$wiki_root" && qmd mcp --http ) >/tmp/qmd-mcp-http.log 2>&1 &
fi

cd /app
exec "$@"
