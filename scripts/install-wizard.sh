#!/usr/bin/env bash
# Interactive installation wizard for k8s-mcp-server.
# Picks an MCP client, toggles server start options, and emits (or writes) the
# matching configuration. Re-runnable; existing config files are backed up.
set -u

SERVER_NAME="kubernetes"
PROGRAM="k8s-mcp-server"

if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
  B=$'\033[1m'; D=$'\033[2m'; G=$'\033[32m'; C=$'\033[36m'; R=$'\033[0m'
else
  B=""; D=""; G=""; C=""; R=""
fi
bold() { printf '%s%s%s' "$B" "$*" "$R"; }
dim()   { printf '%s%s%s' "$D" "$*" "$R"; }

ANS=""
# ask "Prompt" "default" -> result in global ANS (prompt is NOT captured)
ask() {
  local prompt="$1" def="${2:-}"
  printf '%s?%s %s' "$C" "$R" "$prompt"
  [[ -n "$def" ]] && printf ' %s' "$(dim "[$def]")"
  printf ': '
  IFS= read -r ANS || ANS="$def"
  ANS="${ANS:-$def}"
}

ask_yn() {
  local prompt="$1" def="${2:-n}"
  while true; do
    ask "$prompt" "$def"
    case "${ANS,,}" in y|yes) return 0;; n|no) return 1;; esac
  done
}

# JSON array from args: json_arr a b c -> ["a","b","c"]
json_arr() {
  local s="[" first=1 a
  for a in "$@"; do
    [[ $first -eq 1 ]] && first=0 || s+=","
    a="${a//\\/\\\\}"; a="${a//\"/\\\"}"
    s+="\"$a\""
  done
  printf '%s]' "$s"
}

hr() { printf '%s── %s ──%s\n' "$D" "$*" "$R"; }

echo
bold "k8s-mcp-server — installation wizard"; echo
dim  "Configure the Kubernetes MCP server for your AI client."; echo

# --- 1. Binary --------------------------------------------------------------
hr "Step 1 of 4 · Binary"
BIN="$(command -v "$PROGRAM" 2>/dev/null || true)"
if [[ -z "$BIN" ]]; then
  echo "Couldn't find $PROGRAM on your PATH."
  if command -v brew >/dev/null 2>&1 && ask_yn "Install it with Homebrew now?" "y"; then
    brew install --cask langazov/tap/k8s-mcp-server || brew install langazov/tap/k8s-mcp-server || true
  elif ask_yn "Install with 'go install' instead?" "n"; then
    go install github.com/langazov/go-kubernetes-mcp-server/cmd/k8s-mcp-server@latest
  fi
  BIN="$(command -v "$PROGRAM" 2>/dev/null || true)"
fi
if [[ -z "$BIN" ]]; then
  echo "$(dim "warning: binary still not on PATH — set the path manually in the generated config.")"
  BIN="/usr/local/bin/$PROGRAM"
else
  echo "Using: $(dim "$BIN")"
fi

# --- 2. Cluster -------------------------------------------------------------
hr "Step 2 of 4 · Cluster"
KC_DEFAULT="${KUBECONFIG:-$HOME/.kube/config}"
ask "Path to kubeconfig" "$KC_DEFAULT"; KC="$ANS"
KC="${KC/#\~/$HOME}"
[[ -f "$KC" ]] || echo "$(dim "warning: $KC not found — server will fail until it exists")"

# --- 3. Start options (numbered toggle menu) --------------------------------
hr "Step 3 of 4 · Start options"
FLAGS=(allow-writes allow-destructive allow-debug allow-privileged-targets reveal-secrets)
DESCS=("mutating tools (scale, apply, restart)" "destructive tools (delete, cordon, drain)" "debug tools (exec, ephemeral, port-forward)" "permit kube-system / cluster-scoped targets" "allow per-call secret reveal")
declare -a ON=()
for _ in "${FLAGS[@]}"; do ON+=("n"); done

while true; do
  echo
  bold "Toggle capabilities — enter a number, or Enter to finish:"; echo
  for i in "${!FLAGS[@]}"; do
    if [[ "${ON[$i]}" == "y" ]]; then m="${G}[x]${R}"; else m="${D}[ ]${R}"; fi
    printf '  %s %d) --%-26s %s\n' "$m" "$((i+1))" "${FLAGS[$i]}" "$(dim "${DESCS[$i]}")"
  done
  ask 'Choice' ''; ans="$ANS"
  [[ -z "$ans" ]] && break
  if [[ "$ans" =~ ^[0-9]+$ ]] && (( ans >= 1 && ans <= ${#FLAGS[@]} )); then
    idx=$((ans-1))
    [[ "${ON[$idx]}" == "n" ]] && ON[$idx]="y" || ON[$idx]="n"
  fi
done

NAMESPACES=""
if ask_yn "Restrict to specific namespaces?" "n"; then
  ask "Comma-separated namespaces (e.g. team-a,team-b)"; NAMESPACES="$ANS"
fi

# destructive implies writes
[[ "${ON[1]}" == "y" ]] && ON[0]="y"

# Build the argument list (everything after the binary)
ARGS=(--kubeconfig "$KC")
for i in "${!FLAGS[@]}"; do
  [[ "${ON[$i]}" == "y" ]] && ARGS+=("--${FLAGS[$i]}")
done
if [[ -n "$NAMESPACES" ]]; then
  IFS=',' read -ra NSARR <<< "$NAMESPACES"
  for ns in "${NSARR[@]}"; do
    ns="${ns//[[:space:]]/}"
    [[ -n "$ns" ]] && ARGS+=(--namespace "$ns")
  done
fi

# --- 4. AI client -----------------------------------------------------------
hr "Step 4 of 4 · AI client"
AGENTS=(OpenCode "Claude Desktop" "Claude Code (claude CLI)" Cursor "HTTP server (shared/remote)" "Just print the command")
echo
bold "Which client do you want to configure?"; echo
for i in "${!AGENTS[@]}"; do
  printf '  %s%2d%s) %s\n' "$D" "$((i+1))" "$R" "${AGENTS[$i]}"
done
while true; do
  ask 'Select' '1'; pick="$ANS"
  if [[ "$pick" =~ ^[0-9]+$ ]] && (( pick >= 1 && pick <= ${#AGENTS[@]} )); then
    AGENT="${AGENTS[$((pick-1))]}"; break
  fi
done

# HTTP-specific options
HTTP_ARGS=()
if [[ "$AGENT" == "HTTP server (shared/remote)" ]]; then
  ask "Listen address" ":8080"; LISTEN="$ANS"
  ask "Endpoint path" "/mcp"; ENDPOINT="$ANS"
  ask "CORS origins (comma-separated, or blank)"; CORS="$ANS"
  HTTP_ARGS=(--transport http --listen "$LISTEN" --endpoint "$ENDPOINT")
  [[ -n "$CORS" ]] && HTTP_ARGS+=(--cors-origins "$CORS")
fi

# --- Result -----------------------------------------------------------------
ALL_ARGS=("${ARGS[@]}")
[[ ${#HTTP_ARGS[@]} -gt 0 ]] && ALL_ARGS+=("${HTTP_ARGS[@]}")
echo
hr "Result"

args_json="$(json_arr "${ALL_ARGS[@]}")"
cmd_json="$(json_arr "$BIN" "${ALL_ARGS[@]}")"
full_cmd="$BIN ${ALL_ARGS[*]}"

TARGET=""
ROOT_KEY=""
OBJ=""

case "$AGENT" in
  OpenCode)
    echo
    bold "Add this to your opencode config  $(dim "(project .opencode/opencode.json or ~/.config/opencode/opencode.json)")"
    cat <<EOF

{
  "mcp": {
    "$SERVER_NAME": {
      "type": "local",
      "command": $cmd_json
    }
  }
}
EOF
    TARGET="$(pwd)/.opencode/opencode.json"; ROOT_KEY="mcp"
    OBJ='{"type":"local","command":'"$cmd_json"'}'
    ;;
  "Claude Desktop")
    echo
    bold "Add this to claude_desktop_config.json"
    cat <<EOF

{
  "mcpServers": {
    "$SERVER_NAME": {
      "command": "$BIN",
      "args": $args_json
    }
  }
}
EOF
    case "$(uname -s)" in
      Darwin) TARGET="$HOME/Library/Application Support/Claude/claude_desktop_config.json" ;;
      Linux)  TARGET="$HOME/.config/Claude/claude_desktop_config.json" ;;
    esac
    ROOT_KEY="mcpServers"
    OBJ='{"command":"'"$BIN"'","args":'"$args_json"'}'
    ;;
  "Claude Code (claude CLI)")
    echo
    bold "Register the server with Claude Code:"
    echo
    printf '  claude mcp add %s --scope user -- %s\n' "$SERVER_NAME" "$full_cmd"
    echo
    dim "No file to edit — Claude Code manages the registration itself."
    ;;
  Cursor)
    echo
    bold "Add this to ~/.cursor/mcp.json"
    cat <<EOF

{
  "mcpServers": {
    "$SERVER_NAME": {
      "command": "$BIN",
      "args": $args_json
    }
  }
}
EOF
    TARGET="$HOME/.cursor/mcp.json"; ROOT_KEY="mcpServers"
    OBJ='{"command":"'"$BIN"'","args":'"$args_json"'}'
    ;;
  "HTTP server (shared/remote)")
    echo
    bold "Run the server:"
    echo
    printf '  %s\n' "$full_cmd"
    echo
    dim "Clients connect to: http://<host>${ENDPOINT:-:8080/mcp}"
    echo
    dim "Front it with mTLS or an OAuth proxy — the server does not authenticate clients."
    ;;
  "Just print the command")
    echo
    bold "Command:"
    echo
    printf '  %s\n' "$full_cmd"
    ;;
esac

# --- Optional write ---------------------------------------------------------
if [[ -n "$TARGET" ]]; then
  echo
  if ask_yn "Write/merge into $TARGET now? (backs up existing)" "n"; then
    if ! command -v jq >/dev/null 2>&1; then
      echo "$(dim "jq is required to merge safely — install jq, or paste the block above by hand.")"
    else
      mkdir -p "$(dirname "$TARGET")"
      if [[ -f "$TARGET" && -s "$TARGET" ]]; then
        cp "$TARGET" "$TARGET.bak.$(date +%s)"
        jq --argjson obj "$OBJ" ".\"$ROOT_KEY\".\"$SERVER_NAME\" = \$obj" "$TARGET" > "$TARGET.tmp"
      else
        echo "{\"$ROOT_KEY\":{}}" | jq --argjson obj "$OBJ" ".\"$ROOT_KEY\".\"$SERVER_NAME\" = \$obj" > "$TARGET.tmp"
      fi
      mv "$TARGET.tmp" "$TARGET"
      echo "${G}✔${R} Wrote $(bold "$TARGET")"
    fi
  fi
fi

echo
dim "Done. Restart your client and look for the '$SERVER_NAME' MCP server."
echo
